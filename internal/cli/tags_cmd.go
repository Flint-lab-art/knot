package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"knot/internal/client"
)

// searchCmd 构造 `knot search`（服务端 pg_trgm 检索）。
func searchCmd(a *app) *cobra.Command {
	var (
		tag    string
		limit  int
		offset int
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "search <keyword>",
		Short: "服务端全文检索（标题 + 正文）",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			keyword := strings.Join(args, " ")
			if keyword == "" && tag == "" {
				return errors.New("请给出关键词，或用 --tag 过滤")
			}
			cli, err := a.Client()
			if err != nil {
				return err
			}
			list, err := cli.SearchNotes(ctx, keyword, tag, limit, offset)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(a.out, list)
			}
			if len(list.Notes) == 0 {
				_, _ = fmt.Fprintln(a.out, "没有匹配的笔记")
				return nil
			}
			for _, n := range list.Notes {
				_, _ = fmt.Fprintln(a.out, formatListLine(n, false))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "限定标签")
	cmd.Flags().IntVar(&limit, "limit", 20, "返回条数（1-200）")
	cmd.Flags().IntVar(&offset, "offset", 0, "跳过条数")
	cmd.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return cmd
}

// tagCmd 构造 `knot tag`（list / add / rm / rename）。
func tagCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "标签管理：list / add / rm / rename",
	}
	cmd.AddCommand(
		tagListCmd(a),
		tagAddCmd(a),
		tagRmCmd(a),
		tagRenameCmd(a),
	)
	return cmd
}

func tagListCmd(a *app) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出全部标签",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := a.Client()
			if err != nil {
				return err
			}
			tags, err := cli.ListTags(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(a.out, tags)
			}
			if len(tags) == 0 {
				_, _ = fmt.Fprintln(a.out, "（没有标签）")
				return nil
			}
			for _, t := range tags {
				_, _ = fmt.Fprintln(a.out, t.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	return cmd
}

// tagAddCmd：`knot tag add <selector> <tag>...`。
func tagAddCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "add <selector> <tag> [tag...]",
		Short: "给笔记加标签",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			note, err := a.resolveNote(ctx, args[0])
			if err != nil {
				return err
			}
			cli, err := a.Client()
			if err != nil {
				return err
			}
			var tags []string
			for _, name := range args[1:] {
				tags, err = cli.BindTag(ctx, note.ID, name)
				if err != nil {
					return err
				}
			}
			note.Tags = tags
			if db, err := a.DB(ctx); err == nil {
				_ = db.UpsertNote(ctx, note)
			}
			_, _ = fmt.Fprintf(a.out, "标签: %s\n", strings.Join(tags, ", "))
			return nil
		},
	}
}

// tagRmCmd：`knot tag rm <selector> <tag>...`。
func tagRmCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <selector> <tag> [tag...]",
		Short: "移除笔记的标签",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			note, err := a.resolveNote(ctx, args[0])
			if err != nil {
				return err
			}
			cli, err := a.Client()
			if err != nil {
				return err
			}
			var tags []string
			for _, name := range args[1:] {
				tags, err = cli.UnbindTag(ctx, note.ID, name)
				if err != nil {
					return err
				}
			}
			note.Tags = tags
			if db, err := a.DB(ctx); err == nil {
				_ = db.UpsertNote(ctx, note)
			}
			_, _ = fmt.Fprintf(a.out, "标签: %s\n", strings.Join(tags, ", "))
			return nil
		},
	}
}

// tagRenameCmd：`knot tag rename <old> <new>`，全局重命名。
func tagRenameCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "全局重命名标签",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cli, err := a.Client()
			if err != nil {
				return err
			}
			if err := cli.RenameTag(ctx, args[0], args[1]); err != nil {
				if errors.Is(err, client.ErrConflict) {
					return fmt.Errorf("标签 %q 已存在", args[1])
				}
				return err
			}
			a.syncCache(ctx)
			_, _ = fmt.Fprintf(a.out, "已重命名 %s → %s\n", args[0], args[1])
			return nil
		},
	}
}
