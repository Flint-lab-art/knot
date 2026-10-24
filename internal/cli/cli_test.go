package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCmdVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := NewRootCmd(&out, &errOut)
	cmd.SetArgs([]string{"--version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("--version 失败: %v", err)
	}
	if !strings.Contains(out.String(), "knot version") {
		t.Fatalf("输出 = %q，期望包含 \"knot version\"", out.String())
	}
}

func TestRootCmdTUIReportsPending(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := NewRootCmd(&out, &errOut)
	cmd.SetArgs([]string{"tui"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("tui 未实现时应返回错误")
	}
}
