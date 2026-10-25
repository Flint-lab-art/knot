// Package handler 实现 /api/* 的 HTTP handler。
//
// 无 service 层：handler 直接调用 store 接口；公开接口在查询层强制
// visibility='public'（标签承载可见性是禁止的设计）。
package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"knot/internal/model"
	"knot/internal/store/pg"
)

// Store 是 handler 依赖的存储能力。生产实现是 *pg.Store；
// 测试用 mock 实现它，不必起 PG。
type Store interface {
	CreateNote(ctx context.Context, title, rawMD string, tags []string, vis model.Visibility) (pg.Note, error)
	GetNote(ctx context.Context, id uuid.UUID) (pg.Note, error)
	GetPublicNote(ctx context.Context, id uuid.UUID) (pg.Note, error)
	UpdateNote(ctx context.Context, id uuid.UUID, title, rawMD string, vis model.Visibility, baseVersion int64) (pg.Note, error)
	DeleteNote(ctx context.Context, id uuid.UUID) error
	ListNotes(ctx context.Context, opt pg.ListOptions) ([]pg.Note, error)
	ListTagsForNote(ctx context.Context, id uuid.UUID) ([]string, error)
	TagsByNote(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error)
	BindTag(ctx context.Context, noteID uuid.UUID, name string) error
	UnbindTag(ctx context.Context, noteID uuid.UUID, name string) error
	ListTags(ctx context.Context) ([]pg.Tag, error)
	ListPublicTags(ctx context.Context) ([]pg.Tag, error)
	RenameTag(ctx context.Context, oldName, newName string) error
	DeleteTag(ctx context.Context, name string) error
	Snapshot(ctx context.Context) (pg.Snapshot, error)
}

// API 聚合所有 handler 依赖。
type API struct {
	store Store
}

// New 构造 handler 集合。
func New(store Store) *API {
	return &API{store: store}
}

// statusFor 把 store 的业务错误映射成 HTTP 状态码；未知错误一律 500，
// 不把 SQL/堆栈透给客户端（docs/SECURITY.md）。
func statusFor(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, pg.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, pg.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, pg.ErrTagExists):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
