#!/usr/bin/env bash
# scripts/dev-setup.sh
# Knot 开发环境依赖安装脚本
# 用法: bash scripts/dev-setup.sh
set -euo pipefail

# ---------- 可调参数 ----------
MODULE="${MODULE:-knot}"                 # go mod init 的模块名
GO_MIN="1.23"                            # 最低 Go 版本
# 国内默认走 goproxy.cn；海外开发机改成 https://proxy.golang.org,direct
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
export GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"   # 国内校验库；海外可删

# ---------- 1. 检查 Go ----------
if ! command -v go >/dev/null 2>&1; then
  echo "✗ 未找到 go，请先安装 Go >= ${GO_MIN}: https://go.dev/dl/"
  exit 1
fi
GO_VER="$(go env GOVERSION | sed 's/^go//')"
echo "✓ Go 版本: ${GO_VER} (要求 >= ${GO_MIN})"
if [ "$(printf '%s\n' "$GO_MIN" "$GO_VER" | sort -V | head -n1)" != "$GO_MIN" ]; then
  echo "✗ Go 版本过低，请升级到 >= ${GO_MIN}"
  exit 1
fi

# ---------- 2. 初始化 go.mod ----------
if [ ! -f go.mod ]; then
  echo "→ 初始化 go.mod: module ${MODULE}"
  go mod init "${MODULE}"
else
  echo "✓ go.mod 已存在，跳过 init"
fi

# ---------- 3. 运行时依赖 ----------
echo "→ 拉取运行时依赖..."
RUNTIME_DEPS=(
  # 两端共用
  "github.com/google/uuid"
  "github.com/knadh/koanf/v2"
  "github.com/caarlos0/env/v11"

  # CLI / TUI
  "github.com/charmbracelet/bubbletea"
  "github.com/charmbracelet/bubbles"
  "github.com/charmbracelet/lipgloss"
  "github.com/spf13/cobra"
  "github.com/charmbracelet/glamour"
  "github.com/ktr0731/go-fuzzyfinder"

  # Server
  "github.com/go-chi/chi/v5"
  "github.com/jackc/pgx/v5"
  "github.com/pressly/goose/v3"

  # 本地 SQLite 缓存（纯 Go，无 CGO）
  "modernc.org/sqlite"

  # 二期 RAG 再取消注释
  # "github.com/pgvector/pgvector-go"
)
for dep in "${RUNTIME_DEPS[@]}"; do
  echo "  + ${dep}"
  go get "${dep}"
done

# ---------- 4. 构建工具（不写入 go.mod） ----------
echo "→ 安装构建工具到 $(go env GOPATH)/bin ..."
TOOLS=(
  "github.com/sqlc-dev/sqlc/cmd/sqlc@latest"
  "github.com/pressly/goose/v3/cmd/goose@latest"
  "github.com/golangci/golangci-lint/cmd/golangci-lint@latest"
)
for tool in "${TOOLS[@]}"; do
  echo "  + ${tool}"
  go install "${tool}"
done

# 确保 GOPATH/bin 在 PATH
GOBIN="$(go env GOPATH)/bin"
if ! echo "$PATH" | grep -q "${GOBIN}"; then
  echo "⚠ 请把 ${GOBIN} 加入 PATH，例如在 ~/.zshrc 或 ~/.bashrc 追加："
  echo "    export PATH=\"${GOBIN}:\$PATH\""
fi

# ---------- 5. 校验 ----------
echo "→ 校验工具："
for bin in sqlc goose golangci-lint; do
  if command -v "$bin" >/dev/null 2>&1; then
    echo "  ✓ $bin $("$bin" version 2>/dev/null | head -n1 || true)"
  else
    echo "  ✗ $bin 不在 PATH，请检查 ${GOBIN}"
  fi
done

# ---------- 6. 整理依赖 ----------
echo "→ go mod tidy（代码尚未引用时可能移除部分依赖，正常）"
go mod tidy
