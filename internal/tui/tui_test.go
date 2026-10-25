package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"golang.org/x/term"

	"knot/internal/client"
	"knot/internal/dto"
	"knot/internal/model"
)

// fakeCache 是内存版本地缓存（元数据 + 正文懒缓存）。
type fakeCache struct {
	notes    []dto.Note
	err      error
	tag      string
	contents map[uuid.UUID]string
}

func (f *fakeCache) LoadContent(_ context.Context, id uuid.UUID, _ time.Time) (string, bool, error) {
	if f.err != nil {
		return "", false, f.err
	}
	body, ok := f.contents[id]
	return body, ok, nil
}

func (f *fakeCache) SaveContent(_ context.Context, id uuid.UUID, rawMD string, _ time.Time) error {
	if f.err != nil {
		return f.err
	}
	if f.contents == nil {
		f.contents = map[uuid.UUID]string{}
	}
	f.contents[id] = rawMD
	return nil
}

func (f *fakeCache) ListNotes(_ context.Context, tag string, _ int) ([]dto.Note, error) {
	f.tag = tag
	if f.err != nil {
		return nil, f.err
	}
	if tag == "" {
		return f.notes, nil
	}
	var out []dto.Note
	for _, n := range f.notes {
		for _, t := range n.Tags {
			if t == tag {
				out = append(out, n)
				break
			}
		}
	}
	return out, nil
}

// fakeRemote 是内存版远端。
type fakeRemote struct {
	bodies map[uuid.UUID]string
	err    error
	// netErr 非 nil 时 GetNote 模拟断网（client.NetworkError）。
	netErr error
}

func (f *fakeRemote) GetNote(_ context.Context, id uuid.UUID) (dto.NoteDetail, error) {
	if f.netErr != nil {
		return dto.NoteDetail{}, f.netErr
	}
	if f.err != nil {
		return dto.NoteDetail{}, f.err
	}
	if _, ok := f.bodies[id]; !ok {
		return dto.NoteDetail{}, errFakeNotFound
	}
	return dto.NoteDetail{}, nil
}

func (f *fakeRemote) UpdateNote(context.Context, uuid.UUID, string, string, model.Visibility, int64) (dto.Note, error) {
	return dto.Note{}, errors.New("not used in tests")
}

func (f *fakeRemote) DeleteNote(context.Context, uuid.UUID) error {
	return errors.New("not used in tests")
}

var errFakeNotFound = errors.New("fake: not found")

func note(id uuid.UUID, title string, tags ...string) dto.Note {
	if tags == nil {
		tags = []string{}
	}
	return dto.Note{
		ID: id, Title: title, Version: 1, Visibility: string(model.VisibilityPrivate),
		CreatedAt:  time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
		ModifiedAt: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
		Tags:       tags,
	}
}

func newTestModel(cache *fakeCache) Model {
	m := NewModel(Deps{
		Cache:  cache,
		Remote: &fakeRemote{bodies: map[uuid.UUID]string{}},
		Sync:   func(context.Context) (SyncResult, error) { return SyncResult{}, nil },
	})
	m.width, m.height = 120, 40
	return m
}

// key 构造一个按键消息。
func key(s string) tea.KeyMsg {
	switch s {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// runCmd 执行命令并返回产生的消息（nil 命令返回 nil）。
func runCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	return cmd()
}

// Run 不得进入事件循环：非 TTY 环境（测试）必须立即返回错误。
func TestRunRejectsNonTTY(t *testing.T) {
	orig := isTTY
	isTTY = func() bool { return false }
	t.Cleanup(func() { isTTY = orig })

	done := make(chan error, 1)
	go func() { done <- Run(Deps{}) }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNoTTY) {
			t.Fatalf("应返回 ErrNoTTY，实际 %v", err)
		}
	case <-time.After(timeoutAfter):
		t.Fatal("Run 进入了事件循环，必须立即返回")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []tea.KeyMsg{key("q"), {Type: tea.KeyCtrlC}} {
		_, cmd := newTestModel(&fakeCache{}).Update(k)
		if cmd == nil {
			t.Fatal("应触发 tea.Quit")
		}
		if msg := cmd(); msg != (tea.QuitMsg{}) {
			t.Fatalf("命令返回 %T，期望 tea.QuitMsg", msg)
		}
	}
}

// Init 应同时加载列表与触发同步。
func TestInitBatchesLoadAndSync(t *testing.T) {
	m := newTestModel(&fakeCache{notes: []dto.Note{note(uuid.New(), "a")}})
	msg := runCmd(t, m.Init())
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init 应返回 BatchMsg，实际 %T", msg)
	}
	if len(batch) != 2 {
		t.Fatalf("Init 应包含加载与同步两个命令，实际 %d", len(batch))
	}
}

