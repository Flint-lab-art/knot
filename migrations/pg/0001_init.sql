-- 0001_init: 服务端基础表（docs/DATA_MODEL.md）
--
-- 只加不删：已有表结构变更走新的迁移文件。
-- gen_random_uuid() 是 PG 13+ 内置函数，不需要 pgcrypto。
-- pg_trgm 扩展由部署步骤 CREATE EXTENSION 创建（需要超级用户），
-- 因此 0002 的 trigram 索引单独成文件，未装扩展时迁移不会被阻断。

-- +goose Up
CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    raw_md TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    visibility TEXT NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private', 'public')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    modified_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE note_tags (
    note_id UUID NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, tag_id)
);

-- 列表按 modified_at 倒序。
CREATE INDEX idx_notes_modified_at ON notes (modified_at DESC);

-- 公开接口按 visibility 过滤。
CREATE INDEX idx_notes_visibility ON notes (visibility);

-- 按标签反查笔记、标签删除级联。
CREATE INDEX idx_note_tags_tag_id ON note_tags (tag_id);

-- +goose Down
DROP TABLE IF EXISTS note_tags;
DROP TABLE IF EXISTS notes;
DROP TABLE IF EXISTS tags;
