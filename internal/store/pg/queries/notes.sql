-- ---------------- notes ----------------

-- name: CreateNote :one
INSERT INTO notes (title, raw_md, visibility)
VALUES (@title, @raw_md, @visibility)
RETURNING *;

-- name: GetNote :one
-- 只有 private 接口用；公开接口走 GetPublicNote。
SELECT * FROM notes WHERE id = @id::uuid;

-- name: GetPublicNote :one
-- 查询层强制 visibility = 'public'：非 public 笔记在这里就是 not found。
SELECT * FROM notes
WHERE id = @id::uuid AND visibility = 'public';

-- name: UpdateNote :one
-- 乐观锁：base_version 不匹配时更新 0 行，调用方据此返回 409。
UPDATE notes
SET title = @title,
    raw_md = @raw_md,
    visibility = @visibility,
    version = version + 1,
    modified_at = now()
WHERE id = @id::uuid AND version = @base_version
RETURNING *;

-- name: DeleteNote :execrows
DELETE FROM notes WHERE id = @id::uuid;

-- name: ListNotes :many
-- cursor 为空表示第一页；否则取 (modified_at, id) 严格小于游标的记录。
-- visibility 为空表示不限（私有接口），公开接口必须传 'public'。
SELECT * FROM notes
WHERE (sqlc.narg(visibility)::text IS NULL OR visibility = sqlc.narg(visibility)::text)
  AND ((sqlc.narg(cursor_modified_at)::timestamptz IS NULL AND sqlc.narg(cursor_id)::uuid IS NULL)
       OR (modified_at, id) < (sqlc.narg(cursor_modified_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY modified_at DESC, id DESC
LIMIT @page_limit;

-- name: SearchNotes :many
-- keyword 匹配 title 或 raw_md（pg_trgm 的 ILIKE 走 gin_trgm_ops 索引）。
-- visibility 为空表示不限，公开接口必须传 'public'。
SELECT * FROM notes
WHERE (sqlc.narg(visibility)::text IS NULL OR visibility = sqlc.narg(visibility)::text)
  AND (@keyword::text = '' OR title ILIKE '%' || @keyword::text || '%'
                        OR raw_md ILIKE '%' || @keyword::text || '%')
  AND (sqlc.narg(tag_name)::text IS NULL
       OR EXISTS (SELECT 1 FROM note_tags nt JOIN tags t ON t.id = nt.tag_id
                  WHERE nt.note_id = notes.id AND t.name = sqlc.narg(tag_name)::text))
ORDER BY modified_at DESC, id DESC
LIMIT @page_limit OFFSET @page_offset;

-- ---------------- 笔记 ↔ 标签 ----------------

-- name: ListTagsForNote :many
SELECT t.id, t.name FROM tags t
JOIN note_tags nt ON nt.tag_id = t.id
WHERE nt.note_id = @note_id::uuid
ORDER BY t.name;

-- name: ListTagsForNotes :many
-- 列表页一次取回多篇笔记的标签，避免 N+1。
SELECT nt.note_id, t.name FROM note_tags nt
JOIN tags t ON t.id = nt.tag_id
WHERE nt.note_id = ANY(@note_ids::uuid[])
ORDER BY nt.note_id, t.name;

-- name: BindTag :exec
-- 标签不存在时自动新建；并发下先 INSERT ... ON CONFLICT 拿到 id。
INSERT INTO note_tags (note_id, tag_id)
SELECT @note_id::uuid, id FROM tags WHERE name = @tag_name
ON CONFLICT DO NOTHING;

-- name: UnbindTag :exec
DELETE FROM note_tags nt
USING tags t
WHERE nt.tag_id = t.id AND nt.note_id = @note_id::uuid AND t.name = @tag_name;
