# AGENTS.md

## 先读

- **`docs/` 是本项目的设计与契约来源**，写代码前按任务读对应文件：`ARCHITECTURE.md`（模块划分与依赖方向）、`DATA_MODEL.md`（PG/SQLite 表结构）、`API.md`（接口、错误码、限流、CLI 映射）、`CLIENT.md`（CLI/TUI 命令与配置）、`SYNC_CACHE.md`（对账与缓存）、`WEB.md`（前端规格，最详细）、`SECURITY.md`、`DEPLOYMENT.md`、`ROADMAP.md`（MVP/二期边界）、`DEVELOPMENT.md`（依赖与测试约定）。
- 文档与代码/配置冲突时以可执行配置为准，并顺手修正文档；反之改动设计也要更新对应文档。

## 仓库现状

- 骨架阶段：Go 代码除 `web/` 外全部是 0 字节占位文件（`cmd/*`、`internal/*`），现在 `go build/vet/test ./...` 会报 `expected 'package', found 'EOF'`；先写实现再验证。
- 根模块名是裸路径 `knot`，导入写成 `knot/internal/...`。
- 在没有有效 `.go` 源码时 `go mod tidy` 会删掉全部 `require`（`make setup` 末步就会执行 tidy），这就是当前 `go.mod` 无依赖、`go.sum` 为空的原因。源码可编译前不要用 tidy 管理依赖；需要拉依赖时直接 `go get`。
- 没有 CI/pre-commit；`make check`（vet → lint → test）是唯一检查入口。也没有 `sqlc.yaml`，`make sqlc` 目前不可用。
- `web/` 仍是 Vue 默认模板（JS），与 `WEB.md` 的规格（TypeScript、`src/api`、`src/composables`、markdown-it + DOMPurify、Vite proxy）尚未对齐——改前端前先读 `WEB.md`，按它落地而不是沿用模板。

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

- 包管理器用 npm（Makefile 与 `scripts/web-build.sh` 都调 npm；`web/README.md` 的 pnpm 是模板残留）。
- 首次 `cd web && npm install`；`npm run dev` / `npm run build` / `npm run test:unit` / `npm run lint` / `npm run format`。
- Vitest 默认 watch，一次性运行用 `npm run test:unit -- --run`。
- `npm run lint` 带 `--fix`（oxlint + eslint），会直接改文件；`npm run format` 只格式化 `src/`。
- 格式：Prettier 无分号、单引号、行宽 100；Node 版本要求 `^22.18.0 || >=24.12.0`。
- 目前无 lockfile；`scripts/web-build.sh` 只在有 `package-lock.json` 时用 `npm ci`，CI 环境缺 lockfile 会直接失败——首次 `npm install` 后应提交 lockfile。
- Vite 尚未配置 `/api` proxy（`WEB.md` 要求代理到 `127.0.0.1:8080`）。
