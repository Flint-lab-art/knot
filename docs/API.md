# API

## 通用约定

- 鉴权：私有接口用 `Authorization: Bearer <token>`；公开接口无 token。
- 时间：RFC3339（UTC），响应里的时间一律转成 UTC 再输出。
- 错误统一返回：

```json
{ "code": 409, "msg": "version conflict" }
```

- 分页：`limit`（默认 50，最大 200）+ `offset`（默认 0）。
- 幂等：`DELETE` 不存在也返回 204，方便重试。

## 错误码

| code | 场景 |
|---|---|
| 400 | 参数缺失、格式错误、`visibility` 非法枚举、未知 JSON 字段、`base_version` 缺失 |
| 401 | 缺失/错误 Bearer Token（含服务端未配置 token） |
| 404 | 资源不存在（含已删除、非 public 笔记） |
| 409 | `base_version` 与当前 `version` 不一致；重命名标签时目标名已存在 |
| 413 | 请求体超过 8 MiB |
| 429 | 触发限流，带 `Retry-After` |
| 500 | 服务端内部错误，不返回堆栈/SQL |

请求体用 `DisallowUnknownFields` 解析：拼错的字段（例如把 `base_version` 写成
`baseVersion`）直接 400，而不是被静默忽略后变成版本冲突。

## 私有接口

| 接口 | Method | 说明 |
|---|---|---|
| `/api/notes` | POST | 新建，body 含 `title / raw_md / tags / visibility`；笔记与标签同一事务 |
| `/api/notes` | GET | 列表，元数据（不含 `raw_md`），游标分页（见下） |
| `/api/notes/:id` | PUT | 修改，body 含 `title / raw_md / visibility / base_version`；version 不匹配返 409 |
| `/api/notes/:id` | DELETE | 删除，204；幂等 |
| `/api/notes/:id` | GET | 获取完整笔记（含 `raw_md`），供懒加载 |
| `/api/notes/:id/tags` | POST | 绑定标签，body `{"name":"go"}`；标签不存在自动新建（同一事务） |
| `/api/notes/:id/tags/:tag_name` | DELETE | 移除标签；未绑定也返 200（幂等） |
| `/api/tags/:old_name` | PUT | 标签全局重命名，body `{"name":"new"}` |
| `/api/tags` | GET | 列出全部标签 |
| `/api/sync/snapshot` | GET | 全量元数据快照（不含 `raw_md`），供 `knot sync` |
| `/api/search?q=&tag=&limit=&offset=` | GET | pg_trgm 全文检索，返回元数据 |
| `/api/llm/chat` | POST | 二期：RAG 问答，后端调 DeepSeek |
| `/healthz` | GET | 健康检查，只查进程与 PG 连接 |

### 分页：列表用游标，搜索用 offset

列表（`GET /api/notes`、`GET /api/public/notes`）按 `modified_at DESC, id DESC` 排序，用**不透明游标**翻页：

```json
{ "notes": [ ... ], "next_cursor": "MjAyNi0xMC0yNFQxMDowMDowMFo..." }
```

- 客户端把 `next_cursor` 原样放到下一次请求的 `?cursor=`，**不要解析**（以后可能换排序键）；
- 响应没有 `next_cursor`（或为空）表示已经到底；
- 为什么不用 offset：翻页期间有新写入时，offset 会让同一条笔记重复出现或被跳过。
- `/api/search` 保留 `limit/offset`：它要按标签/关键词过滤且 Web 需要页码，offset 的偏差在检索场景可接受。

### 请求/响应示例

`POST /api/notes`：

```json
{
  "title": "笔记标题",
  "raw_md": "# 正文",
  "tags": ["go", "notes"],
  "visibility": "private"
}
```

响应 `201`：

```json
{
  "id": "uuid",
  "title": "笔记标题",
  "version": 1,
  "visibility": "private",
  "created_at": "2026-10-24T10:00:00Z",
  "modified_at": "2026-10-24T10:00:00Z",
  "tags": ["go", "notes"]
}
```

`PUT /api/notes/:id`：

```json
{
  "title": "新标题",
  "raw_md": "# 新正文",
  "visibility": "private",
  "base_version": 1
}
```

成功返回 `200` + 新 `version: 2`；冲突返回 `409`。

`GET /api/sync/snapshot`：

```json
{
  "notes": [
    { "id": "uuid", "title": "...", "visibility": "private",
      "created_at": "...", "modified_at": "...", "tags": ["go"] }
  ],
  "tags": [{ "id": "uuid", "name": "go" }],
  "synced_at": "2026-10-24T10:00:00Z"
}
```

**不含 `raw_md`**。

## 公开只读接口（Vue Web）

| 接口 | Method | 说明 |
|---|---|---|
| `/api/public/notes?tag=&limit=&offset=` | GET | 笔记元列表，**查询层强制 `visibility='public'`** |
| `/api/public/notes/:id` | GET | 笔记完整内容，非 `public` 返 404 |
| `/api/public/tags` | GET | 仅返回至少关联一篇 `public` 笔记的标签 |

公开接口**不返回** `version`、`modified_at`、`visibility` 等内部字段，不返回草稿/私有内容。
`/api/public/notes` 同为游标分页（`?cursor=&limit=`）。

## 限流

Go 中间件按 IP 令牌桶：

| 接口 | 速率 | 突发 |
|---|---|---|
| `/api/public/*` | 10 req/s | 20 |
| `/api/public/notes`、`/api/search` | 2 req/s | 5 |
| 私有接口（`/api/search` 除外） | 不限（有 token 保护） | — |

超限返回 `429` + `Retry-After`（秒）。limiter 带过期清理，避免 IP 表无限增长。

**不读 `X-Forwarded-For`**：服务只监听 `127.0.0.1` 由 Caddy 反代，客户端可以伪造该头绕过限流。
代价是经反代后所有请求共享同一个桶，即公开接口实际是**全站**限流（而非按真实客户端 IP 分桶）；
对「单实例个人站 + 限流防刷」这个目标这是更安全的一侧。

## CLI-API 映射

| CLI 动作 | 接口 | Method |
|---|---|---|
| new | `/api/notes` | POST |
| edit | `/api/notes/:id` | PUT |
| rm | `/api/notes/:id` | DELETE |
| show / edit 懒加载 | `/api/notes/:id` | GET |
| tag add / rm | `/api/notes/:id/tags` 等 | POST / DELETE |
| tag rename | `/api/tags/:old_name` | PUT |
| sync | `/api/sync/snapshot` | GET |
| search | `/api/search` | GET |
