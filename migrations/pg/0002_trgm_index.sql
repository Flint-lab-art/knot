-- 0002_trgm_index: pg_trgm 检索索引（docs/API.md 的 /api/search）
--
-- 依赖 pg_trgm 扩展。迁移不负责 CREATE EXTENSION（需要超级用户），
-- 部署时由管理员在目标库执行一次：
--
--   CREATE EXTENSION IF NOT EXISTS pg_trgm;
--
-- 未装扩展时本迁移会失败；确认已装后重跑 `knot-server migrate` 即可。
-- Down 不删扩展：主键、其他表也可能依赖它。

-- +goose Up
CREATE INDEX idx_notes_title_trgm ON notes USING gin (title gin_trgm_ops);
CREATE INDEX idx_notes_raw_md_trgm ON notes USING gin (raw_md gin_trgm_ops);

-- +goose Down
DROP INDEX IF EXISTS idx_notes_raw_md_trgm;
DROP INDEX IF EXISTS idx_notes_title_trgm;
