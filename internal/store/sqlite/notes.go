package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"knot/internal/dto"
	"knot/internal/reconcile"
)

// 本地查询与写后同步（docs/SYNC_CACHE.md「本地缓存一致性」）。
//
// 这些方法只服务于 CLI 的即时一致性：服务端写成功后立刻更新对应行，
// 失败也不回滚服务端（本地只是缓存，下次 sync 修复）。

// ListNotes 列出本地笔记元数据，按 modified_at 倒序。
//
// tag 非空时只返回带该标签的笔记。limit<=0 表示不限。
func (d *DB) ListNotes(ctx context.Context, tag string, limit int) ([]dto.Note, error) {
	query := `
		SELECT n.id, n.title, n.created_at, n.modified_at, n.visibility
		FROM note_meta n`
	args := []any{}
	if tag != "" {
		query += `
		JOIN note_tag_meta nt ON nt.note_id = n.id
		JOIN tag_meta t ON t.id = nt.tag_id
		WHERE t.name = ?`
		args = append(args, tag)
	}
	query += ` ORDER BY n.modified_at DESC, n.id DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 列出笔记: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var notes []dto.Note
	for rows.Next() {
		var (
			id, title, createdAt, modifiedAt, visibility string
		)
		if err := rows.Scan(&id, &title, &createdAt, &modifiedAt, &visibility); err != nil {
			return nil, fmt.Errorf("sqlite: 扫描笔记: %w", err)
		}
		n, err := toDTO(id, title, createdAt, modifiedAt, visibility)
		if err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: 遍历笔记: %w", err)
	}

	// 标签一次查全，避免 N+1。
	if len(notes) > 0 {
		tagMap, err := d.tagsByNote(ctx, idsOf(notes))
		if err != nil {
			return nil, err
		}
		for i := range notes {
			notes[i].Tags = tagMap[notes[i].ID]
			if notes[i].Tags == nil {
				notes[i].Tags = []string{}
			}
		}
	}
	return notes, nil
}

// GetNote 读取本地笔记元数据（不含正文）。
func (d *DB) GetNote(ctx context.Context, id uuid.UUID) (dto.Note, error) {
	var title, createdAt, modifiedAt, visibility string
	err := d.db.QueryRowContext(ctx,
		`SELECT title, created_at, modified_at, visibility FROM note_meta WHERE id = ?`, id.String()).
		Scan(&title, &createdAt, &modifiedAt, &visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return dto.Note{}, ErrNotFound
	}
	if err != nil {
		return dto.Note{}, fmt.Errorf("sqlite: 读取笔记 %s: %w", id, err)
	}
	n, err := toDTO(id.String(), title, createdAt, modifiedAt, visibility)
	if err != nil {
		return dto.Note{}, err
	}
	tags, err := d.tagsByNote(ctx, []uuid.UUID{id})
	if err != nil {
		return dto.Note{}, err
	}
	n.Tags = tags[id]
	if n.Tags == nil {
		n.Tags = []string{}
	}
	return n, nil
}

// ErrNotFound 表示本地缓存里没有这条笔记。
var ErrNotFound = errors.New("sqlite: 本地没有该笔记")

// UpsertNote 写入/更新笔记元数据与标签绑定（同一事务）。
//
// 服务端写操作成功后调用。version 不在本地表里：本地不需要乐观锁，
// 版本只在拿到完整笔记时用于下一次 PUT。
func (d *DB) UpsertNote(ctx context.Context, n dto.Note) error {
	return d.withTx(ctx, func(tx *sql.Tx) error {
		return upsertNoteTx(ctx, tx, n)
	})
}

// upsertNoteTx 是同事务版本，供 DeleteNoteCached 等复用。
func upsertNoteTx(ctx context.Context, tx *sql.Tx, n dto.Note) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO note_meta(id, title, created_at, modified_at, visibility)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			title = excluded.title,
			modified_at = excluded.modified_at,
			visibility = excluded.visibility`,
		n.ID.String(), n.Title, reconcile.FormatTime(n.CreatedAt),
		reconcile.FormatTime(n.ModifiedAt), n.Visibility,
	); err != nil {
		return fmt.Errorf("sqlite: 写入笔记 %s: %w", n.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_tag_meta WHERE note_id = ?`, n.ID.String()); err != nil {
		return fmt.Errorf("sqlite: 清理笔记绑定: %w", err)
	}
	for _, name := range n.Tags {
		tagID, err := ensureTagTx(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO note_tag_meta(note_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`,
			n.ID.String(), tagID); err != nil {
			return fmt.Errorf("sqlite: 绑定标签 %q: %w", name, err)
		}
	}
	return nil
}

// DeleteNoteCached 删除本地笔记（外键级联清绑定与正文缓存）。
// 不存在不报错（与服务端 DELETE 幂等一致）。
func (d *DB) DeleteNoteCached(ctx context.Context, id uuid.UUID) error {
	if _, err := d.db.ExecContext(ctx, `DELETE FROM note_meta WHERE id = ?`, id.String()); err != nil {
		return fmt.Errorf("sqlite: 删除本地笔记 %s: %w", id, err)
	}
	return nil
}

// SaveContent 写入/刷新正文懒缓存。
func (d *DB) SaveContent(ctx context.Context, id uuid.UUID, rawMD string, now time.Time) error {
	ts := now.UTC().Format(time.RFC3339Nano)
	if _, err := d.db.ExecContext(ctx, `
		INSERT INTO note_content_cache(note_id, raw_md, cached_at, last_access_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET
			raw_md = excluded.raw_md,
			cached_at = excluded.cached_at,
			last_access_at = excluded.last_access_at`,
		id.String(), rawMD, ts, ts,
	); err != nil {
		return fmt.Errorf("sqlite: 写入正文缓存 %s: %w", id, err)
	}
	return nil
}

