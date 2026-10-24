package tui

import (
	"os"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestRunRequiresTTY(t *testing.T) {
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("当前在 TTY 中运行，跳过非交互终端用例")
	}
	if err := Run(); err == nil {
		t.Fatal("非 TTY 下 Run 应返回错误而不是卡住")
	}
}

func TestModelQuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		_, cmd := NewModel().Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if key == "ctrl+c" {
			_, cmd = NewModel().Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		}
		if cmd == nil {
			t.Fatalf("%q 应触发 tea.Quit", key)
		}
	}
}

func TestModelViewNonEmpty(t *testing.T) {
	if NewModel().View() == "" {
		t.Fatal("View 不应为空")
	}
}
