-- 同步快照只有元数据，绝不 SELECT raw_md。

-- name: CountNotes :one
SELECT count(*) FROM notes;

-- name: ListNoteMeta :many
SELECT id, title, version, visibility, created_at, modified_at FROM notes
ORDER BY id
LIMIT @page_limit OFFSET @page_offset;

-- name: ListNoteTagNames :many
-- 与 ListNoteMeta 配合：一次拉全量「笔记 id → 标签名」，避免 N+1。
SELECT nt.note_id, t.name FROM note_tags nt
JOIN tags t ON t.id = nt.tag_id
ORDER BY nt.note_id, t.name;
