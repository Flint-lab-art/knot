# AGENTS.md

## 任务流程（每次开工先做）

1. **开始任务前先读 `TODO.md`**，确认当前阶段与要做的条目，不要跳过阶段顺序。
2. **每完成一项任务，立刻把 `TODO.md` 对应复选框改成 `[x]`**；同一提交内完成代码 + TODO 勾选，不要事后补勾。
3. 任务发现与文档冲突、或产生新事项时，一并更新 `TODO.md`（新增/调整条目）而不是只记在脑子里。
4. `TODO.md` 尚未被 git 跟踪；开始使用本流程时先 `git add TODO.md` 纳入版本管理。

## 先读

- **`docs/` 是本项目的设计与契约来源**，写代码前按任务读对应文件：`ARCHITECTURE.md`（模块划分与依赖方向）、`DATA_MODEL.md`（PG/SQLite 表结构）、`API.md`（接口、错误码、限流、CLI 映射）、`CLIENT.md`（CLI/TUI 命令与配置）、`SYNC_CACHE.md`（对账与缓存）、`WEB.md`（前端规格，最详细）、`SECURITY.md`、`DEPLOYMENT.md`、`ROADMAP.md`（MVP/二期边界）、`DEVELOPMENT.md`（依赖与测试约定）。
- 文档与代码/配置冲突时以可执行配置为准，并顺手修正文档；反之改动设计也要更新对应文档。

## 仓库现状

- 骨架阶段：Go 代码除 `web/` 外曾全部是 0 字节占位文件，**阶段 1 已完成**：`go build ./...` 首次通过，`make check` 与 pre-commit 可用，此后禁止再用 `--no-verify` 跳过 hook。
- 根模块名是裸路径 `knot`，导入写成 `knot/internal/...`。
- `go.mod` 只保留实际引用的依赖（当前 `cobra`、`bubbletea`、`uuid`），其余在对应阶段 `go get`；不要在源码不引用时提前拉依赖，`go mod tidy` 会把它们删掉。
- 没有 CI；`make check`（vet → lint → test）与 `.githooks/pre-commit` 是唯一检查入口。也没有 `sqlc.yaml`，`make sqlc` 目前不可用。
- `web/` 已是 Vue 3 + **TypeScript** 工程（`vite.config.ts`、`vitest.config.ts`、`tsconfig.*.json`），但页面仍是默认模板；与 `WEB.md` 的规格（`src/api`、`src/composables`、markdown-it + DOMPurify）尚未对齐——改前端前先读 `WEB.md`，按它落地而不是沿用模板。
- **前端语言是 TypeScript**（Vue 3 + Vite + `vue-tsc`）。`pnpm build` 会先跑 `type-check` 再构建；类型定义放 `src/types/`，与 `docs/API.md` 的 DTO 字段对齐。

## 架构约束（来自 docs/ARCHITECTURE.md）

- 依赖方向：`model ← dto ← client / store / reconcile ← cli / tui / server ← cmd`。
- `model`、`dto` 只依赖标准库和 `google/uuid`；`server` 只依赖 `store/pg`；`cli`/`tui` 只依赖 `store/sqlite` 和 `client`；`reconcile` 纯函数。
- 无 service 层：业务逻辑写在 `store` 方法里，handler 直接调用。
- 对账包是 `internal/reconcile`（纯函数）。**不要把它命名为 `sync`**：包名 `sync` 会遮蔽标准库 `sync`，包内无法直接用 `sync.Mutex`，调用方也得起别名。
- sqlc 生成代码放 `internal/store/pg/sqlcgen/`，不手改；`store.go` 包一层，handler 只认 store 接口。
- 数据真值在服务端 PostgreSQL，客户端 SQLite 是可重建缓存；同步是单向拉取，本地不回写。不要引入离线写入/双向冲突逻辑。
- 所有写操作走 HTTP；`visibility` 在服务端查询层强制过滤，标签承载可见性是明确禁止的设计。
- SQLite 用 `modernc.org/sqlite`（纯 Go），构建统一 `CGO_ENABLED=0`；本地库必须 `PRAGMA foreign_keys = ON`。
- 迁移用 goose 并 `//go:embed`：PG 在 `migrations/pg`（`knot-server migrate` 执行），SQLite 在 `migrations/sqlite`（CLI 启动时执行）；迁移只加不删。

## Git 规范（强制）

