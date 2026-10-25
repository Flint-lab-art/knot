# Knot 开发 TODO

## 阶段 0：文档与配置对齐

- [x] `mv internal/sync internal/reconcile`，包名 `reconcile`
- [x] 写 `AGENTS.md`（任务流程、分支策略、提交格式、测试要求、pre-commit）
- [x] 写 `.gitignore`
- [x] 建 `LICENSE`（MIT）
- [x] 加 `.githooks/pre-commit`（执行 `make check`）+ `make hooks` 目标
- [x] 前端语言定为 **TypeScript**，`web/` 迁移到 TS（`tsconfig.*.json`、`vite.config.ts`、`vitest.config.ts`、`main.ts`、`vue-tsc`）
- [x] `web/vite.config.ts` 加 `server.proxy`：`/api` → `127.0.0.1:8080`
- [x] 修文档与脚本不一致：Node `^22.18.0 || >=24.12.0`（`README.md` / `DEVELOPMENT.md` / `scripts/web-build.sh`）、`make test` 补 `-count=1`
- [x] 包管理器统一为 **pnpm**（`web-build.sh`、`make web-dev`、hook、文档全部改 pnpm，删除 `package-lock.json`）
- [x] 生成并提交 `web/pnpm-lock.yaml`
- [x] 建 `config.example.toml`（含客户端 `server_url/token/[editor]/[cache]` 与服务端 `listen/pg_dsn`，已过 TOML 校验）
- [x] 提交阶段 0（Go 骨架未补完，需 `git commit --no-verify`，仅此一次）
- [x] `make tools` 与 `scripts/dev-setup.sh` 改用 `golangci-lint/v2`（v1 无法解析 Go 1.27 导出数据）

## 阶段 1：可编译最小骨架

> 目标：`go build ./...` 首次通过，`make check` 与 pre-commit 真正可用。

- [x] `internal/model/`、`internal/dto/`、`internal/config/`、`internal/cli/cmd/`、`internal/server/handler|middleware/`、`internal/store/pg/queries|sqlcgen/`、`internal/tui/views/` 各补带 `package` 声明的占位文件
- [x] `cmd/knot/main.go`：Cobra 根命令，支持 `--version`
- [x] `cmd/knot-server/main.go`：最小 HTTP 服务 + `GET /healthz`
- [x] `go build ./...`、`go vet ./...` 通过
- [x] `make check` 首次全绿（首次不需要 `--no-verify`）

## 阶段 2：数据层

- [x] `migrations/pg/0001_init.sql`：`tags` / `notes`（含 `version`、`visibility`）/ `note_tags` + 索引
- [x] `migrations/sqlite/0001_init.sql`：`note_meta` / `tag_meta` / `note_tag_meta` / `note_content_cache` / `sync_state`
- [x] `migrations/pg/0002_trgm_index.sql`：`title` / `raw_md` 的 gin trigram 索引（依赖 `pg_trgm`，扩展由 `make db-setup` / 部署步骤安装）
- [x] `migrations/migrations.go`：统一 `//go:embed` 两套迁移（`..` 路径在 embed 里非法）
- [x] `sqlc.yaml` 配置，`internal/store/pg/queries/notes.sql`、`tags.sql`、`sync.sql`
- [x] `make sqlc` 生成代码到 `internal/store/pg/sqlcgen/`（schema 由 `scripts/sqlc-schema.sh` 从迁移生成，pre-commit 校验）
- [x] `internal/store/pg/store.go`：CRUD 方法（`Open` / `Migrate` / `Pool` / `Ping` 已完成；列表改游标分页，不引入 `sort_ord`）
- [ ] `internal/store/sqlite/store.go`：对账事务方法（`Open` / `Migrate` / `Close` 已完成）
- [x] `cmd/knot-server` 加 `migrate` 子命令，迁移 `//go:embed`
- [x] `internal/config.LoadServer`：TOML + `KNOT_PG_DSN` / `KNOT_LISTEN` 覆盖（客户端 `LoadClient` 属阶段 5）
- [x] SQLite `:memory:` 建表、级联删除、回滚测试
- [x] 本地 PG 跑 `knot-server migrate` 成功（`make db-setup`；顺带修了 `docs/DEPLOYMENT.md` 的 `migrate -config` 写法）
- [x] `internal/config` 单测：TOML 解析、环境变量覆盖优先级
- [x] `internal/store/pg` 集成测试：`testing.Short()` 跳过，验证全部迁移 up/down + 触发器/约束
  - 对账事务方法归阶段 4（依赖 `reconcile` 的 diff 结果），阶段 2 只留 `Open` / `Migrate` / `Close`


## 阶段 3：服务端 API

- [x] `internal/dto/*.go`：请求/响应结构体（对照 `docs/API.md`；补分页、公开接口裁剪字段、标签重命名）
- [x] `internal/server/router.go`：chi 路由，私有 / 公开 / `healthz`
- [x] `middleware/auth.go`：Bearer Token，`crypto/subtle`
- [x] `middleware/ratelimit.go`：按 IP 令牌桶，公开 10/s，搜索 2/s
- [x] `handler/notes.go`：POST / PUT（`base_version`，409）/ DELETE（204）/ GET + 列表 / 搜索
- [x] `handler/tags.go`：列表、绑定、解绑、重命名
- [x] `handler/sync.go`：`GET /api/sync/snapshot`，不含 `raw_md`
- [x] `handler/public.go`：查询层强制 `visibility='public'`
- [x] `handler/health.go`（在 `internal/server/server.go`，healthz 只需进程与 PG 连接）
- [x] 测试：409 版本冲突、公开接口过滤、429 限流（`httptest` + mock store）
- [x] `curl` 手测：新建 → 改 → 查 → 删 → sync → 公开过滤 → 409 → 429

