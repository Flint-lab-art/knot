# 数据模型

正文、标签、可见性统一存 PG，md 文件仅是编辑时的临时载体，不承担数据源职责。标签名建议小写、短横线分隔，服务端不强制。

## 服务端表结构（MVP）

```sql
CREATE TABLE tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE
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
- 两端文件由 `migrations/migrations.go` 的 `embed.FS` 统一内嵌（`//go:embed` 不允许 `..`，故放在仓库根）。
- 每个文件都写 `-- +goose Up` / `-- +goose Down`，down 只删本文件建的对象。

### 已落地的迁移文件

| 文件 | 内容 |
|---|---|
| `pg/0001_init.sql` | `tags` / `notes` / `note_tags`，`visibility` CHECK，`modified_at` / `visibility` / `note_tags(tag_id)` 索引 |
| `pg/0002_trgm_index.sql` | `title` / `raw_md` 的 gin trigram 索引，**依赖 `pg_trgm` 扩展** |
| `pg/0003_drop_tag_created_at.sql` | 去掉 `tags.created_at`（没有读方，快照与公开接口的 Tag 只有 `id/name`） |
| `sqlite/0001_init.sql` | `note_meta` / `tag_meta` / `note_tag_meta` / `note_content_cache` / `sync_state` 及索引 |

### 列表排序：不用 `sort_ord`

`docs/API.md` 规定按 `modified_at` 倒序 + `limit/offset`。`offset` 在翻页期间有写入时会重复/漏读，
所以列表接口改用**游标分页**：`WHERE (modified_at, id) < (:cursor_modified_at, :cursor_id)`
配合 `ORDER BY modified_at DESC, id DESC`（`id` 兜底保证同一时刻的多条记录顺序稳定）。
搜索接口因为要按标签/关键词过滤且 Web 需要页码，保留 `offset`。

因此不引入 `sort_ord` 列：排序键完全由 `modified_at` + `id` 决定，少一列就少一处同步点。
`modified_at` 由 `PUT` 的 `modified_at = now()` 维护；新建时由默认值填充。

### 快照的读取方式

`store/pg` 的 `Snapshot` 在**一个只读事务**里按 `id` 分页读元数据（每批 500 行），
再一次性读「笔记 id → 标签名」映射和标签列表，避免 N+1，也不在事务里切连接。
投影里没有 `raw_md`；`SnapshotNote` 的字段集合就是契约，集成测试会断言它没多出字段。

**扩展不写进迁移**：`CREATE EXTENSION` 需要超级用户，而服务以低权角色运行；0001 不依赖它，所以缺扩展时基础表照样建好，只有 0002 失败。部署时先由管理员执行 `CREATE EXTENSION IF NOT EXISTS pg_trgm;`（本地用 `make db-setup`），再跑 `knot-server migrate`。

SQLite 侧 `store/sqlite.Open` 在 DSN 上挂 `_pragma=foreign_keys(1)`，**每个连接**都开外键；`journal_mode=WAL`、`busy_timeout=5000` 同处设置。测试用 `:memory:` 时固定单连接（`cache=shared`），否则各连接各自一个空库。

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
