# Makefile —— Knot
# 用法: make help

# ---------- 变量 ----------
MODULE      ?= knot
BIN_DIR     ?= bin
GO          ?= go
GOARCH      ?= amd64
GOBIN       := $(shell $(GO) env GOPATH)/bin
GOPROXY     ?= https://goproxy.cn,direct
GOSUMDB     ?= sum.golang.google.cn

CLI_BIN     := $(BIN_DIR)/knot
SERVER_BIN  := $(BIN_DIR)/knot-server-linux-$(GOARCH)

LDFLAGS     := -s -w

C_RESET := \033[0m
C_GREEN := \033[32m
C_BLUE  := \033[34m

.DEFAULT_GOAL := help

# ---------- 帮助 ----------
.PHONY: help
help: ## 显示所有目标
	@printf "$(C_BLUE)Knot Makefile$(C_RESET)\n\n"
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  $(C_GREEN)%-20s$(C_RESET) %s\n", $$1, $$2}'

# ---------- 初始化 ----------
.PHONY: setup
setup: ## 安装 Go 依赖与构建工具（调 scripts/dev-setup.sh）
	@if [ ! -f scripts/dev-setup.sh ]; then echo "✗ 缺少 scripts/dev-setup.sh"; exit 1; fi
	@MODULE=$(MODULE) GOPROXY=$(GOPROXY) GOSUMDB=$(GOSUMDB) bash scripts/dev-setup.sh
	@$(MAKE) hooks

.PHONY: tools
tools: ## 仅安装构建工具（sqlc/goose/golangci-lint）
	@GOPROXY=$(GOPROXY) $(GO) install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
	@GOPROXY=$(GOPROXY) $(GO) install github.com/pressly/goose/v3/cmd/goose@latest
	@GOPROXY=$(GOPROXY) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest

.PHONY: tidy
tidy: ## go mod tidy
	$(GO) mod tidy

.PHONY: hooks
hooks: ## 启用仓库内 git hooks（core.hooksPath=.githooks）
	git config core.hooksPath .githooks
	@chmod +x .githooks/* 2>/dev/null || true
	@echo "✓ core.hooksPath=$$(git config core.hooksPath)（pre-commit 会执行 make check）"

.PHONY: backup
backup: ## 备份 PG 到 BACKUP_DIR（需 PG_DSN 环境变量）
	@bash scripts/backup.sh

.PHONY: db-setup
db-setup: ## 本地 PG 建库 + 装 pg_trgm + 跑迁移（调 scripts/db-setup.sh）
	@bash scripts/db-setup.sh

.PHONY: sqlc-schema
sqlc-schema: ## 由 migrations/pg 生成 sqlc 专用 schema.sql
	@bash scripts/sqlc-schema.sh

.PHONY: sqlc-check-schema
sqlc-check-schema: ## 校验 sqlcgen/schema.sql 与迁移一致（pre-commit 调用）
	@bash scripts/sqlc-schema.sh --check

.PHONY: sqlc
sqlc: ## 生成 sqlc 代码（先同步 schema）
	@command -v sqlc >/dev/null 2>&1 || { echo "✗ 先执行 make tools"; exit 1; }
	@bash scripts/sqlc-schema.sh
	sqlc generate

# ---------- 启动 ----------
.PHONY: run-server
run-server: ## 启动后端服务
	$(GO) run ./cmd/knot-server

.PHONY: run
run: ## 启动 TUI
	$(GO) run ./cmd/knot tui

.PHONY: web-dev
web-dev: ## 启动 Vite 开发服务器（代理到本地 Go）
	cd web && pnpm run dev

# ---------- 测试 ----------
.PHONY: test
test: ## 运行测试（含 race）
	$(GO) test -race -count=1 ./...

.PHONY: test-short
test-short: ## 跳过集成测试
	$(GO) test -short -count=1 ./...

.PHONY: e2e
e2e: ## 本地真实 PG 端到端验收（建临时库，跑完自动清理）
	@bash scripts/e2e-local.sh

.PHONY: e2e-web
e2e-web: ## 前端 api 层连真实后端（需先起 knot-server，KNOT_WEB_E2E_BASE 指定地址）
	@if [ -z "$(KNOT_WEB_E2E_BASE)" ]; then echo "✗ 用法: make e2e-web KNOT_WEB_E2E_BASE=http://127.0.0.1:8080"; exit 1; fi
	cd web && KNOT_WEB_E2E_BASE=$(KNOT_WEB_E2E_BASE) pnpm test:unit -- --run src/api/__tests__/integration.spec.ts

.PHONY: cover
cover: ## 生成覆盖率报告
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "✓ coverage.html"

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: lint
lint: ## golangci-lint
	@command -v golangci-lint >/dev/null 2>&1 || { echo "✗ 先执行 make tools"; exit 1; }
	golangci-lint run ./...

.PHONY: check
check: vet lint test ## 提交前检查

# ---------- 打包 ----------
.PHONY: build
build: build-cli build-server ## 打包 CLI 和 Server

.PHONY: build-cli
build-cli: ## 打包 CLI（本机平台）
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(CLI_BIN) ./cmd/knot
	@echo "✓ $(CLI_BIN)"

.PHONY: build-server
build-server: ## 交叉编译 Linux 服务端
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) \
		$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(SERVER_BIN) ./cmd/knot-server
	@echo "✓ $(SERVER_BIN)"

.PHONY: web-build
web-build: ## 构建 Vue 到 web/dist（调 scripts/web-build.sh）
	@if [ ! -f scripts/web-build.sh ]; then echo "✗ 缺少 scripts/web-build.sh"; exit 1; fi
	@bash scripts/web-build.sh

.PHONY: install
install: ## 安装 CLI 到 GOBIN
	CGO_ENABLED=0 $(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/knot

.PHONY: dist
dist: web-build build-server ## 打部署包：二进制 + web/dist + 配置模板
	@rm -rf $(BIN_DIR)/dist && mkdir -p $(BIN_DIR)/dist/web
	cp $(SERVER_BIN) $(BIN_DIR)/dist/knot-server
	cp -r web/dist/. $(BIN_DIR)/dist/web/
	@if [ -f config.example.toml ]; then cp config.example.toml $(BIN_DIR)/dist/config.toml; fi
	@tar -C $(BIN_DIR) -czf $(BIN_DIR)/knot-deploy.tar.gz dist
	@echo "✓ $(BIN_DIR)/knot-deploy.tar.gz"

# ---------- 部署 ----------
.PHONY: deploy
deploy: ## 在服务器上安装/升级部署包（需 root，参数 DEPLOY_PKG=...）
	@if [ -z "$(DEPLOY_PKG)" ]; then echo "✗ 用法: make deploy DEPLOY_PKG=/tmp/knot-deploy.tar.gz"; exit 1; fi
	@bash scripts/deploy.sh "$(DEPLOY_PKG)"

# ---------- 清理 ----------
.PHONY: clean
clean: clean-build clean-test ## 清理打包结果与测试结果
	@echo "✓ 已清理"

.PHONY: clean-build
clean-build: ## 清理打包结果
	rm -rf $(BIN_DIR)
	@echo "✓ 已删除 $(BIN_DIR)/"

.PHONY: clean-test
clean-test: ## 清理测试结果
	rm -f coverage.out coverage.html
	find . -name '*.test' -type f -delete
	$(GO) clean -testcache
	@echo "✓ 已清理测试结果与测试缓存"

.PHONY: clean-web
clean-web: ## 清理前端构建产物
	rm -rf web/dist
	@echo "✓ 已删除 web/dist/"

.PHONY: clean-deps
clean-deps: ## 清空依赖：删 go.mod/go.sum + 模块缓存 + 构建缓存
	rm -f go.mod go.sum
	$(GO) clean -modcache
	$(GO) clean -cache
	@echo "✓ 依赖已清空，执行 make setup 重新初始化"

.PHONY: clean-deps-soft
clean-deps-soft: ## 只 tidy 未使用依赖，保留缓存
	$(GO) mod tidy
	@echo "✓ go.mod 已整理"
