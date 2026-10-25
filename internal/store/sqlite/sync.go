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

// 同步相关键（sync_state）。
const (
	// StateLastSyncAt 记录最近一次成功对账的时间（UTC RFC3339Nano）。
	StateLastSyncAt = "last_sync_at"
)

// ApplySnapshot 在一个事务里把服务端快照对账到本地缓存。
//
// 流程（docs/SYNC_CACHE.md）：读本地 → reconcile.Diff → 按序执行 → 写 last_sync_at。
// 任何一步失败整体回滚，本地保持调用前状态，下次重试。
//
// 只动元索引（note_meta / tag_meta / note_tag_meta）；正文缓存（note_content_cache）
// 保留 —— 笔记被删时由外键级联清掉，正文有变化则等 show/edit 懒加载刷新。
func (d *DB) ApplySnapshot(ctx context.Context, snap dto.SyncSnapshot, now time.Time) (reconcile.Plan, error) {
	var plan reconcile.Plan
	err := d.withTx(ctx, func(tx *sql.Tx) error {
		localNotes, localTags, err := loadLocal(ctx, tx)
		if err != nil {
			return err
		}
		plan = reconcile.Diff(snap, localNotes, localTags)
		for _, c := range plan.Changes {
			if err := applyChange(ctx, tx, c); err != nil {
				return err
			}
		}
		return setState(ctx, tx, StateLastSyncAt, now.UTC().Format(time.RFC3339Nano))
	})
	if err != nil {
		return reconcile.Plan{}, err
	}
	return plan, nil
}

// withTx 在一个事务里执行 fn；fn 返回错误或 panic 都回滚。
func (d *DB) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: 开启事务: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // 已提交时回滚是 no-op
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: 提交事务: %w", err)
	}
	return nil
}

