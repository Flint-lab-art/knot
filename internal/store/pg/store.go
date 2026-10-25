// Package pg 是服务端的 PostgreSQL 存储实现（阶段 2）。
//
// sqlc 生成代码放 sqlcgen/ 子包，不手改；本包包装一层，handler 只认这里的接口。
// 约束（见 docs/ARCHITECTURE.md）：server 只依赖本包，不依赖 store/sqlite。
package pg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // 注册 "pgx" database/sql 驱动，goose 需要
	"github.com/pressly/goose/v3"

	"knot/internal/model"
	"knot/internal/store/pg/sqlcgen"
	"knot/migrations"
)

// migrationsDir 是 migrations.FS 下的服务端子目录。
const migrationsDir = "pg"

// 分页与快照的边界，与 docs/API.md 约定一致。
const (
	// MaxLimit 是列表接口允许的最大 limit。
	MaxLimit = 200
	// DefaultLimit 是不传 limit 时的页大小。
	DefaultLimit = 50
	// snapshotPageSize 是快照分页读取的批大小：PG 不允许在事务里切连接，
	// 分批读避免一次缓存全部笔记正文。
	snapshotPageSize = 500
)

// 业务错误。handler 只按 errors.Is 判断，不关心 SQL 细节。
var (
	// ErrNotFound 表示资源不存在（含已删除、非 public 的笔记）。
	ErrNotFound = errors.New("pg: 资源不存在")
	// ErrConflict 表示 base_version 与当前 version 不一致。
	ErrConflict = errors.New("pg: 版本冲突")
	// ErrTagExists 表示重命名目标标签已存在。
	ErrTagExists = errors.New("pg: 标签已存在")
)

// 更新笔记失败时区分「不存在」与「版本冲突」：冲突需额外确认行还在。
const checkNoteExists = `SELECT EXISTS (SELECT 1 FROM notes WHERE id = $1)`

// Store 是 PG 存储。查询实现是 sqlcgen.Queries，可换成事务版。
type Store struct {
	pool    *pgxpool.Pool
	queries *sqlcgen.Queries
}

// Open 建立连接池并 ping 一次。
//
// schema 迁移不走这里，由 `knot-server migrate` 单独执行；
// 迁移失败不该影响已在运行的旧版本服务。
func Open(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, fmt.Errorf("pg: 空 DSN")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pg: 创建连接池: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pg: 连接数据库: %w", err)
	}
	return New(pool), nil
}

// New 用已有的连接池构造 Store（测试注入用）。
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, queries: sqlcgen.New(pool)}
}

// Pool 暴露底层连接池，供 sqlc 生成的查询使用。
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Queries 返回非事务版查询句柄。
func (s *Store) Queries() *sqlcgen.Queries { return s.queries }

// Ping 检查数据库是否可达（healthz 用）。
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// Close 关闭连接池。
func (s *Store) Close() { s.pool.Close() }

// withTx 在一个事务里执行 fn，fn 的返回值就是提交后返回给调用方的值。
// fn 返回错误则回滚；panic 也会回滚。
func (s *Store) withTx(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pg: 开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }() // 已提交时回滚是 no-op
	if err := fn(sqlcgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pg: 提交事务: %w", err)
	}
	return nil
}

// ---------------- 笔记 ----------------

// CreateNote 新建笔记，笔记与标签在同一事务里写入。
//
// tags 里的空串会被忽略；重复标签去重。返回的 Note 带服务端生成的时间与版本。
func (s *Store) CreateNote(ctx context.Context, title, rawMD string, tags []string, vis model.Visibility) (Note, error) {
	if !vis.Valid() {
		return Note{}, fmt.Errorf("pg: 非法 visibility %q", vis)
	}
	var note Note
	err := s.withTx(ctx, func(q *sqlcgen.Queries) error {
		created, err := q.CreateNote(ctx, sqlcgen.CreateNoteParams{
			Title:      title,
			RawMd:      rawMD,
			Visibility: string(vis),
		})
		if err != nil {
			return fmt.Errorf("pg: 新建笔记: %w", err)
		}
		for _, name := range dedupeTags(tags) {
			if _, err := q.UpsertTag(ctx, name); err != nil {
				return fmt.Errorf("pg: 新建标签 %q: %w", name, err)
			}
			if err := q.BindTag(ctx, sqlcgen.BindTagParams{NoteID: created.ID, TagName: name}); err != nil {
				return fmt.Errorf("pg: 绑定标签 %q: %w", name, err)
			}
		}
		note = toNote(created)
		return nil
	})
	return note, err
}

