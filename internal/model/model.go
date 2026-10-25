// Package model 定义两端共享的领域模型。
//
// 约束（见 docs/ARCHITECTURE.md）：只依赖标准库与 google/uuid，
// 不引入 pgx、cobra、bubbletea 等实现层依赖。
package model

import "github.com/google/uuid"

// Visibility 是笔记可见性。服务端查询层强制用它过滤公开接口。
type Visibility string

const (
	// VisibilityPrivate 仅本人可见（默认）。
	VisibilityPrivate Visibility = "private"
	// VisibilityPublic 经公开接口暴露给只读 Web。
	VisibilityPublic Visibility = "public"
)

// Valid 报告 v 是否为已定义的可见性枚举值。
func (v Visibility) Valid() bool {
	return v == VisibilityPrivate || v == VisibilityPublic
}

// Note 是一篇笔记的元数据，不含正文（raw_md）。
type Note struct {
	ID         uuid.UUID
	Title      string
	Tags       []string
	Version    int64
	Visibility Visibility
}
