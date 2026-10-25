package cli

import (
	"context"
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"

	"knot/internal/reconcile"
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
	a := newApp(out, errOut)

	root := &cobra.Command{
		Use:   "knot",
		Short: "Knot —— 个人知识库的命令行入口",
		Long: "Knot 的个人知识库客户端。\n" +
			"数据真值在服务端，本地 SQLite 仅作可重建缓存；所有写操作走 HTTP。",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
		// 全局参数在子命令执行前注入 app；子命令结束时关闭本地库。
		PersistentPreRun: func(*cobra.Command, []string) {
			a.configPath = a.flagConfig
			a.cachePath = a.flagCacheDB
		},
		PersistentPostRun: func(*cobra.Command, []string) { a.Close() },
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.PersistentFlags().StringVarP(&a.flagConfig, "config", "c", "",
		"配置文件路径（默认 $KNOT_CONFIG 或 ~/.config/knot/config.toml）")
	root.PersistentFlags().StringVar(&a.flagCacheDB, "cache-db", "",
		"本地缓存库路径（默认 [cache].path 或 ~/.cache/knot/cache.db）")

	root.AddCommand(
		newNoteCmd(a),
		editCmd(a),
		showCmd(a),
		rmCmd(a),
		listCmd(a),
		searchCmd(a),
		tagCmd(a),
		syncCmd(a),
		cacheCmd(a),
		tuiCmd(a),
	)
	return root
}

// tuiCmd 构造 `knot tui`。
func tuiCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "启动交互式 TUI 主界面",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			cli, err := a.Client()
			if err != nil {
				return err
			}
			db, err := a.DB(ctx)
			if err != nil {
				return err
			}
			return tui.Run(tui.Deps{
				Cache:  db,
				Remote: cli,
				Sync: func(ctx context.Context) (tui.SyncResult, error) {
					snap, err := cli.Snapshot(ctx)
					if err != nil {
						return tui.SyncResult{}, err
					}
					plan, err := db.ApplySnapshot(ctx, snap, snap.SyncedAt)
					if err != nil {
						return tui.SyncResult{}, err
					}
					return tui.SyncResult{
						Insert: plan.Stats[reconcile.ActionInsert],
						Update: plan.Stats[reconcile.ActionUpdate],
						Delete: plan.Stats[reconcile.ActionDelete],
						Unlink: plan.Stats[reconcile.ActionUnlink],
					}, nil
				},
			})
		},
	}
}