// GetNote 取完整笔记（含正文），私有接口用。
func (s *Store) GetNote(ctx context.Context, id uuid.UUID) (Note, error) {
	note, err := s.queries.GetNote(ctx, id)
	if err != nil {
		return Note{}, notFoundAs(err, "pg: 读取笔记")
	}
	return toNote(note), nil
}

// GetPublicNote 取公开笔记，非 public 与不存在同样返回 ErrNotFound。
func (s *Store) GetPublicNote(ctx context.Context, id uuid.UUID) (Note, error) {
	note, err := s.queries.GetPublicNote(ctx, id)
	if err != nil {
		return Note{}, notFoundAs(err, "pg: 读取公开笔记")
	}
	return toNote(note), nil
}

// UpdateNote 乐观锁更新：base_version 不匹配返回 ErrConflict，笔记不存在返回 ErrNotFound。
func (s *Store) UpdateNote(ctx context.Context, id uuid.UUID, title, rawMD string, vis model.Visibility, baseVersion int64) (Note, error) {
	if !vis.Valid() {
		return Note{}, fmt.Errorf("pg: 非法 visibility %q", vis)
	}
	note, err := s.queries.UpdateNote(ctx, sqlcgen.UpdateNoteParams{
		Title:       title,
		RawMd:       rawMD,
		Visibility:  string(vis),
		ID:          id,
		BaseVersion: clampInt32(baseVersion),
	})
	if err == nil {
		return toNote(note), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Note{}, fmt.Errorf("pg: 更新笔记: %w", err)
	}
	// 更新 0 行：要么不存在，要么版本不符。
	var exists bool
	if qerr := s.pool.QueryRow(ctx, checkNoteExists, id).Scan(&exists); qerr != nil {
		return Note{}, fmt.Errorf("pg: 确认笔记存在: %w", qerr)
	}
	if !exists {
		return Note{}, ErrNotFound
	}
	return Note{}, ErrConflict
}

// DeleteNote 删除笔记；不存在也返回 nil（DELETE 幂等）。
func (s *Store) DeleteNote(ctx context.Context, id uuid.UUID) error {
	if _, err := s.queries.DeleteNote(ctx, id); err != nil {
		return fmt.Errorf("pg: 删除笔记: %w", err)
	}
	return nil
}

// ListOptions 是笔记列表 / 搜索的查询条件。
type ListOptions struct {
	// Visibility 为空表示不限（私有接口）；公开接口固定 public。
	Visibility model.Visibility
	// Keywords 为空串表示不按正文/标题过滤。
	Keywords string
	// Tag 为空表示不按标签过滤。
	Tag string
	// Cursor 是上一页最后一条的 modified_at；零值表示第一页。
	Cursor time.Time
	// CursorID 是上一页最后一条的 id，与 Cursor 一起保证翻页稳定。
	CursorID *uuid.UUID
	Limit    int
	Offset   int
}

// NormalizeLimit 把 limit 收敛到 [1, MaxLimit]，0 视为 DefaultLimit。
func NormalizeLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}

// ListNotes 按 modified_at 倒序翻页。
//
// 带 Offset 或 Keywords/Tag 时走搜索查询（OFFSET 分页），否则走游标分页：
// 游标比 offset 稳定，翻页期间插入新笔记不会导致重复/漏读。
// Visibility 为空表示不限（私有接口）；公开接口必须显式传 public。
func (s *Store) ListNotes(ctx context.Context, opt ListOptions) ([]Note, error) {
	limit := clampInt32(int64(NormalizeLimit(opt.Limit)))
	if opt.Offset > 0 || opt.Keywords != "" || opt.Tag != "" {
		notes, err := s.queries.SearchNotes(ctx, sqlcgen.SearchNotesParams{
			Keyword:    opt.Keywords,
			TagName:    optionalText(opt.Tag),
			Visibility: optionalText(string(opt.Visibility)),
			PageOffset: clampInt32(int64(opt.Offset)),
			PageLimit:  limit,
		})
		if err != nil {
			return nil, fmt.Errorf("pg: 搜索笔记: %w", err)
		}
		return toNotes(notes), nil
	}
	notes, err := s.queries.ListNotes(ctx, sqlcgen.ListNotesParams{
		Visibility:       optionalText(string(opt.Visibility)),
		CursorModifiedAt: optionalTimestamptz(opt.Cursor),
		CursorID:         optionalUUID(opt.CursorID),
		PageLimit:        limit,
	})
	if err != nil {
		return nil, fmt.Errorf("pg: 列出笔记: %w", err)
	}
	return toNotes(notes), nil
}

