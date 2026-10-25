-- 0001_init: 本地缓存表（docs/DATA_MODEL.md、docs/SYNC_CACHE.md）
--
-- 本地库可整体重建：真值在服务端 PG，这里只是缓存，不做迁移历史兼容。
-- ForeignKey 级联要求连接开启 PRAGMA foreign_keys = ON（见 store.go）。
-- 两级缓存：note_meta / tag_meta / note_tag_meta 是元索引，
-- note_content_cache 是正文懒缓存，二者分开维护。

-- +goose Up
CREATE TABLE note_meta (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    modified_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private', 'public'))
);

CREATE TABLE tag_meta (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL
);

CREATE TABLE note_tag_meta (
    note_id TEXT NOT NULL,
    tag_id TEXT NOT NULL,
    PRIMARY KEY (note_id, tag_id),
    FOREIGN KEY (note_id) REFERENCES note_meta(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tag_meta(id) ON DELETE CASCADE
);

CREATE TABLE note_content_cache (
    note_id TEXT PRIMARY KEY,
    raw_md TEXT NOT NULL,
    cached_at TEXT NOT NULL,
    last_access_at TEXT NOT NULL,
    FOREIGN KEY (note_id) REFERENCES note_meta(id) ON DELETE CASCADE
);

CREATE INDEX idx_meta_modified_at ON note_meta (modified_at DESC);

CREATE INDEX idx_note_tag_meta_tag_id ON note_tag_meta (tag_id);

CREATE INDEX idx_content_last_access ON note_content_cache (last_access_at);

CREATE TABLE sync_state (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS sync_state;
DROP TABLE IF EXISTS note_content_cache;
DROP TABLE IF EXISTS note_tag_meta;
DROP TABLE IF EXISTS tag_meta;
DROP TABLE IF EXISTS note_meta;