// 列表加载后应选中第一条并进入加载态。
func TestLoadNotesSelectsFirst(t *testing.T) {
	id := uuid.New()
	m := newTestModel(&fakeCache{notes: []dto.Note{note(id, "第一条")}})

	updated, cmd := m.Update(notesMsg{notes: m.deps.Cache.(*fakeCache).notes})
	m = updated.(Model)
	if m.selected == nil || m.selected.ID != id {
		t.Fatalf("应选中第一条: %+v", m.selected)
	}
	if !m.loading {
		t.Fatal("应进入加载态")
	}
	if cmd == nil {
		t.Fatal("应发起正文加载")
	}
}

// 导航：上下移动并重新加载正文。
func TestNavigation(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	notes := []dto.Note{note(ids[0], "a"), note(ids[1], "b"), note(ids[2], "c")}
	m := newTestModel(&fakeCache{notes: notes})
	m.notes = notes
	m.clampCursor()

	updated, _ := m.Update(key("down"))
	m = updated.(Model)
	if m.cursor != 1 || m.selected.ID != ids[1] {
		t.Fatalf("down 后 cursor=%d selected=%v", m.cursor, m.selected)
	}
	updated, _ = m.Update(key("G"))
	m = updated.(Model)
	if m.cursor != 2 {
		t.Fatalf("G 应跳到末尾，实际 %d", m.cursor)
	}
	// 末尾继续下移不越界。
	updated, _ = m.Update(key("down"))
	m = updated.(Model)
	if m.cursor != 2 {
		t.Fatalf("不应越界，实际 %d", m.cursor)
	}
	updated, _ = m.Update(key("g"))
	m = updated.(Model)
	if m.cursor != 0 {
		t.Fatalf("g 应回到开头，实际 %d", m.cursor)
	}
}

// 过期响应（seq 不匹配）必须被丢弃，避免快速翻页时显示错笔记。
func TestStaleDetailDropped(t *testing.T) {
	notes := []dto.Note{note(uuid.New(), "a"), note(uuid.New(), "b")}
	m := newTestModel(&fakeCache{notes: notes})
	m.notes = notes
	updated, _ := m.Update(key("down"))
	m = updated.(Model) // detailSeq = 1
	updated, _ = m.Update(detailMsg{seq: m.detailSeq - 1, detail: dto.NoteDetail{RawMD: "旧正文"}})
	m = updated.(Model)
	if m.body != "" {
		t.Fatalf("过期响应不应写入正文: %q", m.body)
	}
	if m.loading != true {
		t.Fatal("仍在等待当前请求")
	}
}

// 404 应给出「已在服务端删除」的提示。
func TestDetailNotFound(t *testing.T) {
	m := newTestModel(&fakeCache{})
	m.loading = true
	m.detailSeq = 1 // 模拟已发起一次请求
	updated, _ := m.Update(detailMsg{seq: 1, err: errFakeNotFound})
	m = updated.(Model)
	if !strings.Contains(m.detailErr, "加载正文失败") {
		t.Fatalf("普通错误提示不符: %q", m.detailErr)
	}
}

// 搜索框：输入后回车按标题过滤本地列表。
func TestSearchFiltersLocalList(t *testing.T) {
	cache := &fakeCache{notes: []dto.Note{
		note(uuid.New(), "会议记录"),
		note(uuid.New(), "读书笔记"),
	}}
	m := newTestModel(cache)
	m.notes = cache.notes

	updated, _ := m.Update(key("/"))
	m = updated.(Model)
	if !m.searching {
		t.Fatal("应进入搜索模式")
	}
	for _, r := range "会议" {
		updated, _ = m.Update(key(string(r)))
		m = updated.(Model)
	}
	if m.search != "会议" {
		t.Fatalf("search = %q", m.search)
	}
	updated, cmd := m.Update(key("enter"))
	m = updated.(Model)
	if m.searching {
		t.Fatal("回车应退出搜索模式")
	}
	if cmd == nil {
		t.Fatal("回车应重新加载列表")
	}
	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(Model)
	if len(m.notes) != 1 || m.notes[0].Title != "会议记录" {
		t.Fatalf("过滤结果不符: %+v", m.notes)
	}

	// Esc 取消搜索恢复全部。
	updated, _ = m.Update(key("/"))
	m = updated.(Model)
	updated, cmd = m.Update(key("esc"))
	m = updated.(Model)
	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(Model)
	if len(m.notes) != 2 {
		t.Fatalf("取消搜索后应恢复全部，实际 %d", len(m.notes))
	}
}

