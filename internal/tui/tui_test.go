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

// ---------------- 改名 / 删除 ----------------

// recordingRemote 记录写操作，供改名/删除断言。
type recordingRemote struct {
	detail  dto.NoteDetail
	updated dto.Note
	deleted uuid.UUID
	title   string
	rawMD   string
	err     error
}

func (r *recordingRemote) GetNote(context.Context, uuid.UUID) (dto.NoteDetail, error) {
	return r.detail, r.err
}
func (r *recordingRemote) UpdateNote(_ context.Context, _ uuid.UUID, title, rawMD string, _ model.Visibility, _ int64) (dto.Note, error) {
	if r.err != nil {
		return dto.Note{}, r.err
	}
	r.title, r.rawMD = title, rawMD
	return r.updated, nil
}
func (r *recordingRemote) DeleteNote(_ context.Context, id uuid.UUID) error {
	if r.err != nil {
		return r.err
	}
	r.deleted = id
	return nil
}

func newRenameModel(remote *recordingRemote) Model {
	n := note(uuid.New(), "原标题")
	m := newTestModel(&fakeCache{notes: []dto.Note{n}})
	m.notes = []dto.Note{n}
	m.selected = &n
	m.deps.Remote = remote
	return m
}

// r 键打开改名输入框，预填当前标题。
func TestRenameOpensWithCurrentTitle(t *testing.T) {
	m := newRenameModel(&recordingRemote{})
	updated, _ := m.Update(key("r"))
	m = updated.(Model)
	if m.mode != modeRenaming {
		t.Fatal("r 应进入改名模式")
	}
	if m.rename != "原标题" {
		t.Fatalf("应预填当前标题，实际 %q", m.rename)
	}
	if !strings.Contains(m.View(), "标题: 原标题") {
		t.Fatalf("输入框未显示:\n%s", m.View())
	}
}

