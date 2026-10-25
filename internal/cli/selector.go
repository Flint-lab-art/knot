package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/store/sqlite"
)

// parseUUID 尝试把字符串解析成 UUID。
func parseUUID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// matchNotes 在本地缓存里按标题做不区分大小写的子串匹配。
//
// 本地没有缓存时返回空切片，调用方的提示会引导用户先 `knot sync`。
func matchNotes(ctx context.Context, db *sqlite.DB, selector string) ([]dto.Note, error) {
	all, err := db.ListNotes(ctx, "", 0)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(selector)
	var out []dto.Note
	for _, n := range all {
		if strings.Contains(strings.ToLower(n.Title), needle) {
			out = append(out, n)
		}
	}
	return out, nil
}

// ambiguous 构造「匹配到多条」的错误，列出候选便于用户改用 UUID。
func ambiguous(selector string, matches []dto.Note) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q 匹配到 %d 篇笔记，请改用更精确的关键词或 UUID：\n", selector, len(matches))
	for _, n := range matches {
		fmt.Fprintf(&b, "  %s  %s\n", n.ID, n.Title)
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}
