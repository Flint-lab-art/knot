package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Run 在阶段 1 只做终端校验，不得进入事件循环：否则 stdin 是 TTY 时
// go test 会卡住，make check / pre-commit 全部挂起。
func TestRunNeverEntersEventLoop(t *testing.T) {
	done := make(chan error, 1)
	go func() { done <- Run() }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("阶段 1 的 Run 应返回「未实现」错误")
		}
	case <-time.After(timeoutAfter):
		t.Fatal("Run 进入了事件循环（阻塞），必须立即返回")
	}
}

func TestModelQuitKeys(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
	}{
		{name: "q", msg: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}},
		{name: "ctrl+c", msg: tea.KeyMsg{Type: tea.KeyCtrlC}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, cmd := NewModel().Update(tt.msg)
			if cmd == nil {
				t.Fatalf("%s 应触发 tea.Quit", tt.name)
			}
			if msg := cmd(); msg != (tea.QuitMsg{}) {
				t.Fatalf("%s 的命令返回 %T，期望 tea.QuitMsg", tt.name, msg)
			}
		})
	}
}

func TestModelViewNonEmpty(t *testing.T) {
	if NewModel().View() == "" {
		t.Fatal("View 不应为空")
	}
}
