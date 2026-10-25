#!/usr/bin/env bash
# scripts/db-setup.sh —— 本地开发用的 PG 初始化（幂等，可重复执行）
#
# 做三件事：建库（已存在则跳过）、安装 pg_trgm 扩展、跑 knot-server migrate。
# 扩展必须由有权建扩展的角色安装，所以单独放在这里，而不是塞进迁移文件。
#
# 用法:
#   scripts/db-setup.sh                       # 用内置默认值
#   PG_ADMIN_DSN=... PG_DSN=... scripts/db-setup.sh
#   make db-setup
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

DB_NAME="${KNOT_DB_NAME:-knot_migrate_check}"
# 默认走本机 Unix socket（host=/run/postgresql）+ postgres 角色：
# Debian/Ubuntu 的默认 pg_hba 对 local 是 trust/peer，通常不需要密码。
# 换成 TCP + 密码认证的话，覆盖这两个变量即可：
#   PG_ADMIN_DSN='postgres://postgres:密码@127.0.0.1:5432/postgres?sslmode=disable'
PG_ADMIN_DSN="${PG_ADMIN_DSN:-postgres://postgres@/postgres?host=/run/postgresql&sslmode=disable}"
PG_DSN="${PG_DSN:-postgres://postgres@/${DB_NAME}?host=/run/postgresql&sslmode=disable}"

if ! command -v psql >/dev/null 2>&1; then
  echo "✗ 未找到 psql，请先安装 PostgreSQL 客户端" >&2
  exit 1
fi

echo "→ 确保数据库 ${DB_NAME} 存在"
exists="$(psql "$PG_ADMIN_DSN" -tA -c "SELECT 1 FROM pg_database WHERE datname = '${DB_NAME}'")"
if [ "$exists" = "1" ]; then
  echo "  ✓ 已存在"
else
  psql "$PG_ADMIN_DSN" -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${DB_NAME}" >/dev/null
  echo "  ✓ 已创建"
fi

echo "→ 安装 pg_trgm 扩展（0002 的 trigram 索引依赖它）"
psql "$PG_DSN" -v ON_ERROR_STOP=1 -c "CREATE EXTENSION IF NOT EXISTS pg_trgm" >/dev/null
echo "  ✓ pg_trgm"

echo "→ 执行迁移"
KNOT_PG_DSN="$PG_DSN" go run ./cmd/knot-server migrate
