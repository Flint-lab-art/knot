# 开发

## 依赖

### Go 运行时

| 用途 | 库 |
|---|---|
| UUID | `github.com/google/uuid` |
| 配置 | `github.com/knadh/koanf/v2` |
| TUI | `github.com/charmbracelet/bubbletea`、`bubbles`、`lipgloss` |
| CLI | `github.com/spf13/cobra` |
| Markdown 渲染 | `github.com/charmbracelet/glamour` |
| 模糊选择 | `github.com/ktr0731/go-fuzzyfinder` |
| 路由 | `github.com/go-chi/chi/v5` |
| PG | `github.com/jackc/pgx/v5` |
| 迁移 | `github.com/pressly/goose/v3` |
| SQLite | `modernc.org/sqlite`（纯 Go，无 CGO） |
| 向量（二期） | `github.com/pgvector/pgvector-go` |

### 构建工具（`go install`，不写入 go.mod）

- `github.com/sqlc-dev/sqlc/cmd/sqlc`
- `github.com/pressly/goose/v3/cmd/goose`
- `github.com/golangci/golangci-lint/v2/cmd/golangci-lint`（v1 的 `v1.64.8` 是最后支持旧版，无法解析 Go 1.27 的导出数据，必须用 v2）

### 外部

- Go >= 1.23
- Node.js `^22.18.0 || >=24.12.0`（Vue 构建，见 `web/package.json` 的 `engines`）
- PostgreSQL 16（含 `pg_trgm`，二期加 `pgvector`）
- Caddy（生产部署）

## 脚本

| 脚本 | 作用 |
|---|---|
| `scripts/dev-setup.sh` | 检查 Go 版本、`go mod init`、拉运行时依赖、装构建工具、`go mod tidy` |
| `scripts/web-build.sh` | 校验 node/pnpm、`pnpm install --frozen-lockfile`（无 lock 时 fallback `pnpm install`，CI 下禁止 fallback）、`pnpm run build`、校验 `dist/index.html` |

## Makefile 目标

| 目标 | 作用 |
|---|---|
| `setup` | 调 `dev-setup.sh`，末尾执行 `make hooks` 启用 git hooks |
| `hooks` | `core.hooksPath=.githooks`，启用 pre-commit（`make check`） |
| `tools` | 只装 sqlc/goose/golangci-lint |
| `tidy` | `go mod tidy` |
| `sqlc` | `sqlc generate` |
| `run-server` | `go run ./cmd/knot-server` |
| `run` | `go run ./cmd/knot tui` |
| `web-dev` | `cd web && pnpm run dev` |
| `test` | `go test -race -count=1 ./...` |
| `test-short` | `go test -short -count=1 ./...` |
| `cover` | 覆盖率报告 |
| `vet` | `go vet ./...` |
| `lint` | `golangci-lint run ./...` |
| `check` | `vet + lint + test` |
| `build-cli` | 本机平台编 CLI |
| `build-server` | 交叉编译 Linux（`GOARCH` 可覆盖） |
| `web-build` | 调 `web-build.sh` |
| `install` | `go install ./cmd/knot` |
| `dist` | `web-build → build-server → tar`，产物 `bin/knot-deploy.tar.gz` |
| `clean` | 清 `bin/` + 测试结果 |
| `clean-web` | 清 `web/dist` |
| `clean-deps` | 删 `go.mod/go.sum` + 清模块与构建缓存 |

## 交叉编译

- CLI 只在本机使用，`build-cli` 不设 `GOOS/GOARCH`。
- Server 交叉编译：`make build-server GOARCH=arm64`。
- 全部依赖纯 Go，`CGO_ENABLED=0` 可静态链接，无 glibc 依赖。

## 测试约定

| 包 | 测试内容 |
|---|---|
| `internal/reconcile` | 快照 diff 纯函数，最高价值 |
| `internal/store/sqlite` | `:memory:` SQLite，事务、级联删除 |
| `internal/store/pg` | 本地 PG 或 testcontainers，事务、pg_trgm |
| `internal/server/handler` | `httptest` + mock store，路由、鉴权 |
| `web/` | Vitest：API 封装、筛选逻辑、**Markdown sanitize** |

## 提交前

```bash
make check
```

`make hooks` 会把 `core.hooksPath` 指到 `.githooks/`，pre-commit 自动执行 `make check`；涉及 `web/src` 改动时还会跑一次 Vitest。`.git/hooks` 不入库，新克隆后必须重跑 `make hooks`（`make setup` 已含）。

## Git 忽略

```gitignore
bin/
web/node_modules/
web/dist/
coverage.out
coverage.html
*.test
```
