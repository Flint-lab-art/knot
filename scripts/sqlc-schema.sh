#!/usr/bin/env bash
# scripts/sqlc-schema.sh —— 拼出 sqlc 专用的 schema.sql（make sqlc-schema）
#
# sqlc 的 schema 解析器处理不了 goose 的 `-- +goose Up/Down` 混排，
# 所以把 migrations/pg/*.sql 的 Up 段落抄成纯 DDL 副本。
# 改完迁移必须重跑本脚本并提交生成结果；`make sqlc-check-schema`
# 在 CI 缺失的情况下由 pre-commit 兜底校验副本是否最新。
#
# 用法:
#   scripts/sqlc-schema.sh           # 生成 output
#   scripts/sqlc-schema.sh --check   # 只检查是否与迁移一致
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$ROOT/internal/store/pg/sqlcgen/schema.sql"
MIGRATIONS="$ROOT/migrations/pg"

# trgm 索引与 sqlc 的表结构映射无关，跳过（副本里显式标注）。
SKIP=( "0002_trgm_index.sql" )

build() {
  cat <<'EOF'
-- sqlc 专用 schema：由 migrations/pg/*.sql 的 Up 段落拼接而成，禁止手改。
-- 重新生成：make sqlc-schema（改完迁移后必须跑）。
--
-- 为什么要副本：goose 的 `-- +goose Up/Down` 在同一文件里混排，
-- sqlc 的 schema 解析器无法判断哪部分是最终结构。
EOF
  for f in $(ls "$MIGRATIONS"/*.sql | sort); do
    base="$(basename "$f")"
    skip=0
    for s in "${SKIP[@]}"; do [ "$base" = "$s" ] && skip=1; done
    if [ "$skip" = 1 ]; then
      printf '\n-- ---- %s（索引与 sqlc 无关，略）----\n' "$base"
      continue
    fi
    printf '\n-- ---- migrations/pg/%s ----\n\n' "$base"
    # 只取 -- +goose Up 与 -- +goose Down 之间的内容，去掉注释与空行首尾。
    awk '
      /^-- \+goose Up/   {up=1; next}
      /^-- \+goose Down/ {up=0}
      up {print}
    ' "$f" | sed -e '/^[[:space:]]*$/d' -e '/^--/d'
  done
}

mkdir -p "$(dirname "$OUT")"
if [ "${1:-}" = "--check" ]; then
  if ! diff -u "$OUT" <(build) >/tmp/sqlc-schema.diff 2>&1; then
    echo "✗ internal/store/pg/sqlcgen/schema.sql 与迁移不一致，请运行 make sqlc-schema" >&2
    cat /tmp/sqlc-schema.diff >&2
    exit 1
  fi
  echo "✓ sqlc schema 与迁移一致"
else
  build >"$OUT"
  echo "✓ 已生成 ${OUT#"$ROOT"/}"
fi
