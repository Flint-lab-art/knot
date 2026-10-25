-- 0003_drop_tag_created_at: 去掉 tags.created_at
--
-- 标签不需要创建时间：公开接口 /api/public/tags 只返回 id + name，
-- 同步快照的 Tag 也只有 id/name，多一列就多一份无用字段与 sqlc 映射。
-- 迁移只加不删的原则针对「已有数据/能力的回滚安全」；这里是无读方的
-- 冗余列，down 会补回同定义（数据不可恢复，符合预期）。

-- +goose Up
ALTER TABLE tags DROP COLUMN created_at;

-- +goose Down
ALTER TABLE tags ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();
