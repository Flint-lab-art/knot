// Package dto 定义 API 契约结构体（对照 docs/API.md）。
//
// dto 控制对外暴露的字段：公开接口不返回内部字段，同步快照不含 raw_md。
// 约束（见 docs/ARCHITECTURE.md）：只依赖标准库与 google/uuid。
package dto

import (
	"time"

	"github.com/google/uuid"
)

// Error 是统一错误响应体：{"code":409,"msg":"version conflict"}。
type Error struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// CreateNoteRequest 对应 POST /api/notes。
type CreateNoteRequest struct {
	Title      string   `json:"title"`
	RawMD      string   `json:"raw_md"`
	Tags       []string `json:"tags"`
	Visibility string   `json:"visibility"`
}

// UpdateNoteRequest 对应 PUT /api/notes/:id。
type UpdateNoteRequest struct {
	Title       string `json:"title"`
	RawMD       string `json:"raw_md"`
	Visibility  string `json:"visibility"`
	BaseVersion int64  `json:"base_version"`
}

// Note 是笔记元数据响应（不含 raw_md）。
type Note struct {
	ID         uuid.UUID `json:"id"`
	Title      string    `json:"title"`
	Version    int64     `json:"version"`
	Visibility string    `json:"visibility"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
	Tags       []string  `json:"tags"`
}

// NoteDetail 是笔记完整内容响应（含 raw_md），供 show / edit 懒加载。
type NoteDetail struct {
	Note
	RawMD string `json:"raw_md"`
}

// Tag 是标签。
type Tag struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// SyncSnapshot 对应 GET /api/sync/snapshot，不含 raw_md。
type SyncSnapshot struct {
	Notes    []Note    `json:"notes"`
	Tags     []Tag     `json:"tags"`
	SyncedAt time.Time `json:"synced_at"`
}