// LoadContent 读取正文懒缓存；未缓存时返回 false（调用方应去服务端拉）。
// 命中时更新 last_access_at（二期 LRU 依赖它）。
func (d *DB) LoadContent(ctx context.Context, id uuid.UUID, now time.Time) (string, bool, error) {
	var rawMD string
	err := d.db.QueryRowContext(ctx,
		`SELECT raw_md FROM note_content_cache WHERE note_id = ?`, id.String()).Scan(&rawMD)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("sqlite: 读取正文缓存 %s: %w", id, err)
	}
	if _, err := d.db.ExecContext(ctx,
		`UPDATE note_content_cache SET last_access_at = ? WHERE note_id = ?`,
		now.UTC().Format(time.RFC3339Nano), id.String()); err != nil {
		// 访问时间只影响 LRU，写失败不影响读取结果。
		return rawMD, true, nil
	}
	return rawMD, true, nil
}

// ClearContent 清空正文缓存（`knot cache clear --content`），返回删除行数。
func (d *DB) ClearContent(ctx context.Context) (int64, error) {
	res, err := d.db.ExecContext(ctx, `DELETE FROM note_content_cache`)
	if err != nil {
		return 0, fmt.Errorf("sqlite: 清空正文缓存: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: 统计删除行数: %w", err)
	}
	return n, nil
}

// ClearAll 清空全部缓存（`knot cache clear --all`），一个事务内完成。
func (d *DB) ClearAll(ctx context.Context) error {
	return d.withTx(ctx, func(tx *sql.Tx) error {
		// 先删笔记（级联绑定与正文），再删标签，最后清同步状态。
		for _, q := range []string{
			`DELETE FROM note_content_cache`,
			`DELETE FROM note_tag_meta`,
			`DELETE FROM note_meta`,
			`DELETE FROM tag_meta`,
			`DELETE FROM sync_state`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("sqlite: 清空缓存: %w", err)
			}
		}
		return nil
	})
}

// CacheStats 是 `knot cache info` 的统计。
type CacheStats struct {
	Notes      int
	Tags       int
	Bindings   int
	Contents   int
	LastSyncAt time.Time
	HasSynced  bool
}

// Stats 统计本地缓存规模。
func (d *DB) Stats(ctx context.Context) (CacheStats, error) {
	var s CacheStats
	for _, q := range []struct {
		query string
		dst   *int
	}{
		{`SELECT count(*) FROM note_meta`, &s.Notes},
		{`SELECT count(*) FROM tag_meta`, &s.Tags},
		{`SELECT count(*) FROM note_tag_meta`, &s.Bindings},
		{`SELECT count(*) FROM note_content_cache`, &s.Contents},
	} {
		if err := d.db.QueryRowContext(ctx, q.query).Scan(q.dst); err != nil {
			return CacheStats{}, fmt.Errorf("sqlite: 统计缓存: %w", err)
		}
	}
	last, ok, err := d.LastSyncAt(ctx)
	if err != nil {
		return CacheStats{}, err
	}
	s.LastSyncAt, s.HasSynced = last, ok
	return s, nil
}

// tagsByNote 批量返回笔记的标签名。
func (d *DB) tagsByNote(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := make(map[uuid.UUID][]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// 用固定占位符拼 IN (?,(...))：参数是 UUID 值，不拼进 SQL 文本。
	placeholders := make([]byte, 0, len(ids)*2-1)
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id.String()
	}
	//nolint:gosec // 占位符由上面的循环生成，只有 ? 与逗号
	query := `SELECT nt.note_id, t.name FROM note_tag_meta nt
		JOIN tag_meta t ON t.id = nt.tag_id
		WHERE nt.note_id IN (` + string(placeholders) + `)
		ORDER BY nt.note_id, t.name`
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: 批量读取标签: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var noteID, name string
		if err := rows.Scan(&noteID, &name); err != nil {
			return nil, fmt.Errorf("sqlite: 扫描标签: %w", err)
		}
		id, err := uuid.Parse(noteID)
		if err != nil {
			return nil, fmt.Errorf("sqlite: 本地笔记 id 非法 %q: %w", noteID, err)
		}
		out[id] = append(out[id], name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: 遍历标签: %w", err)
	}
	return out, nil
}

// toDTO 把本地字符串列转成 dto.Note（时间解析失败说明缓存被外部改坏）。
func toDTO(id, title, createdAt, modifiedAt, visibility string) (dto.Note, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return dto.Note{}, fmt.Errorf("sqlite: 本地笔记 id 非法 %q: %w", id, err)
	}
	c, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return dto.Note{}, fmt.Errorf("sqlite: created_at 格式错误 %q: %w", createdAt, err)
	}
	m, err := time.Parse(time.RFC3339Nano, modifiedAt)
	if err != nil {
		return dto.Note{}, fmt.Errorf("sqlite: modified_at 格式错误 %q: %w", modifiedAt, err)
	}
	return dto.Note{
		ID: uid, Title: title, Visibility: visibility,
		CreatedAt: c.UTC(), ModifiedAt: m.UTC(), Tags: []string{},
	}, nil
}

func idsOf(notes []dto.Note) []uuid.UUID {
	ids := make([]uuid.UUID, len(notes))
	for i, n := range notes {
		ids[i] = n.ID
	}
	return ids
}
