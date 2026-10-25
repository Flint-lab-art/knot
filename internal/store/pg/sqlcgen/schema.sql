-- sqlc 专用 schema：由 migrations/pg/*.sql 的 Up 段落拼接而成，禁止手改。
-- 重新生成：make sqlc-schema（改完迁移后必须跑）。
--
-- 为什么要副本：goose 的 `-- +goose Up/Down` 在同一文件里混排，
-- sqlc 的 schema 解析器无法判断哪部分是最终结构。

-- ---- migrations/pg/0001_init.sql ----

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
CREATE INDEX idx_notes_modified_at ON notes (modified_at DESC);
CREATE INDEX idx_notes_visibility ON notes (visibility);
CREATE INDEX idx_note_tags_tag_id ON note_tags (tag_id);

-- ---- 0002_trgm_index.sql（索引与 sqlc 无关，略）----

-- ---- migrations/pg/0003_drop_tag_created_at.sql ----

ALTER TABLE tags DROP COLUMN created_at;
