# 路线图

## MVP

**服务端**

- PG 表初始化（`tags / notes / note_tags`），`version` 乐观锁，`visibility` 字段
- 基础 CRUD 接口，笔记与标签同一事务
- `PUT` 带 `base_version`，冲突 409
- `GET /api/sync/snapshot` 全量元数据
- 公开只读接口，查询层强制 `visibility='public'`
- 只读 PG 角色
- Go 中间件按 IP 限流
- `/healthz`
- 迁移通过 `knot-server migrate` 执行
- `pg_dump` 备份脚本

**客户端**

- CLI：`new / edit / show / rm / list / search / tag / sync / cache / completion`
- TUI：双栏布局、列表、搜索、编辑、标签面板、同步状态栏
- SQLite 两级缓存与正文懒加载
- 全量快照对账（删除、重命名、解绑）
- 配置文件 `~/.config/knot/config.toml`
- 归一化 hash 比对提交

**Neovim 插件**

- 轻量胶水，调用 `knot` CLI
- 编辑入口、搜索弹窗、标签面板、关联笔记面板

**Web**

- Vue 最简展示：列表、标签筛选、阅读页
- Markdown sanitize + CSP
- 只读，无写入接口

**部署**

- Caddy + systemd，Go 监听 `127.0.0.1`
- `make dist` 产出部署包
- SSH 基线（密钥、高位端口、安全组、Fail2ban）

## 二期 A：全文检索优化

- pg_trgm 检索调优，分页与排序
- 本地缓存同步检索索引
- TUI fzf 外部二进制优先（检测到则用，否则 fallback `go-fuzzyfinder`）
- `unlisted` 可见性枚举实现（分享链接可读但不在公开列表）

## 二期 B：RAG / LLM

- 笔记修改后后台切片，生成 embedding 入 pgvector
- `rag_chunks` 更新时删旧 chunk
- 后端 RAG 问答接口 `POST /api/llm/chat`
- 调用 DeepSeek，**API Key 只存服务端**
- TUI 右栏增加 `LLM 会话` tab，会话存 `llm_sessions`
- `change_log + seq` 增量同步（有多写客户端时）

## 优化项

- 正文缓存 LRU 淘汰
- 备份异地推送
- Web UI 美化与卡片流
- 缓存兜底一键清空重拉
- token 轮换与多客户端签发

## 已从优化项提到 MVP

- `version` 乐观锁（防并发覆盖）
- `visibility` 查询层隔离（公开/私密安全底线）
- `/healthz`
- 只读 PG 角色
- Go 中间件限流
- 部署安全基线

## 未决事项

- token 轮换策略与多客户端签发
- 私密笔记在公开实例的存储策略
- 标签是否强制小写/短横线规范，服务端校验还是客户端约束
- `pg_dump` 备份频率、保留份数、异地推送
- `unlisted` 是否二期实现，或永远不做
