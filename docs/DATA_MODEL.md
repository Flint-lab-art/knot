# 数据模型

正文、标签、可见性统一存 PG，md 文件仅是编辑时的临时载体，不承担数据源职责。标签名建议小写、短横线分隔，服务端不强制。

## 服务端表结构（MVP）

```sql
CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE notes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title TEXT NOT NULL,
    raw_md TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    visibility TEXT NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private', 'public')),
    created_at TIMESTAMPTZ DEFAULT now(),
    modified_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE note_tags (
    note_id UUID NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, tag_id)
);

-- 二期：RAG / LLM
CREATE TABLE llm_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id UUID REFERENCES notes(id) ON DELETE CASCADE,
    messages JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE rag_chunks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id UUID REFERENCES notes(id) ON DELETE CASCADE,
    content TEXT NOT NULL,
    embedding vector(1024)
);

CREATE INDEX idx_notes_title_trgm ON notes USING gin (title gin_trgm_ops);
CREATE INDEX idx_notes_raw_md_trgm ON notes USING gin (raw_md gin_trgm_ops);
CREATE INDEX idx_notes_visibility ON notes (visibility);
CREATE INDEX idx_notes_modified_at ON notes (modified_at);
```

### 字段说明

| 字段 | 说明 |
|---|---|
| `version` | 乐观锁。每次成功 `PUT` 加 1；客户端提交时带 `base_version`，不匹配返 409 |
| `visibility` | 访问控制属性，不由用户 tag 承载。MVP 枚举 `private / public`；`unlisted` 保留枚举位，二期实现 |
| `modified_at` | 写操作时更新，用于列表排序和同步对账 |

**可见性不用 tag 承载**：tag 是用户可自由编辑的分类，误删/改名会静默改变访问控制，安全上不可接受。

## 本地缓存表结构

```sql
PRAGMA foreign_keys = ON;

CREATE TABLE note_meta (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    modified_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'private'
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

CREATE INDEX idx_content_last_access ON note_content_cache (last_access_at);

CREATE TABLE sync_state (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

`sync_state` 存本地同步元信息（如 `last_sync_at`），不做游标用（MVP 全量对账）；二期 `change_log` 上线后存 `last_seq`。

## 迁移

- 服务端：`migrations/pg/*.sql`，goose 管理，`//go:embed` 进 `knot-server`，用 `knot-server migrate` 执行。
- 本地：`migrations/sqlite/*.sql`，goose 管理，`//go:embed` 进 `knot`，CLI 启动时自动执行。
- 迁移只加不删，回滚安全；删字段/改类型需单独评估。

## 二期：change_log

有多写客户端时引入：

```sql
CREATE TABLE change_log (
    seq BIGSERIAL PRIMARY KEY,
    entity_type TEXT NOT NULL,   -- note / tag / note_tag
    entity_id TEXT NOT NULL,
    op TEXT NOT NULL,            -- upsert / delete
    changed_at TIMESTAMPTZ DEFAULT now()
);
```

- 写笔记/标签/绑定的**同一事务**里插记录；
- 删除写 `op=delete`；
- 同步接口 `GET /api/sync/changes?since_seq=N` 返回增量；
- 本地存 `last_seq`，失败不推进；
- 保留 `knot sync --full` 全量对账兜底。
