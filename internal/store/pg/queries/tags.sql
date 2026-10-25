-- name: UpsertTag :one
-- 绑定标签时自动建档；冲突时不改动已有标签（重命名走 RenameTag）。
INSERT INTO tags (name) VALUES (@name)
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: GetTagByName :one
SELECT * FROM tags WHERE name = @name;

-- name: ListTags :many
SELECT * FROM tags ORDER BY name;

-- name: ListPublicTags :many
-- 公开接口只列至少有 1 篇 public 笔记的标签。
SELECT t.id, t.name FROM tags t
WHERE EXISTS (
    SELECT 1 FROM note_tags nt JOIN notes n ON n.id = nt.note_id
    WHERE nt.tag_id = t.id AND n.visibility = 'public'
)
ORDER BY t.name;

-- name: RenameTag :execrows
UPDATE tags SET name = @new_name WHERE name = @old_name;

-- name: DeleteTag :execrows
-- 关联笔记不受影响，note_tags 行随之级联删除。
DELETE FROM tags WHERE name = @name;