// TagsByNote 批量返回笔记的标签名，值为按名字排序的切片。
// 没有标签的笔记不会出现在 map 里（调用方按空切片处理）。
func (s *Store) TagsByNote(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := make(map[uuid.UUID][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.queries.ListTagsForNotes(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("pg: 批量读取笔记标签: %w", err)
	}
	for _, r := range rows {
		out[r.NoteID] = append(out[r.NoteID], r.Name)
	}
	return out, nil
}

// ---------------- 标签 ----------------

// ListTagsForNote 返回笔记的标签名（按名字排序），不含该笔记时是空切片。
func (s *Store) ListTagsForNote(ctx context.Context, id uuid.UUID) ([]string, error) {
	rows, err := s.queries.ListTagsForNote(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("pg: 读取笔记标签: %w", err)
	}
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Name
	}
	return names, nil
}

// BindTag 给笔记绑定标签；标签不存在时自动新建。笔记不存在返回 ErrNotFound。
func (s *Store) BindTag(ctx context.Context, noteID uuid.UUID, name string) error {
	if name == "" {
		return fmt.Errorf("pg: 空标签名")
	}
	return s.withTx(ctx, func(q *sqlcgen.Queries) error {
		if _, err := q.GetNote(ctx, noteID); err != nil {
			return notFoundAs(err, "pg: 绑定标签前确认笔记")
		}
		if _, err := q.UpsertTag(ctx, name); err != nil {
			return fmt.Errorf("pg: 新建标签 %q: %w", name, err)
		}
		if err := q.BindTag(ctx, sqlcgen.BindTagParams{NoteID: noteID, TagName: name}); err != nil {
			return fmt.Errorf("pg: 绑定标签 %q: %w", name, err)
		}
		return nil
	})
}

// UnbindTag 解绑标签；标签或绑定不存在都不报错（幂等）。
func (s *Store) UnbindTag(ctx context.Context, noteID uuid.UUID, name string) error {
	if err := s.queries.UnbindTag(ctx, sqlcgen.UnbindTagParams{NoteID: noteID, TagName: name}); err != nil {
		return fmt.Errorf("pg: 解绑标签 %q: %w", name, err)
	}
	return nil
}

// ListTags 列出全部标签（私有接口）。
func (s *Store) ListTags(ctx context.Context) ([]Tag, error) {
	tags, err := s.queries.ListTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg: 列出标签: %w", err)
	}
	return toTags(tags), nil
}

// ListPublicTags 只列出至少关联一篇 public 笔记的标签。
func (s *Store) ListPublicTags(ctx context.Context) ([]Tag, error) {
	tags, err := s.queries.ListPublicTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("pg: 列出公开标签: %w", err)
	}
	return toTags(tags), nil
}

