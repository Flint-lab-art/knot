package tui

import "errors"

// ErrNotImplemented 表示 TUI 的业务功能尚未接入（阶段 6）。
var ErrNotImplemented = errors.New("tui 尚未实现")
