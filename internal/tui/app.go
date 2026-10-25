package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"knot/internal/client"
	"knot/internal/dto"
	"knot/internal/tui/views"
)

// 布局阈值与尺寸。
const (
	// narrowWidth 以下切单栏（docs/CLIENT.md）。
	narrowWidth = 100
	// listWidth 是双栏时左栏宽度。
	listWidth = 34
	// statusBarHeight 是底部状态栏占用的行数。
	statusBarHeight = 1
	// rpcTimeout 是单次网络请求超时。
	rpcTimeout = 10 * time.Second
	// syncTimeout 是全量同步超时。
	syncTimeout = 30 * time.Second
)

// mode 是界面模式。
type mode int

const (
	modeList mode = iota
	modeSearch
	modeTagPanel
)

// Model 是 Bubble Tea 根 Model。
type Model struct {
	deps Deps

	width, height int
	mode          mode

	notes  []dto.Note
	cursor int

	// filter 是按标签筛选；searching/search 是搜索框状态。
	filter    string
	searching bool
	search    string

	// 详情
	selected     *dto.Note
	body         string
	loading      bool
	detailErr    string
	detailSeq    int // 丢弃过期响应
	detailCached bool

	// 标签面板
	tags      []string
	tagCursor int

	// 状态栏
	status  string
	syncing bool
}

// NewModel 构造根 Model。
func NewModel(d Deps) Model {
	if d.Now == nil {
		d.Now = time.Now
	}
	return Model{deps: d, status: "就绪"}
}

// Init 实现 tea.Model：加载本地列表，并在后台同步一次。
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadNotes(), m.syncCmd())
}

// ---------------- 消息 ----------------

type notesMsg struct {
	notes []dto.Note
	err   error
}

type detailMsg struct {
	seq    int
	detail dto.NoteDetail
	err    error
	// cached 表示正文来自本地缓存（离线回落），状态栏要提示。
	cached bool
}

type syncMsg struct {
	res SyncResult
	err error
}

type tagsMsg struct{ tags []string }

// Update 实现 tea.Model。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case notesMsg:
		if msg.err != nil {
			m.status = "读取本地失败: " + msg.err.Error()
			return m, nil
		}
		m.notes = msg.notes
		m.clampCursor()
		return m, m.detailCmd()

	case tagsMsg:
		m.tags = msg.tags
		if m.tagCursor >= len(m.tags) {
			m.tagCursor = 0
		}
		return m, nil

	case detailMsg:
		// 过期响应（用户已切换笔记）直接丢弃。
		if msg.seq != m.detailSeq {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			switch {
			case errors.Is(msg.err, client.ErrNotFound):
				m.detailErr = "笔记已在服务端删除，按 s 同步清理"
			case errors.Is(msg.err, client.ErrUnauthorized):
				m.detailErr = "token 无效，检查配置文件"
			default:
				m.detailErr = "加载正文失败: " + msg.err.Error()
			}
			return m, nil
		}
		m.detailErr = ""
		m.body = msg.detail.RawMD
		m.detailCached = msg.cached
		note := msg.detail.Note
		m.selected = &note
		// 用服务端最新元数据刷新列表行。
		for i := range m.notes {
			if m.notes[i].ID == note.ID {
				m.notes[i] = note
				break
			}
		}
		return m, nil

	case syncMsg:
		m.syncing = false
		if msg.err != nil {
			m.status = "同步失败: " + msg.err.Error()
			return m, nil
		}
		if msg.res.Changed() {
			m.status = fmt.Sprintf("已同步：写入 %d / 更新 %d / 删除 %d / 解绑 %d",
				msg.res.Insert, msg.res.Update, msg.res.Delete, msg.res.Unlink)
		} else {
			m.status = "已同步，无变更"
		}
		return m, m.loadNotes()
	}
	return m, nil
}