- **禁止任何写入 `main` 的操作**：不要 `checkout/switch main`、不要在 `main` 上 `commit`、不要 `merge/rebase/reset` 到 `main`、不要 `push origin main`、不要 `push --force` 覆盖 `main`。查看用 `git log main`、`git diff main` 这类只读命令没问题。
- 工作分支是 `dev`（跟踪 `origin/dev`）。单人开发，**直接在 `dev` 上提交，不建 `feat/*` 功能分支**；`debv` 是本地遗留分支，不要在其上开发。
- 开新工作前 `git status` 确认干净并 `git pull --rebase origin dev`；保持线性历史，不制造无意义 merge commit。高风险重构或需要“可整体丢弃”的实验可以临时开本地分支，合并后删掉。
- 提交前必须通过 `make check`（vet → lint → test）+ 前端 `pnpm test:unit -- --run`；`make check` 已由 `.githooks/pre-commit` 自动执行，提交前端改动时 hook 还会额外跑 Vitest。
- **没有 CI**：本地测试与 hook 是唯一防线，不要依赖远端兜底。本项目不打算引入 GitHub Actions。
- hook 装在 `.githooks/` 并由 `core.hooksPath` 指向（`make hooks` 启用，`make setup` 末尾会自动调用）；`.git/hooks` 不入库，换机器必须重跑 `make hooks`。
- 阶段 1 之前唯一允许的 `git commit --no-verify` 例外已随阶段 1 完成失效（那次提交标记为 `b43611c`）；现在 hook 必须真实通过，`--no-verify` 视为违规。
- `.gitignore` 已覆盖 `bin/`、`web/dist/`、`config.toml`、`.env`、`*.db`、`*.dump` —— **`config.toml` 与 `.env` 含 token，绝不要 `git add -f`**。
- 提交信息用 Conventional Commits 前缀：`feat:` / `fix:` / `docs:` / `refactor:` / `test:` / `chore:` / `build:` / `ci:`。标题用祈使句、≤ 72 字，正文说明「为什么」而不是复述 diff。
- 一个提交只做一件事：不要混入格式化噪声、无关重构、依赖升级。需格式化/生成代码（`pnpm format`、`sqlc generate`）时单独成提交。
- `sqlc` 生成代码（`internal/store/pg/sqlcgen/`）随生成它的 SQL 改动一起提交，不要手改生成文件。
- 不要改写已推送的历史（`--amend`、`rebase -i`、`reset --hard` 仅限**未推送**的本地提交）；`git push --force` 只能对非 `main` 分支使用，优先 `--force-with-lease`。
- 合并回 `main` 由维护者操作，agent 只负责在 `dev` 上提交并推送 `origin dev`。

## 测试要求

不要求“每个功能都写单测”式的覆盖，按出错代价决定：

| 优先级 | 必须测 | 位置 |
|---|---|---|
| 高 | 快照 diff 纯函数（删除 / 重命名 / 解绑 / 新增） | `internal/reconcile/*_test.go`，表驱动 |
| 高 | SQLite 对账事务、级联删除、回滚 | `internal/store/sqlite`，用 `:memory:` |
| 高 | Markdown sanitize（`<script>`、`onerror`、`javascript:`、GFM 不误伤） | `web/` Vitest |
| 中 | `base_version` 冲突 409、公开接口 `visibility` 过滤、限流 429 | handler 测试（`httptest` + mock store） |
| 低 | CLI 参数解析、TUI 渲染、胶水代码 | 不强制，靠手测 |

- 新增/修改纯函数或数据写入逻辑必须带测试；UI 与胶水代码以手测为主。
- 不要为凑覆盖率写只断言“调用过 mock”的测试。
- 依赖外部服务（PG、testcontainers）的测试用 `testing.Short()` 跳过，保证 `make test-short` 可跑。

## 常用命令

```sh
make setup        # 首次：检查 Go、拉依赖、装 sqlc/goose/golangci-lint（默认 goproxy.cn）
make run-server   # go run ./cmd/knot-server（约定监听 127.0.0.1:8080）
make run          # go run ./cmd/knot tui
make test         # go test -race -count=1 ./...
make test-short   # go test -short -count=1 ./...（跳过集成测试）
go test -race -run TestName ./internal/store/sqlite   # 单测（Makefile 无单测目标）
make check        # vet -> lint -> test；lint 需先 make tools
make build        # 本机 CLI + Server 交叉编译 linux/amd64
make build-server GOARCH=arm64
make dist         # web-build + build-server -> bin/knot-deploy.tar.gz
```

- 海外环境：`GOPROXY=https://proxy.golang.org,direct make setup`。
- 有集成/外部依赖（PG、testcontainers）的测试必须用 `testing.Short()` 跳过，保证 `make test-short` 能跑。

## 前端（web/）

- **包管理器是 pnpm，禁止使用 npm / yarn**：不要生成 `package-lock.json`，不要运行 `npm install`。没有 pnpm 时先 `corepack enable`。
- 首次 `cd web && pnpm install`；`pnpm dev` / `pnpm build` / `pnpm test:unit` / `pnpm lint` / `pnpm format`，单独类型检查用 `pnpm type-check`。
- `pnpm build` = `vue-tsc --build` + `vite build`，类型错误会阻断构建；改了 `.vue`/`.ts` 后至少跑一次 `pnpm type-check`。
- **`@vitest/eslint-plugin` 必须锁死 `1.6.27`**：`1.6.28` 要求 TypeScript >=6，而 `vue-tsc`/`@vue/tsconfig` 目前停在 TS 5.9，升级会让依赖安装报 ERESOLVE。
- 配置文件是 `.ts`：`vite.config.ts` / `vitest.config.ts`；`vitest.config.ts` 里导入用 `./vite.config.js`（带扩展名，否则 Vite 原生 config loader 会告警）。
- `/api` 已在 `vite.config.ts` 里代理到 `http://127.0.0.1:8080`。
- Vitest 默认 watch，一次性运行用 `pnpm test:unit -- --run`。
- `pnpm lint` 带 `--fix`（oxlint + eslint），会直接改文件；`pnpm format` 只格式化 `src/`。
- 格式：Prettier 无分号、单引号、行宽 100；Node 版本要求 `^22.18.0 || >=24.12.0`。
- `web/pnpm-lock.yaml` 必须入库；`scripts/web-build.sh` 存在 lockfile 时用 `pnpm install --frozen-lockfile`。
