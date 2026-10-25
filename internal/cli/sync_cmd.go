package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"knot/internal/reconcile"
)

// syncCmd 构造 `knot sync`。
func syncCmd(a *app) *cobra.Command {
	var full bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "全量元数据对账（--full 先清空本地缓存再重建）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			db, err := a.DB(ctx)
			if err != nil {
				return err
			}
			if full {
				if err := db.ClearAll(ctx); err != nil {
					return err
				}
			}
			cli, err := a.Client()
			if err != nil {
				return err
			}
			snap, err := cli.Snapshot(ctx)
			if err != nil {
				return err
			}
			plan, err := db.ApplySnapshot(ctx, snap, snap.SyncedAt)
			if err != nil {
				return err
			}
			if plan.IsEmpty() {
				_, _ = fmt.Fprintln(a.out, "已是最新，无需变更")
				return nil
			}
			prefix := "同步完成"
			if full {
				prefix = "全量重建完成（正文缓存已清空，按需重新拉取）"
			}
			_, _ = fmt.Fprintf(a.out, "%s：写入 %d，更新 %d，删除 %d，解绑 %d\n",
				prefix,
				plan.Stats[reconcile.ActionInsert], plan.Stats[reconcile.ActionUpdate],
				plan.Stats[reconcile.ActionDelete], plan.Stats[reconcile.ActionUnlink])
			return nil
		},
	}
	cmd.Flags().BoolVar(&full, "full", false, "先清空本地缓存再全量重建")
	return cmd
}

// cacheCmd 构造 `knot cache`。
func cacheCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "本地缓存：info / clear",
	}
	cmd.AddCommand(cacheInfoCmd(a), cacheClearCmd(a))
	return cmd
}

func cacheInfoCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "显示缓存规模与最近同步时间",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			db, err := a.DB(ctx)
			if err != nil {
				return err
			}
			s, err := db.Stats(ctx)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(a.out, "笔记元数据: %d\n", s.Notes)
			_, _ = fmt.Fprintf(a.out, "标签:       %d\n", s.Tags)
			_, _ = fmt.Fprintf(a.out, "绑定:       %d\n", s.Bindings)
			_, _ = fmt.Fprintf(a.out, "正文缓存:   %d\n", s.Contents)
			if s.HasSynced {
				_, _ = fmt.Fprintf(a.out, "最近同步:   %s\n", s.LastSyncAt.UTC().Format("2006-01-02 15:04:05 UTC"))
			} else {
				_, _ = fmt.Fprintln(a.out, "最近同步:   从未同步")
			}
			return nil
		},
	}
}

func cacheClearCmd(a *app) *cobra.Command {
	var (
		content bool
		all     bool
	)
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "清空缓存（--content 只清正文，--all 全清）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if content == all {
				return fmt.Errorf("请指定 --content 或 --all 之一")
			}
			ctx := cmd.Context()
			db, err := a.DB(ctx)
			if err != nil {
				return err
			}
			if content {
				n, err := db.ClearContent(ctx)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprintf(a.out, "已清理 %d 份正文缓存\n", n)
				return nil
			}
			if err := db.ClearAll(ctx); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(a.out, "已清空全部缓存（下次 `knot sync` 重建）")
			return nil
		},
	}
	cmd.Flags().BoolVar(&content, "content", false, "只清正文懒缓存")
	cmd.Flags().BoolVar(&all, "all", false, "清空全部缓存")
	return cmd
}
