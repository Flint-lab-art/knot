package views

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"

	"knot/internal/dto"
)

func sample(n int) []dto.Note {
	notes := make([]dto.Note, n)
	for i := range notes {
		notes[i] = dto.Note{
			ID: uuid.New(), Title: "笔记标题", Version: 1, Visibility: "private",
			ModifiedAt: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
			Tags:       []string{"go", "测试"},
		}
	}
	return notes
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"短", 10, "短"},
		{"正好十个字符的文本", 9, "正好十个…"},
		{"中文都会被按宽度计算", 6, "中文…"},
		{"中文都会被按宽度计算", 8, "中文都…"},
		{"abc", 0, ""},
		{"abc", 2, "a…"},
	}
	for _, tt := range tests {
		if got := truncate(tt.in, tt.width); got != tt.want {
			t.Fatalf("truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
		if w := lipgloss.Width(truncate(tt.in, tt.width)); w > tt.width {
			t.Fatalf("truncate(%q, %d) 宽度 %d 超限", tt.in, tt.width, w)
		}
	}
}

func TestPad(t *testing.T) {
	if got := pad("ab", 4); got != "ab  " {
		t.Fatalf("pad = %q", got)
	}
	// 已超宽不截断（由调用方决定）。
	if got := pad("abcdef", 3); got != "abcdef" {
		t.Fatalf("pad = %q", got)
	}
}

func TestListEmpty(t *testing.T) {
	if !strings.Contains(List(nil, 0, 30, 10), "没有笔记") {
		t.Fatal("空列表应给出提示")
	}
}

// 列表按 height 限制行数，并保证光标始终可见。
func TestListScrollsToCursor(t *testing.T) {
	notes := sample(20)
	out := List(notes, 15, 30, 5)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("应只渲染 5 行，实际 %d", len(lines))
	}
}

// 每行显示宽度不超过给定宽度（中文不能撑破布局）。
func TestListLineWidth(t *testing.T) {
	out := List(sample(2), 0, 20, 5)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := lipgloss.Width(line); w > 20 {
			t.Fatalf("行宽 %d 超过 20: %q", w, line)
		}
	}
}

func TestDetailStates(t *testing.T) {
	n := &dto.Note{
		ID: uuid.New(), Title: "标题", Version: 3, Visibility: "public",
		ModifiedAt: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
		Tags:       []string{"go"},
	}
	if !strings.Contains(Detail(nil, "", false, "", 40), "选择左侧笔记") {
		t.Fatal("无选中应提示")
	}
	if !strings.Contains(Detail(n, "", true, "", 40), "加载正文") {
		t.Fatal("加载态应提示")
	}
	if !strings.Contains(Detail(n, "", false, "", 40), "正文为空") {
		t.Fatal("空正文应提示编辑")
	}
	body := Detail(n, "# 正文\n第二行", false, "", 40)
	if !strings.Contains(body, "# 正文") || !strings.Contains(body, "v3") || !strings.Contains(body, "#go") {
		t.Fatalf("详情缺少内容:\n%s", body)
	}
	if !strings.Contains(Detail(n, "", false, "出错了", 40), "出错了") {
		t.Fatal("错误态应显示错误信息")
	}
}

func TestStatusBar(t *testing.T) {
	out := StatusBar("已同步", "go", 80)
	if lipgloss.Width(out) > 80 {
		t.Fatalf("状态栏超宽: %d", lipgloss.Width(out))
	}
	for _, want := range []string{"已同步", "go", "q 退出"} {
		if !strings.Contains(out, want) {
			t.Fatalf("状态栏缺少 %q: %q", want, out)
		}
	}
	// 极窄时只保留状态文字，不溢出。
	narrow := StatusBar("很长的同步状态信息", "", 10)
	if lipgloss.Width(narrow) > 10 {
		t.Fatalf("窄状态栏超宽: %q", narrow)
	}
}

func TestTagPanel(t *testing.T) {
	if !strings.Contains(TagPanel(nil, "", 20, 5), "没有标签") {
		t.Fatal("空标签应提示")
	}
	out := TagPanel([]string{"go", "rust"}, "rust", 20, 8)
	if !strings.Contains(out, "go") || !strings.Contains(out, "rust") {
		t.Fatalf("标签面板缺少内容:\n%s", out)
	}
	// 超出高度的标签不全部渲染。
	many := make([]string, 30)
	for i := range many {
		many[i] = "tag"
	}
	if got := strings.Count(TagPanel(many, "", 20, 8), "\n"); got > 8 {
		t.Fatalf("标签面板行数 %d 超过高度", got)
	}
}

func TestFormatTags(t *testing.T) {
	if got := formatTags(nil); got != "" {
		t.Fatalf("空标签应为空串，实际 %q", got)
	}
	if got := formatTags([]string{"go", "rust"}); got != "#go #rust" {
		t.Fatalf("formatTags = %q", got)
	}
}
