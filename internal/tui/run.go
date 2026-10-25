package tui

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// ErrNoTTY 表示当前标准输入不是终端，TUI 无法交互。
var ErrNoTTY = errors.New("需要交互式终端（请在 TTY 中直接运行 knot tui）")

// isTTY 判断标准输入是否为终端（测试可替换）。
var isTTY = func() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Run 启动 TUI 主循环（左栏列表 / 右栏详情 / 底部状态栏，见 docs/CLIENT.md）。
//
// 没有终端时立即返回 ErrNoTTY，绝不进入事件循环 —— 否则在非交互环境
// （go test、CI、管道）里会永久阻塞。
func Run(d Deps) error {
	if !isTTY() {
		return ErrNoTTY
	}
	p := tea.NewProgram(NewModel(d), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI 退出异常: %w", err)
	}
	return nil
}