// RenameTag 全局重命名标签；old 不存在返回 ErrNotFound，new 已存在返回 ErrTagExists。
func (s *Store) RenameTag(ctx context.Context, oldName, newName string) error {
	if newName == "" {
		return fmt.Errorf("pg: 空标签名")
	}
	if oldName == newName {
		return nil
	}
	n, err := s.queries.RenameTag(ctx, sqlcgen.RenameTagParams{OldName: oldName, NewName: newName})
	if err != nil {
		if isUniqueViolation(err) {
			return ErrTagExists
		}
		return fmt.Errorf("pg: 重命名标签 %q: %w", oldName, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTag 删除标签；不存在返回 ErrNotFound。关联笔记保留，绑定级联删除。
func (s *Store) DeleteTag(ctx context.Context, name string) error {
	n, err := s.queries.DeleteTag(ctx, name)
	if err != nil {
		return fmt.Errorf("pg: 删除标签 %q: %w", name, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------- 同步快照 ----------------

// SnapshotNote 是快照里的一条笔记元数据：不含正文。
type SnapshotNote struct {
	ID         uuid.UUID
	Title      string
	Version    int32
	Visibility model.Visibility
	CreatedAt  time.Time
	ModifiedAt time.Time
	Tags       []string
}

// Snapshot 是全量元数据快照，对应 GET /api/sync/snapshot。
type Snapshot struct {
	Notes    []SnapshotNote
	Tags     []Tag
	SyncedAt time.Time
}

// Snapshot 读取全量元数据快照。
//
// 所有查询在同一个只读事务里完成，避免读到半途的写入；按 id 分页而不是
// modified_at，并投影掉 raw_md。
func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	var snap Snapshot
	err := s.withTx(ctx, func(q *sqlcgen.Queries) error {
		total, err := q.CountNotes(ctx)
		if err != nil {
			return fmt.Errorf("pg: 统计笔记: %w", err)
		}
		notes := make([]SnapshotNote, 0, total)
		for offset := int64(0); offset < total; offset += snapshotPageSize {
			rows, err := q.ListNoteMeta(ctx, sqlcgen.ListNoteMetaParams{
				PageLimit:  snapshotPageSize,
				PageOffset: clampInt32(offset),
			})
			if err != nil {
				return fmt.Errorf("pg: 读取快照笔记: %w", err)
			}
			for _, r := range rows {
				notes = append(notes, SnapshotNote{
					ID:         r.ID,
					Title:      r.Title,
					Version:    r.Version,
					Visibility: model.Visibility(r.Visibility),
					CreatedAt:  r.CreatedAt,
					ModifiedAt: r.ModifiedAt,
				})
			}
			if len(rows) == 0 {
				break // 读取期间有删除，避免死循环
			}
		}
		bindings, err := q.ListNoteTagNames(ctx)
		if err != nil {
			return fmt.Errorf("pg: 读取快照标签: %w", err)
		}
		tags, err := q.ListTags(ctx)
		if err != nil {
			return fmt.Errorf("pg: 读取快照标签列表: %w", err)
		}
		snap.Tags = toTags(tags)
		byNote := make(map[uuid.UUID][]string, len(notes))
		for _, b := range bindings {
			byNote[b.NoteID] = append(byNote[b.NoteID], b.Name)
		}
		for i := range notes {
			notes[i].Tags = byNote[notes[i].ID]
			if notes[i].Tags == nil {
				notes[i].Tags = []string{}
			}
		}
		snap.Notes = notes
		snap.SyncedAt = time.Now().UTC()
		return nil
	})
	return snap, err
}

// ---------------- 对外视图类型 ----------------
//
// handler 只认这些类型，不直接依赖 sqlcgen（生成代码改动不应波及 API 层）。

// Note 是一篇笔记：正文与元数据一起返回。Version 用 int64 与 dto 对齐。
type Note struct {
	ID         uuid.UUID
	Title      string
	RawMD      string
	Version    int64
	Visibility model.Visibility
	CreatedAt  time.Time
	ModifiedAt time.Time
}

// Tag 是标签。
type Tag struct {
	ID   uuid.UUID
	Name string
}

func toNote(n sqlcgen.Note) Note {
	return Note{
		ID:         n.ID,
		Title:      n.Title,
		RawMD:      n.RawMd,
		Version:    int64(n.Version),
		Visibility: model.Visibility(n.Visibility),
		CreatedAt:  n.CreatedAt,
		ModifiedAt: n.ModifiedAt,
	}
}

func toNotes(ns []sqlcgen.Note) []Note {
	out := make([]Note, len(ns))
	for i, n := range ns {
		out[i] = toNote(n)
	}
	return out
}

func toTags(ts []sqlcgen.Tag) []Tag {
	out := make([]Tag, len(ts))
	for i, t := range ts {
		out[i] = Tag{ID: t.ID, Name: t.Name}
	}
	return out
}

// ---------------- 辅助 ----------------

// notFoundAs 把 pgx 的 no rows 映射成 ErrNotFound，其余错误加前缀返回。
func notFoundAs(err error, prefix string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// optionalText 把空串映射成 SQL NULL（sqlc 用 pgtype 表示可空参数）。
func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}

func optionalTimestamptz(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

func optionalUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *id, Valid: true}
}

// dedupeTags 去掉空串与重复，保持原顺序。
func dedupeTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// clampInt32 防止 int64 溢出 sqlc 生成的 int32 参数（分页上限远小于 int32）。
func clampInt32(v int64) int32 {
	const maxInt32 = 1<<31 - 1
	switch {
	case v < 0:
		return 0
	case v > maxInt32:
		return maxInt32
	default:
		return int32(v)
	}
}

// Migrate 执行 migrations/pg/ 下所有未应用的迁移。
//
// 迁移文件内嵌在二进制里（//go:embed），服务器不需要装 goose 命令。
// 0002 依赖 pg_trgm 扩展，需事先由管理员 CREATE EXTENSION pg_trgm。
func Migrate(ctx context.Context, dsn string) error {
	if dsn == "" {
		return fmt.Errorf("pg: 空 DSN")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("pg: 打开连接: %w", err)
	}
	defer func() { _ = db.Close() }()

	// goose 在 PG 上会取会话级咨询锁，避免多个实例并发迁移。
	sub, err := fs.Sub(migrations.FS, migrationsDir)
	if err != nil {
		return fmt.Errorf("pg: 读取内嵌迁移: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub)
	if err != nil {
		return fmt.Errorf("pg: 初始化迁移: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("pg: 执行迁移: %w", err)
	}
	return nil
}