## 阶段 4：客户端 HTTP + 同步对账

- [x] `internal/client/client.go`：fetch 封装、Bearer、错误码映射、429
- [x] `internal/client/notes.go` / `tags.go` / `sync.go`（按接口分组写在 `client.go`，公开接口也一并覆盖）
- [x] `internal/reconcile/reconcile.go`：全量快照 diff 纯函数
- [x] `internal/reconcile/reconcile_test.go`：表驱动，覆盖删除 / 重命名 / 解绑 / 新增
- [x] `internal/store/sqlite` 对账事务实现（`ApplySnapshot` + 本地读写 `notes.go`）
- [x] `go test ./internal/reconcile/` 全绿
- [x] 实库端到端验证：client 建/改/删 + 标签重命名 → sync 后本地与快照逐条一致

## 阶段 5：CLI

- [x] `internal/config/config.go`：TOML + 环境变量覆盖，`editor.command` 做 `LookPath`
- [x] `internal/cli/root.go`：Cobra 根命令
- [x] `new.go` / `edit.go` / `show.go` / `rm.go` / `list.go`（`notes_cmd.go`，共用 `--tags` / `--visibility` 等参数）
- [x] `search.go` / `tag.go` / `sync.go` / `cache.go` / `completion.go`（`tags_cmd.go` / `sync_cmd.go`；`completion` 由 cobra 自带）
- [x] `cmd/knot/main.go` 装配
- [x] `edit` 流程：懒加载 → 临时文件 → 编辑器 → 归一化 hash → PUT
- [x] `rm` 流程：DELETE → 本地事务删三张表
- [x] 全链路手测：`new → list → show → edit → rm → sync`

## 阶段 6：TUI

- [x] `internal/tui/app.go`：Bubble Tea 根 Model，双栏布局
- [x] `views/list.go` / `detail.go` / `editor.go` / `tagpanel.go`（合并为 `views/views.go`：List / Detail / StatusBar / TagPanel；`editor.go` 不适用——编辑交给外部编辑器）
- [x] `statusbar.go`：同步状态、当前标签、快捷键（`views.StatusBar`）
- [x] 快捷键：`/` 搜索、`t` 标签、`s` 同步、`q` 退出（另加 `j/k/g/G` 导航、`Esc` 清筛选）
- [x] 窄终端（< 100 列）自动单栏
- [x] 手测：浏览、搜索、编辑保存、同步
  - 编辑走外部编辑器，TUI 里不内嵌编辑；`e` 入口留到二期（需要暂存终端再起编辑器）

## 阶段 7：Vue Web（TypeScript）

- [x] `vite.config.ts` 确认 proxy（已完成，复核即可）
- [x] `src/types/`：与 `docs/API.md` 对齐的 DTO 类型（`types/api.ts`，只含公开接口字段）
- [x] `src/api/client.ts` / `notes.ts` / `tags.ts`
- [x] `src/views/NoteList.vue` / `NoteDetail.vue` / `NotFound.vue`
- [x] `src/components/NoteCard.vue` / `TagBar.vue` / `MarkdownView.vue` / `Pagination.vue`
- [x] 装 `markdown-it` + `@types/markdown-it` + `dompurify` + `@fontsource-variable/inter` + `noto-sans-cjk-sc`
- [x] CSS 变量：颜色、字号、间距、圆角、卡片参数（按 `WEB.md`）
- [x] 三态：加载 / 空 / 错误；429 提示
- [x] Vitest：Markdown sanitize 四条用例（`<script>`、`onerror`、`javascript:`、GFM 不误伤）
- [x] `make web-dev` 手测列表、阅读、标签筛选、429（Vite 代理 + 公开接口过滤已验；渲染态由 48 条 Vitest 覆盖）
- [x] `make web-build` 产出 dist

## 阶段 8：Neovim 插件

- [x] `nvim/` 目录：lua 插件，调 `knot` CLI（`lua/knot/init.lua`）
- [x] 编辑入口（`:KnotEdit`，异步执行，不阻塞 Neovim）
- [x] 搜索弹窗（`:KnotSearch`，服务端检索）
- [x] 标签面板（`:KnotTags`）
- [x] 关联笔记面板（`:KnotRelated`，按共享标签在客户端计算）
- [x] 手测（`nvim -l nvim/tests/run.lua` 自检 + 真实 CLI 集成验证）

## 阶段 9：部署联调

- [ ] 本机 docker PG + Caddy 走一遍完整部署流程
- [ ] 验证迁移、`/api` 反代、SPA fallback、`/healthz`
- [ ] 上服务器：systemd + Caddy
- [ ] 公网 HTTPS 验证
- [ ] 公开笔记可见，私密笔记 404
- [ ] 限流生效（429）
- [ ] `pg_dump` 定时备份
- [ ] `knot-server.prev` 回滚演练
- [ ] 提交部署脚本 / 文档补充

## 二期（不在当前 TODO）

- [ ] `change_log + seq` 增量同步
- [ ] 正文缓存 LRU
- [ ] pg_trgm 检索调优 + TUI fzf 外部二进制
- [ ] `unlisted` 可见性
- [ ] RAG / LLM：切片、embedding、`/api/llm/chat`、DeepSeek、TUI LLM tab
- [ ] token 轮换 / 多客户端签发
- [ ] Web UI 美化、备份异地推送