// handleKey 分发按键。
func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	if m.searching {
		return m.handleSearchKey(k)
	}
	if m.mode == modeTagPanel {
		return m.handleTagKey(k)
	}

	switch k.String() {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, m.detailCmd()
	case "down", "j":
		if m.cursor < len(m.notes)-1 {
			m.cursor++
		}
		return m, m.detailCmd()
	case "g":
		m.cursor = 0
		return m, m.detailCmd()
	case "G":
		if len(m.notes) > 0 {
			m.cursor = len(m.notes) - 1
		}
		return m, m.detailCmd()
	case "/":
		m.searching = true
		m.search = ""
		return m, nil
	case "t":
		m.mode = modeTagPanel
		return m, m.loadTagsCmd()
	case "s":
		if m.syncing {
			m.status = "同步进行中…"
			return m, nil
		}
		m.syncing = true
		m.status = "同步中…"
		return m, m.syncCmd()
	case "esc":
		// 清除标签筛选。
		if m.filter != "" {
			m.filter = ""
			m.status = "已清除筛选"
			return m, m.loadNotes()
		}
	}
	return m, nil
}

// handleSearchKey 处理搜索框输入。
//
// 搜索在本地缓存里做子串匹配：TUI 的定位是「秒开浏览」，
// 服务端 pg_trgm 检索走 `knot search`，避免每敲一个字就打一次 API。
func (m Model) handleSearchKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		m.searching = false
		m.search = ""
		m.status = "已取消搜索"
		return m, m.loadNotes()
	case tea.KeyEnter:
		m.searching = false
		m.status = fmt.Sprintf("搜索：%q", m.search)
		return m, m.loadNotes()
	case tea.KeyBackspace:
		if r := []rune(m.search); len(r) > 0 {
			m.search = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.search += string(k.Runes)
		return m, nil
	}
	return m, nil
}

// handleTagKey 处理标签面板。
func (m Model) handleTagKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "t", "q":
		m.mode = modeList
		return m, nil
	case "up", "k":
		if m.tagCursor > 0 {
			m.tagCursor--
		}
		return m, nil
	case "down", "j":
		if m.tagCursor < len(m.tags)-1 {
			m.tagCursor++
		}
		return m, nil
	case "enter":
		if len(m.tags) > 0 {
			m.filter = m.tags[m.tagCursor]
			m.status = "筛选标签：" + m.filter
		}
		m.mode = modeList
		return m, m.loadNotes()
	}
	return m, nil
}

// View 实现 tea.Model。
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "knot tui 加载中…"
	}

	bodyHeight := m.height - statusBarHeight
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	if m.mode == modeTagPanel {
		panel := views.TagPanel(m.tags, m.tagLabel(), m.width-2, bodyHeight)
		return fit(panel, m.width, bodyHeight) + "\n" + views.StatusBar("标签  ↑/↓ 选择  回车筛选  Esc 关闭", "", m.width)
	}

	bar := views.StatusBar(m.status, m.filterChip(), m.width)
	if m.searching {
		bar = views.StatusBar("/"+m.search+"_", "", m.width)
	}

	if m.width < narrowWidth {
		return fit(m.narrowView(), m.width, bodyHeight) + "\n" + bar
	}

	left := views.List(m.notes, m.cursor, listWidth-2, bodyHeight)
	right := views.Detail(m.selected, m.body, m.loading, m.detailErr, m.width-listWidth-2)
	if m.detailCached && m.detailErr == "" {
		right = views.StyleMuted.Render("（离线：显示本地缓存正文）") + "\n" + right
	}
	leftBox := lipgloss.NewStyle().Width(listWidth - 1).Height(bodyHeight).
		BorderStyle(lipgloss.NormalBorder()).BorderRight(true).Render(left)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftBox, " "+right) + "\n" + bar
}

// narrowView 是 < 100 列时的单栏视图：一行标题 + 详情。
func (m Model) narrowView() string {
	header := "（无选中）"
	if m.selected != nil {
		header = m.selected.Title
	}
	rest := views.Detail(m.selected, m.body, m.loading, m.detailErr, m.width)
	if m.detailCached && m.detailErr == "" {
		rest = views.StyleMuted.Render("（离线：显示本地缓存正文）") + "\n" + rest
	}
	return views.StyleTitle.Render(truncatePlain(header, m.width)) + "\n" + rest
}

// ---------------- 命令 ----------------

