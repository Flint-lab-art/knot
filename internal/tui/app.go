package tui

import tea "github.com/charmbracelet/bubbletea"

// Model 是 Bubble Tea 根 Model。
//
// 布局（见 docs/CLIENT.md）：左栏列表、右栏详情、底部状态栏；
// 窄终端（< 100 列）自动切单栏。当前为占位实现，阶段 6 补全。
type Model struct {
	width  int
	height int
}

// NewModel 构造根 Model。
func NewModel() Model {
	return Model{}
}

// Init 实现 tea.Model。
func (m Model) Init() tea.Cmd {
	return nil
}

// Update 实现 tea.Model：目前只处理退出与窗口尺寸。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

// View 实现 tea.Model。
func (m Model) View() string {
	return "knot tui —— 骨架阶段，列表与详情待接入（阶段 6）。按 q 退出。\n"
}