// loadLocal 读出本地元索引（含每篇笔记的标签名），供对账比较。
func loadLocal(ctx context.Context, tx *sql.Tx) ([]reconcile.LocalNote, []reconcile.LocalTag, error) {
	tagRows, err := tx.QueryContext(ctx, `SELECT id, name FROM tag_meta ORDER BY name`)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: 读取本地标签: %w", err)
	}
	var tags []reconcile.LocalTag
	for tagRows.Next() {
		var id, name string
		if err := tagRows.Scan(&id, &name); err != nil {
			_ = tagRows.Close()
			return nil, nil, fmt.Errorf("sqlite: 扫描本地标签: %w", err)
		}
		uid, err := uuid.Parse(id)
		if err != nil {
			_ = tagRows.Close()
			return nil, nil, fmt.Errorf("sqlite: 本地标签 id 非法 %q: %w", id, err)
		}
		tags = append(tags, reconcile.LocalTag{ID: uid, Name: name})
	}
	if err := tagRows.Err(); err != nil {
		_ = tagRows.Close()
		return nil, nil, fmt.Errorf("sqlite: 遍历本地标签: %w", err)
	}
	_ = tagRows.Close()

	// 一次查询带出标签名，避免 N+1。
	rows, err := tx.QueryContext(ctx, `
		SELECT n.id, n.title, n.created_at, n.modified_at, n.visibility, COALESCE(t.name, '')
		FROM note_meta n
		LEFT JOIN note_tag_meta nt ON nt.note_id = n.id
		LEFT JOIN tag_meta t ON t.id = nt.tag_id
		ORDER BY n.modified_at DESC, n.id`)
	if err != nil {
		return nil, nil, fmt.Errorf("sqlite: 读取本地笔记: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var notes []reconcile.LocalNote
	byID := make(map[uuid.UUID]int)
	for rows.Next() {
		var id, title, createdAt, modifiedAt, visibility, tagName string
		if err := rows.Scan(&id, &title, &createdAt, &modifiedAt, &visibility, &tagName); err != nil {
			return nil, nil, fmt.Errorf("sqlite: 扫描本地笔记: %w", err)
		}
		uid, err := uuid.Parse(id)
		if err != nil {
			return nil, nil, fmt.Errorf("sqlite: 本地笔记 id 非法 %q: %w", id, err)
		}
		idx, ok := byID[uid]
		if !ok {
			notes = append(notes, reconcile.LocalNote{
				ID: uid, Title: title, CreatedAt: createdAt,
				ModifiedAt: modifiedAt, Visibility: visibility,
			})
			idx = len(notes) - 1
			byID[uid] = idx
		}
		if tagName != "" {
			notes[idx].Tags = append(notes[idx].Tags, tagName)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("sqlite: 遍历本地笔记: %w", err)
	}
	return notes, tags, nil
}

// upsertTagTx 把服务端标签落到 tag_meta，按「id 优先、其次 name」处理冲突。
//
// 重命名会让同一个 id 对应新名字，而旧名字那一行还在本地；此时如果按 name
// 做 ON CONFLICT，会撞 id 主键。所以分两步：先把旧名字行（若 id 不同）删掉，
// 再按 id upsert 改名，最后按 name 兜底插入。
func upsertTagTx(ctx context.Context, tx *sql.Tx, id uuid.UUID, name string) error {
	if id == uuid.Nil {
		return fmt.Errorf("sqlite: 标签 %q 缺少 id", name)
	}
	// 同名但 id 不同的旧行：直接删（其绑定由后续 update 重建）。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tag_meta WHERE name = ? AND id <> ?`, name, id.String()); err != nil {
		return fmt.Errorf("sqlite: 清理同名旧标签 %q: %w", name, err)
	}
	// 同 id 的行可能是旧名字，原地改名；不存在则插入。
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tag_meta(id, name) VALUES (?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name`, id.String(), name); err != nil {
		return fmt.Errorf("sqlite: 写入标签 %q: %w", name, err)
	}
	return nil
}

// ensureTagTx 确保本地有该名字的标签（本地写后同步用），返回其 id。
// 服务端没下发 id 的场合（新建标签的响应只含名字），本地生成一个稳定 id。
func ensureTagTx(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM tag_meta WHERE name = ?`, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("sqlite: 查询标签 %q: %w", name, err)
	}
	id = uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO tag_meta(id, name) VALUES (?, ?)`, id, name); err != nil {
		return "", fmt.Errorf("sqlite: 写入标签 %q: %w", name, err)
	}
	return id, nil
}

// update 会删除该笔记的全部绑定再按快照重建：快照里没带的就是要解绑的，
// 独立 unlink 变更因此是幂等的（先解绑再重建，结果一致）。
func applyChange(ctx context.Context, tx *sql.Tx, c reconcile.Change) error {
	switch c.Action {
	case reconcile.ActionInsert, reconcile.ActionUpdate:
		if c.Note == nil {
			return upsertTagTx(ctx, tx, c.TagID, c.TagName)
		}
		if c.Action == reconcile.ActionUpdate {
			// 先清绑定，避免旧标签残留；正文缓存在 applyChange 之外，不受影响。
			if _, err := tx.ExecContext(ctx, `DELETE FROM note_tag_meta WHERE note_id = ?`, c.NoteID.String()); err != nil {
				return fmt.Errorf("sqlite: 清理笔记绑定: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO note_meta(id, title, created_at, modified_at, visibility)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				title = excluded.title,
				created_at = excluded.created_at,
				modified_at = excluded.modified_at,
				visibility = excluded.visibility`,
			c.Note.ID.String(), c.Note.Title,
			reconcile.FormatTime(c.Note.CreatedAt), reconcile.FormatTime(c.Note.ModifiedAt),
			c.Note.Visibility,
		); err != nil {
			return fmt.Errorf("sqlite: 写入笔记 %s: %w", c.NoteID, err)
		}
		for _, name := range c.Note.Tags {
			var tagID string
			if err := tx.QueryRowContext(ctx, `SELECT id FROM tag_meta WHERE name = ?`, name).Scan(&tagID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("sqlite: 笔记 %s 的标签 %q 在快照里缺失", c.NoteID, name)
				}
				return fmt.Errorf("sqlite: 查询标签 %q: %w", name, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO note_tag_meta(note_id, tag_id) VALUES (?, ?) ON CONFLICT DO NOTHING`,
				c.NoteID.String(), tagID); err != nil {
				return fmt.Errorf("sqlite: 绑定标签 %q: %w", name, err)
			}
		}
		return nil

	case reconcile.ActionUnlink:
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM note_tag_meta
			WHERE note_id = ? AND tag_id = (SELECT id FROM tag_meta WHERE name = ?)`,
			c.NoteID.String(), c.TagName); err != nil {
			return fmt.Errorf("sqlite: 解绑标签 %q: %w", c.TagName, err)
		}
		return nil

	case reconcile.ActionDelete:
		if c.NoteID != uuid.Nil {
			// 外键级联清绑定与正文缓存。
			if _, err := tx.ExecContext(ctx, `DELETE FROM note_meta WHERE id = ?`, c.NoteID.String()); err != nil {
				return fmt.Errorf("sqlite: 删除笔记 %s: %w", c.NoteID, err)
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tag_meta WHERE name = ?`, c.TagName); err != nil {
			return fmt.Errorf("sqlite: 删除标签 %q: %w", c.TagName, err)
		}
		return nil

	default:
		return fmt.Errorf("sqlite: 未知对账动作 %q", c.Action)
	}
}

// setState 写入 sync_state（upsert）。
func setState(ctx context.Context, tx *sql.Tx, key, value string) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sync_state(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("sqlite: 写入 sync_state %q: %w", key, err)
	}
	return nil
}

// LastSyncAt 读取最近一次成功对账时间；从未同步过时返回零值与 false。
func (d *DB) LastSyncAt(ctx context.Context) (time.Time, bool, error) {
	var raw string
	err := d.db.QueryRowContext(ctx, `SELECT value FROM sync_state WHERE key = ?`, StateLastSyncAt).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("sqlite: 读取 last_sync_at: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("sqlite: last_sync_at 格式错误 %q: %w", raw, err)
	}
	return t, true, nil
}
