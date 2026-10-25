// Package tui 是 knot 的交互式终端界面（Bubble Tea）。
//
// 布局见 docs/CLIENT.md：左栏列表、右栏详情、底部状态栏；窄终端（< 100 列）
// 自动切单栏。约束（docs/ARCHITECTURE.md）：只依赖 store/sqlite 与 client。
package tui

import (
	"context"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/model"
)

// Deps 是 TUI 需要的运行时依赖，由 cmd 层装配。
type Deps struct {
	// Cache 提供本地元数据读取：列表来自本地缓存，保证秒开。
	Cache Cache
	// Remote 提供正文懒加载与写操作。
	Remote Remote
	// Sync 触发一次全量同步；由调用方实现（它同时掌握 client 与缓存）。
	Sync func(ctx context.Context) (SyncResult, error)
	// Now 可注入时钟（测试用），为 nil 时用 time.Now。
	Now func() time.Time
}

// Cache 是本地缓存的只读视图（*sqlite.DB 满足）。
type Cache interface {
	ListNotes(ctx context.Context, tag string, limit int) ([]dto.Note, error)
}

// Remote 是服务端交互的最小集合（*client.Client 满足其中列出的方法）。
type Remote interface {
	GetNote(ctx context.Context, id uuid.UUID) (dto.NoteDetail, error)
	UpdateNote(ctx context.Context, id uuid.UUID, title, rawMD string, vis model.Visibility, baseVersion int64) (dto.Note, error)
	DeleteNote(ctx context.Context, id uuid.UUID) error
}

// SyncResult 是一次同步的统计，用于状态栏提示。
type SyncResult struct {
	Insert, Update, Delete, Unlink int
}

// Changed 报告是否有实际变更。
func (r SyncResult) Changed() bool { return r.Insert+r.Update+r.Delete+r.Unlink > 0 }
