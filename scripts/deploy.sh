#!/usr/bin/env bash
# scripts/deploy.sh —— 在服务器上安装/升级 knot-server（docs/DEPLOYMENT.md）
#
# 用法（服务器上，root 或 sudo）：
#   sudo scripts/deploy.sh /tmp/knot-deploy.tar.gz
#   sudo scripts/deploy.sh /tmp/knot-deploy.tar.gz --skip-migrate
#
# 做四件事：
#   1. 解包到临时目录并校验产物（二进制可执行、web/index.html 存在）
#   2. 备份当前二进制到 knot-server.prev（回滚用）
#   3. 安装 web/ 与二进制
#   4. 跑迁移并重启服务，最后做一次 healthz 冒烟
#
# 失败时自动回滚二进制并重启（web 不做回滚：前端缓存与后端向后兼容，
# 回滚二进制已能恢复可用性）。
set -euo pipefail

APP_DIR="${APP_DIR:-/opt/knot}"
SERVICE="${SERVICE:-knot-server}"
CONFIG="${APP_DIR}/config.toml"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/healthz}"

tarball="${1:-}"
skip_migrate=0
if [ "${2:-}" = "--skip-migrate" ]; then skip_migrate=1; fi

if [ -z "${tarball}" ] || [ ! -f "${tarball}" ]; then
  echo "用法: $0 <knot-deploy.tar.gz> [--skip-migrate]" >&2
  exit 1
fi
# SKIP_ROOT_CHECK=1 只给本地联调/测试用，生产不要设置。
if [ "${EUID}" -ne 0 ] && [ "${SKIP_ROOT_CHECK:-0}" != "1" ]; then
  echo "✗ 需要 root（或用 sudo）" >&2
  exit 1
fi
if [ ! -f "${CONFIG}" ]; then
  echo "✗ 缺少 ${CONFIG}（先按 docs/DEPLOYMENT.md 写好配置）" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "${work}"' EXIT

echo "→ 解包 ${tarball}"
tar -xzf "${tarball}" -C "${work}"
if [ ! -x "${work}/dist/knot-server" ]; then
  echo "✗ 包内缺少可执行 dist/knot-server" >&2
  exit 1
fi
if [ ! -f "${work}/dist/web/index.html" ]; then
  echo "✗ 包内缺少 dist/web/index.html（前端未构建？）" >&2
  exit 1
fi

echo "→ 备份当前二进制"
rollback=0
if [ -f "${APP_DIR}/knot-server" ]; then
  cp -p "${APP_DIR}/knot-server" "${APP_DIR}/knot-server.prev"
  rollback=1
fi

do_rollback() {
  if [ "${rollback}" = 1 ]; then
    echo "✗ 部署失败，回滚二进制" >&2
    mv -f "${APP_DIR}/knot-server.prev" "${APP_DIR}/knot-server"
    systemctl restart "${SERVICE}" || true
  fi
}

echo "→ 安装文件"
install -m 755 "${work}/dist/knot-server" "${APP_DIR}/knot-server"
if command -v rsync >/dev/null 2>&1; then
  rsync -a --delete "${work}/dist/web/" "${APP_DIR}/web/"
else
  rm -rf "${APP_DIR}/web.new" && cp -a "${work}/dist/web" "${APP_DIR}/web.new"
  rm -rf "${APP_DIR}/web.old" && [ -d "${APP_DIR}/web" ] && mv "${APP_DIR}/web" "${APP_DIR}/web.old"
  mv "${APP_DIR}/web.new" "${APP_DIR}/web"
  rm -rf "${APP_DIR}/web.old"
fi

if [ "${skip_migrate}" = 0 ]; then
  echo "→ 执行迁移"
  if ! sudo -u knot "${APP_DIR}/knot-server" migrate --config "${CONFIG}"; then
    do_rollback
    exit 1
  fi
else
  echo "→ 跳过迁移（--skip-migrate）"
fi

echo "→ 重启服务"
if ! systemctl restart "${SERVICE}"; then
  do_rollback
  exit 1
fi

echo "→ healthz 冒烟"
for i in $(seq 1 20); do
  if curl -fsS --max-time 2 "${HEALTH_URL}" >/dev/null 2>&1; then
    echo "✓ 部署完成（healthz 正常）"
    exit 0
  fi
  sleep 1
done

echo "✗ healthz 未通过（${HEALTH_URL}）" >&2
journalctl -u "${SERVICE}" -n 30 --no-pager >&2 || true
do_rollback
exit 1
