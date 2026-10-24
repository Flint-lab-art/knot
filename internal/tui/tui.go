package tui

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrNotImplemented 表示 TUI 的业务功能尚未接入（阶段 6）。
var ErrNotImplemented = errors.New("tui 尚未实现")

// Run 启动 TUI 主循环（左栏列表 / 右栏详情 / 底部状态栏，见 docs/CLIENT.md）。
func Run() error {
	if fi, err := os.Stdin.Stat(); err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%w：需要交互式终端（请在 TTY 中直接运行 knot tui）", ErrNotImplemented)
	}

	_, err := tea.NewProgram(NewModel(), tea.WithAltScreen()).Run()
	return err
}
