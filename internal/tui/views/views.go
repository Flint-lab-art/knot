package views

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"knot/internal/dto"
)

var (
	// StyleSelected 是当前选中行（反色强调，不依赖颜色）。
	StyleSelected = lipgloss.NewStyle().Reverse(true)
	// StyleMuted 是次要信息（标签、时间）。
	StyleMuted = lipgloss.NewStyle().Faint(true)
	// StyleCursor 是选中行的行首标记。
	StyleCursor = lipgloss.NewStyle().Bold(true)
	// StyleTitle 是面板标题。
	StyleTitle = lipgloss.NewStyle().Bold(true)
	// StyleEmpty 是空态文字。
	StyleEmpty = lipgloss.NewStyle().Faint(true).Italic(true)
	// StyleError 是错误提示。
	StyleError = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// List 渲染左栏笔记列表。
//
// 每行是「标题 + 标签」，宽度不足时截断标题（不折行，保证行数稳定）。
// cursor 是当前选中索引；offset 是滚动起点，保证光标始终可见。
func List(notes []dto.Note, cursor, width, height int) string {
	if len(notes) == 0 {
		return StyleEmpty.Render("（没有笔记）\n\n按 s 同步，q 退出")
	}
	offset := 0
	if cursor < offset {
		offset = cursor
	}
	if height > 0 && cursor >= offset+height {
		offset = cursor - height + 1
	}
	end := len(notes)
	if height > 0 && offset+height < end {
		end = offset + height
	}

	var b strings.Builder
	for i := offset; i < end; i++ {
		n := notes[i]
		line := truncate(n.Title, width-2)
		if tags := formatTags(n.Tags); tags != "" {
			line += " " + StyleMuted.Render(truncate(tags, width-2-len([]rune(line))))
		}
		// 行首留一个空格做缩进，总宽恰好是 width。
		if i == cursor {
			b.WriteString(StyleSelected.Render(" " + pad(line, width-1)))
		} else {
			b.WriteString(" " + pad(line, width-1))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Detail 渲染右栏：标题、元信息、正文（或占位提示）。
func Detail(n *dto.Note, body string, loading bool, errMsg string, width int) string {
	if errMsg != "" {
		return StyleError.Render(errMsg)
	}
	if n == nil {
		return StyleEmpty.Render("选择左侧笔记查看正文")
	}
	var b strings.Builder
	b.WriteString(StyleTitle.Render(truncate(n.Title, width)))
	b.WriteString("\n")
	meta := fmt.Sprintf("%s  v%d  %s", n.Visibility, n.Version, n.ModifiedAt.Format("2006-01-02 15:04"))
	if tags := formatTags(n.Tags); tags != "" {
		meta += "  " + tags
	}
	b.WriteString(StyleMuted.Render(truncate(meta, width)))
	b.WriteString("\n\n")
	switch {
	case loading:
		b.WriteString(StyleMuted.Render("加载正文…"))
	case body == "":
		b.WriteString(StyleEmpty.Render("（正文为空，按 e 编辑）"))
	default:
		b.WriteString(body)
	}
	return b.String()
}

// StatusBar 渲染底部状态栏：同步状态 / 当前筛选 / 快捷键。
func StatusBar(status, filter string, width int) string {
	left := status
	if filter != "" {
		left += "  [" + filter + "]"
	}
	keys := "e 编辑  t 标签  / 搜索  s 同步  q 退出"
	// 宽度不够时先牺牲快捷键提示。
	if lipgloss.Width(left)+lipgloss.Width(keys)+2 > width {
		return truncate(left, width)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(keys) - 1
	if gap < 1 {
		gap = 1
	}
	return StyleSelected.Render(left + strings.Repeat(" ", gap) + keys)
}

// TagPanel 渲染标签面板（t 键）：列出所有标签及数量，当前标签高亮。
func TagPanel(tags []string, current string, width, height int) string {
	if len(tags) == 0 {
		return StyleEmpty.Render("（没有标签）")
	}
	// 留 1 行标题 + 1 行空行 + 1 行提示。
	visible := height - 3
	if visible < 1 {
		visible = 1
	}
	start := 0
	if current != "" {
		for i, t := range tags {
			if t == current && i >= visible {
				start = i - visible + 1
			}
		}
	}
	if start+visible > len(tags) {
		start = max(0, len(tags)-visible)
	}

	var b strings.Builder
	b.WriteString(StyleTitle.Render("标签") + "\n")
	for _, t := range tags[start:min(len(tags), start+visible)] {
		line := " " + truncate(t, width-2)
		if t == current {
			b.WriteString(StyleSelected.Render(pad(line, width)) + "\n")
			continue
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + StyleMuted.Render("回车筛选，Esc 关闭"))
	return b.String()
}

// formatTags 把标签拼成 "#a #b"。
func formatTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	parts := make([]string, len(tags))
	for i, t := range tags {
		parts[i] = "#" + t
	}
	return strings.Join(parts, " ")
}

// truncate 按显示宽度截断，避免中文/emoji 把行撑破。
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if w+rw > width-1 { // 留一位给省略号
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String() + "…"
}

// pad 把字符串补齐到指定显示宽度（不换行）。
func pad(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}
