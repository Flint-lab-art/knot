#!/usr/bin/env bash
# scripts/backup.sh —— PostgreSQL 逻辑备份（pg_dump 自定义格式）
#
# 用法：
#   PG_DSN='postgres://knot@/knot?host=/run/postgresql' scripts/backup.sh
#   KEEP=14 scripts/backup.sh          # 保留 14 份（默认 7）
#   BACKUP_DIR=/opt/knot/backup scripts/backup.sh
#
# 设计：
#   - 先写临时文件再 mv：备份中途失败不会留下一个"看起来完整"的半截文件；
#   - 按修改时间清理旧文件，只删本脚本产出的 knot-*.dump；
#   - 恢复演练：pg_restore -l 能列出目录就说明归档可读（见 docs/DEPLOYMENT.md）。
set -euo pipefail

PG_DSN="${PG_DSN:-}"
BACKUP_DIR="${BACKUP_DIR:-/opt/knot/backup}"
KEEP="${KEEP:-7}"

if [ -z "${PG_DSN}" ]; then
  echo "✗ 需要 PG_DSN 环境变量" >&2
  exit 1
fi
if ! command -v pg_dump >/dev/null 2>&1; then
  echo "✗ 未找到 pg_dump" >&2
  exit 1
fi
case "${KEEP}" in
  ''|*[!0-9]*) echo "✗ KEEP 必须是正整数" >&2; exit 1 ;;
esac

mkdir -p "${BACKUP_DIR}"
name="knot-$(date +%F-%H%M%S).dump"
tmp="${BACKUP_DIR}/.${name}.partial"
out="${BACKUP_DIR}/${name}"

echo "→ pg_dump → ${out}"
# custom 格式支持并行恢复与单表恢复，比纯 SQL 更适合日常备份。
pg_dump -Fc --no-owner --no-privileges -d "${PG_DSN}" -f "${tmp}"

# 至少确认归档头能被解析，避免把损坏文件当成备份。
pg_restore -l "${tmp}" >/dev/null

mv "${tmp}" "${out}"
echo "  ✓ $(du -h "${out}" | cut -f1) ${name}"

# 清理：只保留最新的 KEEP 份（文件名带时间戳，可按字典序排序）。
count=$(find "${BACKUP_DIR}" -maxdepth 1 -name 'knot-*.dump' -type f | wc -l | tr -d ' ')
if [ "${count}" -gt "${KEEP}" ]; then
  find "${BACKUP_DIR}" -maxdepth 1 -name 'knot-*.dump' -type f \
    | sort | head -n "$((count - KEEP))" \
    | while read -r old; do
        echo "  - 删除旧备份 $(basename "${old}")"
        rm -f "${old}"
      done
fi

echo "✓ 备份完成（保留 ${KEEP} 份）"
