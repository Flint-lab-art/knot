# 同步与缓存

## 目标

列表、筛选、TUI 浏览秒开且不依赖网络；正文按需拉取；断网时可读已缓存笔记；缓存丢失可整体重建。

## 两级缓存

**常驻元索引**（`note_meta` / `tag_meta` / `note_tag_meta`）：

- 由 `knot sync` 维护，不含正文；
- 体量小，同步快；
- 支撑列表、筛选、TUI 浏览。

**正文懒缓存**（`note_content_cache`）：

- 仅在 `show` / `edit` 触发时通过 `GET /api/notes/:id` 拉取；
- 未打开的笔记正文永不下发本地；
- 二期引入 LRU，超过上限淘汰长期未访问正文，元索引保留。

## 删除主路径

删除由客户端发起，服务端执行，本地同步清理：

```text
knot rm <selector>
  → DELETE /api/notes/:id
  → 服务端在 PG 删除（唯一写入口）
  → 204
  → 本地事务删除 note_meta / note_tag_meta / note_content_cache
```

- DELETE 幂等：不存在也返回 204，方便重试。
- 失败：本地不动，提示重试。
- 断网：直接失败，不排队（无离线写作需求）。
- 编辑时 GET 返回 404：提示“笔记已在服务端删除”，同时清本地元数据与正文缓存。

## 缓存一致性对账

`knot sync` 不是删除入口，而是兜底。用于：

- 本地缓存损坏、重装、`cache clear` 后重建；
- 删除请求成功但本地更新失败；
- 标签重命名、解绑等本地缓存过期；
- 以后多写客户端时，A 删了 B 的缓存要清。

### MVP：全量快照对账

```text
GET /api/sync/snapshot
→ { notes: [{id, title, modified_at, created_at, tags}], tags: [...], synced_at }
```

本地事务：

1. 取服务端 note id 集合 `server_ids`；
2. `DELETE FROM note_meta WHERE id NOT IN server_ids`（外键级联删 `note_tag_meta` / `note_content_cache`，SQLite 开 `PRAGMA foreign_keys=ON`）；
3. 对存在的 id 做 upsert；
4. 标签同理：删本地有、服务端没有的 tag；`note_tag_meta` 按服务端返回重建；
5. 整体一个事务，失败回滚，下次重试。

覆盖：笔记删除、标签删除、标签重命名、note_tag 解绑、正文缓存清理。

**不返回 `raw_md`**，只返回元数据。

### 二期：change_log + seq

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

```text
GET /api/sync/changes?since_seq=123
→ { changes: [...], next_seq: 456 }
```

- 写笔记/标签/绑定的同一事务里插 `change_log`；
- 删除写 `op=delete`；
- 本地存 `last_seq`，失败不推进；
- 保留 `knot sync --full` 走全量快照兜底。

## 本地缓存一致性

- 写操作提交成功后立即更新本地对应记录；
- 写服务端成功但本地更新失败：标记 dirty，下次 sync 修复；
- 全局刷新：`knot sync --full`；
- 脏缓存：`knot cache clear` 一键清空后重拉。

## SQLite 注意

- 必须 `PRAGMA foreign_keys = ON`，否则 `ON DELETE CASCADE` 不生效；
- 或不依赖级联，在同一事务里手动删三张表；
- 对账在一个事务里做，失败回滚，不要“先删再拉”。

## LRU（二期）

- 索引：`note_content_cache(last_access_at)`；
- 每次读取更新 `last_access_at`；
- 超过上限时淘汰最久未访问的正文，元索引保留。
