package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// writeJSON 以带缩进的 JSON 输出。
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("输出 JSON: %w", err)
	}
	return nil
}

// runEditor 用配置里的 editor.command 打开文件。
//
// editor 的 stdin/stdout/stderr 直接连到进程本身：TUI 编辑器（nvim / vim）
// 必须拿到真实终端才能工作。Editor.Args 放在文件名之前。
func runEditor(a *app, path string) error {
	cfg, err := a.Config()
	if err != nil {
		return err
	}
	args := append(append([]string{}, cfg.Editor.Args...), path)
	c := exec.Command(cfg.Editor.Command, args...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("编辑器 %s 退出异常: %w", cfg.Editor.Command, err)
	}
	return nil
}

// confirm 在终端上询问是否继续；从管道等非交互输入读取（读不到视为否）。
func confirm(a *app, prompt string) (bool, error) {
	_, _ = fmt.Fprintf(a.errOut, "%s [y/N] ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("读取确认输入: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}