// loadNotes 读取本地列表（按 filter/search 过滤）。
func (m Model) loadNotes() tea.Cmd {
	deps, filter, search := m.deps, m.filter, m.search
	return func() tea.Msg {
		if deps.Cache == nil {
			return notesMsg{err: errors.New("未配置本地缓存")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		notes, err := deps.Cache.ListNotes(ctx, filter, 0)
		if err != nil {
			return notesMsg{err: err}
		}
		if search != "" {
			needle := strings.ToLower(search)
			kept := notes[:0]
			for _, n := range notes {
				if strings.Contains(strings.ToLower(n.Title), needle) {
					kept = append(kept, n)
				}
			}
			notes = kept
		}
		return notesMsg{notes: notes}
	}
}

// loadTagsCmd 从本地列表收集标签供面板展示。
func (m Model) loadTagsCmd() tea.Cmd {
	deps := m.deps
	return func() tea.Msg {
		if deps.Cache == nil {
			return tagsMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		all, err := deps.Cache.ListNotes(ctx, "", 0)
		if err != nil {
			return tagsMsg{}
		}
		seen := map[string]struct{}{}
		tags := make([]string, 0, 8)
		for _, n := range all {
			for _, t := range n.Tags {
				if _, ok := seen[t]; ok {
					continue
				}
				seen[t] = struct{}{}
				tags = append(tags, t)
			}
		}
		sort.Strings(tags)
		return tagsMsg{tags: tags}
	}
}

// detailCmd 懒加载当前选中笔记的正文。
//
// 指针接收者：需要更新 detailSeq / selected / loading（值接收者改不到调用方）。
func (m *Model) detailCmd() tea.Cmd {
	if m.cursor < 0 || m.cursor >= len(m.notes) {
		m.selected, m.body, m.detailErr = nil, "", ""
		return nil
	}
	n := m.notes[m.cursor]
	m.detailSeq++
	seq := m.detailSeq
	m.selected = &n
	m.loading = true
	m.detailErr = ""
	m.detailCached = false
	m.body = ""
	deps := m.deps
	now := m.deps.Now()
	return func() tea.Msg {
		if deps.Remote == nil {
			return detailMsg{seq: seq, err: errors.New("未配置远端")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		defer cancel()
		detail, err := deps.Remote.GetNote(ctx, n.ID)
		if err == nil {
			// 成功拉到就缓存正文，供离线阅读（docs/SYNC_CACHE.md）。
			if deps.Cache != nil {
				_ = deps.Cache.SaveContent(ctx, detail.ID, detail.RawMD, now)
			}
			return detailMsg{seq: seq, detail: detail}
		}

		// 连不上服务端时回落本地正文缓存；4xx 是确定答案，不回落到缓存。
		var ne *client.NetworkError
		if !errors.As(err, &ne) || deps.Cache == nil {
			return detailMsg{seq: seq, err: err}
		}
		body, ok, cacheErr := deps.Cache.LoadContent(ctx, n.ID, now)
		if cacheErr != nil || !ok {
			return detailMsg{seq: seq, err: err}
		}
		meta := n
		return detailMsg{seq: seq, detail: dto.NoteDetail{Note: meta, RawMD: body}, cached: true}
	}
}

// syncCmd 触发一次全量同步。
func (m Model) syncCmd() tea.Cmd {
	if m.deps.Sync == nil {
		return nil
	}
	sync := m.deps.Sync
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		res, err := sync(ctx)
		return syncMsg{res: res, err: err}
	}
}

// ---------------- 辅助 ----------------

func (m *Model) clampCursor() {
	if len(m.notes) == 0 {
		m.cursor = 0
		m.selected, m.body, m.detailErr = nil, "", ""
		return
	}
	if m.cursor >= len(m.notes) {
		m.cursor = len(m.notes) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m Model) filterChip() string {
	switch {
	case m.searching:
		return ""
	case m.search != "" && m.filter != "":
		return m.filter + " /" + m.search
	case m.filter != "":
		return m.filter
	case m.search != "":
		return "/" + m.search
	}
	return ""
}

func (m Model) tagLabel() string { return m.filter }

// fit 裁剪/补齐到指定宽高，避免布局抖动。
func fit(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for i := range lines {
		lines[i] = truncatePlain(lines[i], width)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// truncatePlain 按显示宽度截断。
func truncatePlain(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > width {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}