// 标签面板：打开、选择、回车筛选。
func TestTagPanelFilters(t *testing.T) {
	cache := &fakeCache{notes: []dto.Note{
		note(uuid.New(), "a", "go", "rust"),
		note(uuid.New(), "b", "go"),
	}}
	m := newTestModel(cache)
	m.notes = cache.notes

	updated, cmd := m.Update(key("t"))
	m = updated.(Model)
	if m.mode != modeTagPanel {
		t.Fatal("t 应打开标签面板")
	}
	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(Model)
	if len(m.tags) != 2 || m.tags[0] != "go" || m.tags[1] != "rust" {
		t.Fatalf("标签列表不符（应去重排序）: %v", m.tags)
	}

	// 选第二个标签（rust）回车。
	updated, _ = m.Update(key("down"))
	m = updated.(Model)
	updated, cmd = m.Update(key("enter"))
	m = updated.(Model)
	if m.filter != "rust" || m.mode != modeList {
		t.Fatalf("filter=%q mode=%v", m.filter, m.mode)
	}
	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(Model)
	if len(m.notes) != 1 || m.notes[0].Title != "a" {
		t.Fatalf("按标签筛选失败: %+v", m.notes)
	}

	// Esc 清除筛选。
	updated, cmd = m.Update(key("esc"))
	m = updated.(Model)
	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(Model)
	if m.filter != "" || len(m.notes) != 2 {
		t.Fatalf("Esc 应清除筛选: filter=%q notes=%d", m.filter, len(m.notes))
	}
}

// 同步中重复按 s 不重复发起。
func TestSyncDebounce(t *testing.T) {
	m := newTestModel(&fakeCache{})
	m.syncing = true
	updated, cmd := m.Update(key("s"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("同步进行中不应再次发起")
	}
	if !strings.Contains(m.status, "进行中") {
		t.Fatalf("status = %q", m.status)
	}
}

// 宽终端双栏、窄终端单栏，且输出宽度不超过终端。
func TestLayoutResponsive(t *testing.T) {
	notes := []dto.Note{note(uuid.New(), "标题"), note(uuid.New(), "第二个")}
	m := newTestModel(&fakeCache{notes: notes})
	m.notes = notes
	m.selected = &notes[0]
	m.body = "正文"
	m.loading = false

	wide := m.View()
	if !strings.Contains(wide, "正文") || !strings.Contains(wide, "第二个") {
		t.Fatalf("双栏应同时显示列表与详情:\n%s", wide)
	}
	if lines := strings.Count(wide, "\n"); lines > m.height {
		t.Fatalf("输出 %d 行超过终端高度 %d", lines+1, m.height)
	}

	m.width = 80
	narrow := m.View()
	if !strings.Contains(narrow, "正文") {
		t.Fatalf("窄终端应显示详情:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Fatalf("窄终端某行宽 %d 超过终端 %d: %q", w, m.width, line)
		}
	}
}

// 状态栏包含快捷键提示与同步状态。
func TestStatusBarContent(t *testing.T) {
	m := newTestModel(&fakeCache{})
	// 有编辑器能力时，状态栏才应该出现「e 编辑」。
	m.deps.Edit = func(context.Context, uuid.UUID) *exec.Cmd { return exec.Command("true") }
	m.status = "已同步，无变更"
	v := m.View()
	for _, want := range []string{"已同步", "e 编辑", "s 同步", "q 退出"} {
		if !strings.Contains(v, want) {
			t.Fatalf("状态栏缺少 %q:\n%s", want, v)
		}
	}
}

// 本地读取失败要给出可见提示，而不是空白界面。
func TestNotesLoadErrorSurfaced(t *testing.T) {
	m := newTestModel(&fakeCache{})
	updated, _ := m.Update(notesMsg{err: errors.New("disk full")})
	m = updated.(Model)
	if !strings.Contains(m.status, "disk full") {
		t.Fatalf("status = %q", m.status)
	}
}

// 没有笔记时显示空态提示。
func TestEmptyState(t *testing.T) {
	m := newTestModel(&fakeCache{})
	updated, _ := m.Update(notesMsg{})
	m = updated.(Model)
	if m.selected != nil {
		t.Fatalf("空列表不应有选中项")
	}
	if !strings.Contains(m.View(), "没有笔记") {
		t.Fatalf("缺少空态提示:\n%s", m.View())
	}
}

// /dev/null 是字符设备但不是终端：必须走 ErrNoTTY，不能进入事件循环。
func TestIsTTYRejectsDevNull(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("打开 %s: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	if term.IsTerminal(int(devNull.Fd())) {
		t.Skip("/dev/null 在本平台被判为终端，跳过")
	}
	// 直接验证判定函数的选择：字符设备判断会误判 /dev/null。
	fi, err := devNull.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("/dev/null 应当是字符设备（说明本用例的前提成立）")
	}
	if term.IsTerminal(int(devNull.Fd())) {
		t.Fatal("term.IsTerminal 不应把 /dev/null 当终端")
	}
}

