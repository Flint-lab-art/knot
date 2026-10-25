package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"knot/internal/client"
	"knot/internal/dto"
	"knot/internal/model"
)

// nowUTC 是时间注入点（测试可替换）。
var nowUTC = func() time.Time { return time.Now().UTC() }

// newNoteCmd 构造 `knot new`。
func newNoteCmd(a *app) *cobra.Command {
	var (
		title      string
		tags       []string
		visibility string
		body       string
	)
	cmd := &cobra.Command{
		Use:   "new [title]",
		Short: "新建笔记",
		Long:  "新建笔记。正文用 --body 给出，之后可用 `knot edit` 补全。",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if title == "" && len(args) > 0 {
				title = strings.Join(args, " ")
			}
			if title == "" && body == "" {
				return errors.New("缺少标题：用 `knot new <标题>` 或 --title")
			}
			vis := model.VisibilityPrivate
			if visibility != "" {
				vis = model.Visibility(visibility)
			}
			if !vis.Valid() {
				return fmt.Errorf("非法 visibility %q（可选 private / public）", visibility)
			}

			cli, err := a.Client()
			if err != nil {
				return err
			}
			note, err := cli.CreateNote(ctx, title, body, tags, vis)
			if err != nil {
				return err
			}
			a.syncCache(ctx)
			_, _ = fmt.Fprintf(a.out, "%s  %s\n", note.ID, note.Title)
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "笔记标题（也可作为位置参数）")
	cmd.Flags().StringSliceVar(&tags, "tags", nil, "标签，逗号分隔或重复指定")
	cmd.Flags().StringVar(&visibility, "visibility", "", "可见性：private（默认）/ public")
	cmd.Flags().StringVar(&body, "body", "", "正文内容")
	return cmd
}

// listCmd 构造 `knot list`。
func listCmd(a *app) *cobra.Command {
	var (
		tag    string
		short  bool
		asJSON bool
		limit  int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出本地缓存的笔记",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			db, err := a.DB(ctx)
			if err != nil {
				return err
			}
			notes, err := db.ListNotes(ctx, tag, limit)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(a.out, notes)
			}
			if len(notes) == 0 {
				_, _ = fmt.Fprintln(a.out, "（本地没有笔记，试跑 `knot sync`）")
				return nil
			}
			for _, n := range notes {
				_, _ = fmt.Fprintln(a.out, formatListLine(n, short))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "只列出带该标签的笔记")
	cmd.Flags().BoolVar(&short, "short", false, "只输出 id 与标题")
	cmd.Flags().BoolVar(&asJSON, "json", false, "输出 JSON")
	cmd.Flags().IntVar(&limit, "limit", 0, "最多列出多少条（0 表示不限）")
	return cmd
}

// formatListLine 渲染一行列表。
func formatListLine(n dto.Note, short bool) string {
	if short {
		return fmt.Sprintf("%s  %s", n.ID, n.Title)
	}
	line := fmt.Sprintf("%s  %s", n.ID.String()[:8], n.Title)
	if len(n.Tags) > 0 {
		line += "  #" + strings.Join(n.Tags, " #")
	}
	return line
}

// showCmd 构造 `knot show`。
func showCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <selector>",
		Short: "查看笔记（懒加载正文）",
		Args:  cobra.ExactArgs(1),
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
			detail, err := cli.GetNote(ctx, note.ID)
			if err != nil {
				return err
			}
			// 顺带刷新本地缓存，后续可离线读。
			if db, err := a.DB(ctx); err == nil {
				_ = db.UpsertNote(ctx, detail.Note)
				_ = db.SaveContent(ctx, detail.ID, detail.RawMD, nowUTC())
			}
			_, _ = fmt.Fprintf(a.out, "# %s\n", detail.Title)
			if len(detail.Tags) > 0 {
				_, _ = fmt.Fprintf(a.out, "标签: %s\n", strings.Join(detail.Tags, ", "))
			}
			_, _ = fmt.Fprintf(a.out, "可见性: %s  版本: %d\n\n", detail.Visibility, detail.Version)
			_, _ = fmt.Fprintln(a.out, detail.RawMD)
			return nil
		},
	}
}

