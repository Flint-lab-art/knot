package tui

import (
	"fmt"
	"os"
)

// Run 启动 TUI 主循环（左栏列表 / 右栏详情 / 底部状态栏，见 docs/CLIENT.md）。
//
// 注意：阶段 1 这里不打开真实的 Bubble Tea 程序，只做终端校验后返回
// ErrNotImplemented。真实 TUI 会在阶段 6 接管 stdin，而 `go test` 的 stdin
// 在交互 shell 里就是 TTY，事件循环会让 make check 挂住；阶段 6 接入时
// 用注入 io.Reader 的方式（tea.WithInput）测试，不做 TTY 真机测试。
func Run() error {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("%w：无法读取标准输入：%v", ErrNotImplemented, err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("%w：需要交互式终端（请在 TTY 中直接运行 knot tui）", ErrNotImplemented)
	}

	return fmt.Errorf("%w：界面在阶段 6 接入", ErrNotImplemented)
}
