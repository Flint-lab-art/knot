// Package cli 装配 knot 的 Cobra 命令树。
//
// 约束（见 docs/ARCHITECTURE.md）：只依赖 store/sqlite 与 client，不依赖 store/pg。
// 子命令列表见 docs/CLIENT.md。
package cli

import (
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"

	"knot/internal/tui"
)

// BuildVersion 由构建方通过 -ldflags "-X knot/internal/cli.BuildVersion=..." 注入；
// 留空时 `knot --version` 回退到构建信息中的模块版本。
var BuildVersion string

// version 返回要展示的版本号。
func version() string {
	if BuildVersion != "" {
		return BuildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// NewRootCmd 构造 knot 根命令。out 用于常规输出，errOut 用于错误。
func NewRootCmd(out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "knot",
		Short: "Knot —— 个人知识库的命令行入口",
		Long: "Knot 的个人知识库客户端。\n" +
			"数据真值在服务端，本地 SQLite 仅作可重建缓存；所有写操作走 HTTP。",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)

	tui := &cobra.Command{
		Use:   "tui",
		Short: "启动交互式 TUI 主界面",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return tui.Run()
		},
	}
	root.AddCommand(tui)

	return root
}