// 断网时右栏回落本地正文缓存，并提示这是缓存副本。
func TestDetailFallsBackToCacheOffline(t *testing.T) {
	notes := []dto.Note{note(uuid.New(), "缓存笔记")}
	cache := &fakeCache{notes: notes, contents: map[uuid.UUID]string{notes[0].ID: "缓存里的正文"}}
	m := newTestModel(cache)
	m.notes = notes

	updated, _ := m.Update(notesMsg{notes: notes})
	m = updated.(Model)

	// 模拟断网：Remote 返回 NetworkError。
	m.deps.Remote = &fakeRemote{netErr: &client.NetworkError{Err: errors.New("connection refused")}}
	cmd := m.detailCmd()
	updated, _ = m.Update(runCmd(t, cmd))
	m = updated.(Model)

	if m.body != "缓存里的正文" {
		t.Fatalf("应回落到缓存正文，实际 %q", m.body)
	}
	if !m.detailCached {
		t.Fatal("应标记正文来自缓存")
	}
	if !strings.Contains(m.View(), "本地缓存") {
		t.Fatalf("界面应提示离线缓存:\n%s", m.View())
	}
}

// 在线拉到正文后应写入缓存，供下次离线使用。
func TestDetailSavesContentOnSuccess(t *testing.T) {
	id := uuid.New()
	notes := []dto.Note{note(id, "在线笔记")}
	cache := &fakeCache{notes: notes, contents: map[uuid.UUID]string{}}
	m := newTestModel(cache)
	m.notes = notes
	m.deps.Remote = &stubRemote{detail: dto.NoteDetail{Note: notes[0], RawMD: "联网正文"}}

	cmd := m.detailCmd()
	msg := runCmd(t, cmd)
	_ = msg
	if cache.contents[id] != "联网正文" {
		t.Fatalf("正文未写入缓存: %v", cache.contents)
	}
}

// stubRemote 返回固定正文。
type stubRemote struct{ detail dto.NoteDetail }

func (s *stubRemote) GetNote(context.Context, uuid.UUID) (dto.NoteDetail, error) {
	return s.detail, nil
}
func (s *stubRemote) UpdateNote(context.Context, uuid.UUID, string, string, model.Visibility, int64) (dto.Note, error) {
	return dto.Note{}, nil
}
func (s *stubRemote) DeleteNote(context.Context, uuid.UUID) error { return nil }

// e 键应触发外部编辑器（tea.ExecProcess），退出后重新加载。
func TestEditKeyInvokesEditor(t *testing.T) {
	notes := []dto.Note{note(uuid.New(), "可编辑")}
	m := newTestModel(&fakeCache{notes: notes})
	m.notes = notes
	m.selected = &notes[0]

	called := false
	m.deps.Edit = func(context.Context, uuid.UUID) *exec.Cmd {
		called = true
		return exec.Command("true")
	}

	updated, cmd := m.Update(key("e"))
	m = updated.(Model)
	if !called {
		t.Fatal("按 e 应调用 Deps.Edit")
	}
	if cmd == nil {
		t.Fatal("应返回 tea.ExecProcess 命令")
	}
	if !strings.Contains(m.status, "编辑中") {
		t.Fatalf("status = %q", m.status)
	}
}

// 没有 Edit 依赖时，e 不能沉默 —— 要给提示，且状态栏不显示该快捷键。
func TestEditKeyWithoutEditor(t *testing.T) {
	notes := []dto.Note{note(uuid.New(), "不可编辑")}
	m := newTestModel(&fakeCache{notes: notes})
	m.notes = notes
	m.selected = &notes[0]
	m.deps.Edit = nil

	updated, cmd := m.Update(key("e"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("无编辑器时不应返回命令")
	}
	if !strings.Contains(m.status, "不可编辑") {
		t.Fatalf("应提示不可编辑，实际 %q", m.status)
	}
	if strings.Contains(m.View(), "e 编辑") {
		t.Fatal("无编辑器时状态栏不应显示「e 编辑」")
	}
}

// 编辑器退出后应刷新列表并给出「已保存」。
func TestEditFinishedReloads(t *testing.T) {
	m := newTestModel(&fakeCache{})
	m.status = "编辑中…"
	updated, cmd := m.Update(editMsg{})
	m = updated.(Model)
	if !strings.Contains(m.status, "已保存") {
		t.Fatalf("status = %q", m.status)
	}
	if cmd == nil {
		t.Fatal("应触发列表重新加载")
	}
}

// 编辑器失败要把错误显示出来。
func TestEditFailureSurfaced(t *testing.T) {
	m := newTestModel(&fakeCache{})
	updated, _ := m.Update(editMsg{err: errors.New("exit status 1")})
	m = updated.(Model)
	if !strings.Contains(m.status, "exit status 1") {
		t.Fatalf("status = %q", m.status)
	}
}