// editCmd 构造 `knot edit`，实现 docs/CLIENT.md 的编辑流程。
func editCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <selector>",
		Short: "用 $EDITOR 编辑笔记（无变化不提交）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEdit(cmd, a, args[0])
		},
	}
}

// runEdit 是编辑流程本体，便于测试 mock 编辑器与 HTTP。
func runEdit(cmd *cobra.Command, a *app, selector string) error {
	ctx := cmd.Context()
	note, err := a.resolveNote(ctx, selector)
	if err != nil {
		return err
	}
	cli, err := a.Client()
	if err != nil {
		return err
	}
	// 始终从服务端取正文：本地缓存可能落后，用它覆盖会造成回退。
	detail, err := cli.GetNote(ctx, note.ID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			if db, dbErr := a.DB(ctx); dbErr == nil {
				_ = db.DeleteNoteCached(ctx, note.ID)
			}
			return errors.New("笔记已在服务端删除，本地缓存已清理")
		}
		return err
	}

	path, err := writeTempMarkdown(detail.ID, detail.RawMD)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path)
		}
	}()

	if err := runEditor(a, path); err != nil {
		keep = true
		return fmt.Errorf("%w（内容保留在 %s）", err, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		keep = true
		return fmt.Errorf("读回临时文件 %s: %w", path, err)
	}

	edited := normalizeMarkdown(string(raw))
	if edited == normalizeMarkdown(detail.RawMD) {
		_, _ = fmt.Fprintln(a.out, "没有变化，未提交")
		return nil
	}

	updated, err := cli.UpdateNote(ctx, detail.ID, detail.Title, edited, model.Visibility(detail.Visibility), detail.Version)
	if err != nil {
		keep = true
		switch {
		case errors.Is(err, client.ErrConflict):
			return fmt.Errorf("服务端已变更（版本冲突），请重新拉取后再编辑；本次内容保留在 %s", path)
		case errors.Is(err, client.ErrNotFound):
			if db, dbErr := a.DB(ctx); dbErr == nil {
				_ = db.DeleteNoteCached(ctx, detail.ID)
			}
			_ = os.Remove(path)
			return errors.New("笔记已在服务端删除，本地缓存已清理")
		}
		return fmt.Errorf("%w（内容保留在 %s）", err, path)
	}
	if db, err := a.DB(ctx); err == nil {
		_ = db.UpsertNote(ctx, updated)
		_ = db.SaveContent(ctx, updated.ID, edited, nowUTC())
	}
	_, _ = fmt.Fprintf(a.out, "已保存 %s  %s（版本 %d）\n", updated.ID, updated.Title, updated.Version)
	return nil
}

// rmCmd 构造 `knot rm`。
func rmCmd(a *app) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <selector>",
		Short: "删除笔记（先删服务端，再清本地缓存）",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			note, err := a.resolveNote(ctx, args[0])
			if err != nil {
				return err
			}
			if !yes {
				ok, err := confirm(a, fmt.Sprintf("删除 %s「%s」？", note.ID, note.Title))
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(a.out, "已取消")
					return nil
				}
			}
			cli, err := a.Client()
			if err != nil {
				return err
			}
			if err := cli.DeleteNote(ctx, note.ID); err != nil {
				return err
			}
			if db, err := a.DB(ctx); err == nil {
				if err := db.DeleteNoteCached(ctx, note.ID); err != nil {
					_, _ = fmt.Fprintf(a.errOut, "⚠ 服务端已删除，本地缓存清理失败（%v），稍后 `knot sync` 修复\n", err)
				}
			}
			_, _ = fmt.Fprintf(a.out, "已删除 %s\n", note.ID)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "跳过确认")
	return cmd
}

// writeTempMarkdown 把正文写到临时文件。
func writeTempMarkdown(id uuid.UUID, rawMD string) (string, error) {
	f, err := os.CreateTemp("", fmt.Sprintf("knot-edit-%s-*.md", id.String()[:8]))
	if err != nil {
		return "", fmt.Errorf("创建临时文件: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(rawMD); err != nil {
		return "", fmt.Errorf("写入临时文件: %w", err)
	}
	return f.Name(), nil
}

// normalizeMarkdown 归一化换行符与行尾空白，避免「仅空白差异」被当成修改。
func normalizeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
}
