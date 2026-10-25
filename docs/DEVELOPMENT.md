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
| `scripts/db-setup.sh` | 本地 PG 建库 + `CREATE EXTENSION pg_trgm` + `knot-server migrate`，幂等 |
| `scripts/sqlc-schema.sh` | 把 `migrations/pg/*.sql` 的 Up 段落拼成 sqlc 用的 `internal/store/pg/sqlcgen/schema.sql`；`--check` 只校验是否最新 |
| `scripts/web-build.sh` | 校验 node/pnpm、`pnpm install --frozen-lockfile`（无 lock 时 fallback `pnpm install`，CI 下禁止 fallback）、`pnpm run build`、校验 `dist/index.html` |
| `scripts/deploy.sh` | 服务器上安装/升级：校验包 → 备份 → 装文件 → 迁移 → 重启 → healthz 冒烟，失败自动回滚二进制 |
| `scripts/backup.sh` | `pg_dump -Fc` + `pg_restore -l` 校验 + 保留 N 份；供 cron 调用 |

### sqlc 与迁移的 schema 同步

sqlc 的 schema 解析器处理不了 goose 的 `-- +goose Up/Down` 混排，因此 `sqlc.yaml` 指向
`internal/store/pg/sqlcgen/schema.sql` —— 一份由 `scripts/sqlc-schema.sh` 从迁移自动拼接的
纯 DDL 副本。**该文件禁止手改**：

```sh
make sqlc-schema        # 改完 migrations/pg/*.sql 后重新生成
make sqlc               # 先同步 schema，再 sqlc generate
make sqlc-check-schema  # 只校验副本是否过期（pre-commit 在迁移变更时自动跑）
```

改迁移却忘了重跑会让 store 代码与真实表结构漂移，所以 pre-commit 在检测到
`migrations/pg/` 有暂存改动时会拒绝提交过期副本。

## Makefile 目标

| 目标 | 作用 |
|---|---|
| `setup` | 调 `dev-setup.sh`，末尾执行 `make hooks` 启用 git hooks |
| `hooks` | `core.hooksPath=.githooks`，启用 pre-commit（`make check`） |
| `db-setup` | 本地 PG 建库 + 装 `pg_trgm` + 跑迁移（调 `scripts/db-setup.sh`） |
| `tools` | 只装 sqlc/goose/golangci-lint |
| `tidy` | `go mod tidy` |
| `sqlc` | 先同步 schema 副本，再 `sqlc generate` |
| `sqlc-schema` | 由迁移生成 `sqlcgen/schema.sql` |
| `sqlc-check-schema` | 校验 schema 副本与迁移一致（pre-commit 用） |
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
| `backup` | 调 `scripts/backup.sh`（需 `PG_DSN`） |
| `deploy` | 调 `scripts/deploy.sh "$(DEPLOY_PKG)"`（需 root） |

## 交叉编译

- CLI 只在本机使用，`build-cli` 不设 `GOOS/GOARCH`。
- Server 交叉编译：`make build-server GOARCH=arm64`。
- 全部依赖纯 Go，`CGO_ENABLED=0` 可静态链接，无 glibc 依赖。

## 测试约定

| 包 | 测试内容 |
|---|---|
| `internal/reconcile` | 快照 diff 纯函数，最高价值 |
| `internal/store/sqlite` | `:memory:` SQLite，迁移建表、外键级联、事务回滚（不需要外部服务，随 `make test` 跑） |
| `internal/store/pg` | 本地 PG 或 testcontainers，事务、pg_trgm（需外部服务，`testing.Short()` 跳过） |
| `internal/server/handler` | `httptest` + mock store，路由、鉴权 |
| `web/` | Vitest：API 封装、筛选逻辑、**Markdown sanitize** |

### PG 集成测试

`internal/store/pg/store_integration_test.go` 带 `integration` build tag 且需要 `KNOT_TEST_PG_DSN`，
两者缺一就跳过，所以 `make test` / `make check` 不依赖 PG：

```sh
KNOT_TEST_PG_DSN='postgres://postgres@/postgres?host=/run/postgresql&sslmode=disable' \
  go test -tags=integration -count=1 ./internal/store/pg/
```

- DSN 必须是能 `CREATE SCHEMA` 的管理库；每个用例建独立的 `knot_test_<random>` schema，
  结束时 `DROP SCHEMA ... CASCADE`，不会碰业务库。
- 用例会在临时 schema 里装 `pg_trgm`（0002 依赖）；用无权限角色跑时请先由管理员把扩展装到库里。
- 覆盖：全部迁移 up → down → up、CHECK/唯一约束、两级级联删除、`base_version` 409、
  公开接口可见性过滤、游标翻页、标签重命名/解绑、快照不含正文。

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
