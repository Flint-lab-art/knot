// Package sqlite 是本地 SQLite 缓存实现（阶段 2）。
//
// 数据可重建：真值在服务端 PostgreSQL，本地不回写。必须开启
// PRAGMA foreign_keys = ON；迁移用 goose + //go:embed（migrations/sqlite）。
// 约束（见 docs/ARCHITECTURE.md）：cli / tui 只依赖本包与 client。
package sqlite

import "errors"

// ErrNotImplemented 表示本地缓存尚未实现（阶段 2）。
var ErrNotImplemented = errors.New("store/sqlite 尚未实现")