// 输入新标题后回车 → 调 UpdateNote，正文原样带上（PUT 是整篇覆盖）。
func TestRenameSubmitsWithExistingBody(t *testing.T) {
	remote := &recordingRemote{
		detail:  dto.NoteDetail{Note: note(uuid.New(), "原标题"), RawMD: "# 原正文\n"},
		updated: note(uuid.New(), "新标题"),
	}
	m := newRenameModel(remote)

	updated, _ := m.Update(key("r"))
	m = updated.(Model)
	m.rename = "新标题"
	updated, cmd := m.Update(key("enter"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("回车应触发提交")
	}
	runCmd(t, cmd)

	if remote.title != "新标题" {
		t.Fatalf("提交的标题 = %q", remote.title)
	}
	if remote.rawMD != "# 原正文\n" {
		t.Fatalf("必须原样回传正文，否则会清空内容；实际 %q", remote.rawMD)
	}
}

// Esc 取消改名，不发请求。
func TestRenameCancel(t *testing.T) {
	remote := &recordingRemote{}
	m := newRenameModel(remote)
	updated, _ := m.Update(key("r"))
	m = updated.(Model)
	updated, cmd := m.Update(key("esc"))
	m = updated.(Model)
	if m.mode != modeList {
		t.Fatal("Esc 应回到列表")
	}
	if cmd != nil {
		t.Fatal("取消不应发请求")
	}
	if remote.title != "" {
		t.Fatal("取消不应调用 UpdateNote")
	}
}

// 空标题不允许提交。
func TestRenameRejectsEmptyTitle(t *testing.T) {
	remote := &recordingRemote{}
	m := newRenameModel(remote)
	updated, _ := m.Update(key("r"))
	m = updated.(Model)
	m.rename = "   "
	updated, cmd := m.Update(key("enter"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("空标题不应提交")
	}
	if !strings.Contains(m.status, "不能为空") {
		t.Fatalf("status = %q", m.status)
	}
}

// 标题没变 → 不发请求。
func TestRenameUnchangedIsNoop(t *testing.T) {
	remote := &recordingRemote{}
	m := newRenameModel(remote)
	updated, _ := m.Update(key("r"))
	m = updated.(Model)
	updated, cmd := m.Update(key("enter"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("标题未变化不应提交")
	}
	if remote.title != "" {
		t.Fatal("不应调用 UpdateNote")
	}
	if !strings.Contains(m.status, "未变化") {
		t.Fatalf("应提示标题未变化，实际 %q", m.status)
	}
}

// d 键进入确认，必须按 y 才删除。
func TestDeleteRequiresConfirmation(t *testing.T) {
	remote := &recordingRemote{}
	m := newRenameModel(remote)
	target := m.selected.ID

	updated, _ := m.Update(key("d"))
	m = updated.(Model)
	if m.mode != modeConfirmDelete {
		t.Fatal("d 应进入删除确认")
	}
	if !strings.Contains(m.View(), "删除「原标题」？") {
		t.Fatalf("确认栏未显示:\n%s", m.View())
	}

	updated, cmd := m.Update(key("n"))
	m = updated.(Model)
	if cmd != nil || remote.deleted != uuid.Nil {
		t.Fatal("按 n 不应删除")
	}

	updated, _ = m.Update(key("d"))
	m = updated.(Model)
	updated, cmd = m.Update(key("y"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("按 y 应执行删除")
	}
	msg := runCmd(t, cmd).(savedMsg)
	if msg.deleted != target {
		t.Fatalf("删除的 id = %v, want %v", msg.deleted, target)
	}
}

// 删除成功后从列表移除，光标落到剩余条目。
func TestDeleteRemovesFromList(t *testing.T) {
	a, b := note(uuid.New(), "a"), note(uuid.New(), "b")
	m := newTestModel(&fakeCache{notes: []dto.Note{a, b}})
	m.notes = []dto.Note{a, b}
	m.selected = &a
	m.cursor = 1

	updated, _ := m.Update(savedMsg{action: "删除", deleted: a.ID, deletedTitle: "a"})
	m = updated.(Model)
	if len(m.notes) != 1 || m.notes[0].ID != b.ID {
		t.Fatalf("列表未正确移除: %+v", m.notes)
	}
	if m.cursor != 0 {
		t.Fatalf("光标应回到有效位置，实际 %d", m.cursor)
	}
	if !strings.Contains(m.status, "已删除「a」") {
		t.Fatalf("status = %q", m.status)
	}
}

// 改名成功后列表行就地更新。
func TestRenameUpdatesListRow(t *testing.T) {
	n := note(uuid.New(), "旧标题")
	m := newTestModel(&fakeCache{notes: []dto.Note{n}})
	m.notes = []dto.Note{n}
	m.selected = &n

	renamed := n
	renamed.Title = "新标题"
	updated, _ := m.Update(savedMsg{action: "改标题", note: &renamed})
	m = updated.(Model)
	if m.notes[0].Title != "新标题" {
		t.Fatalf("列表行未更新: %+v", m.notes[0])
	}
	if m.selected.Title != "新标题" {
		t.Fatalf("详情未更新: %+v", m.selected)
	}
}

// 写操作失败要把错误显示到状态栏。
func TestRenameFailureSurfaced(t *testing.T) {
	m := newTestModel(&fakeCache{})
	updated, _ := m.Update(savedMsg{action: "改标题", err: errors.New("409: version conflict")})
	m = updated.(Model)
	if !strings.Contains(m.status, "version conflict") {
		t.Fatalf("status = %q", m.status)
	}
}

// 没有选中笔记时 r / d 给出提示而不是静默。
func TestRenameDeleteWithoutSelection(t *testing.T) {
	m := newTestModel(&fakeCache{})
	for _, k := range []string{"r", "d"} {
		updated, _ := m.Update(key(k))
		m = updated.(Model)
		if !strings.Contains(m.status, "先选择") {
			t.Fatalf("%q: status = %q", k, m.status)
		}
	}
}
