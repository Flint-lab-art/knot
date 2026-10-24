#!/usr/bin/env bash
# 构建 Vue 到 web/dist
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DIR="${ROOT}/web"

if [ ! -d "${WEB_DIR}" ]; then
  echo "✗ 未找到 ${WEB_DIR}，Vue 工程还没初始化"
  exit 1
fi
if ! command -v node >/dev/null 2>&1; then
  echo "✗ 未找到 node，请先安装 Node.js ^22.18.0 || >=24.12.0"
  exit 1
fi
if ! command -v pnpm >/dev/null 2>&1; then
  echo "✗ 未找到 pnpm，请先执行: corepack enable"
  exit 1
fi

echo "→ Node $(node -v) / pnpm $(pnpm -v)"
cd "${WEB_DIR}"

if [ -f pnpm-lock.yaml ]; then
  echo "→ pnpm install --frozen-lockfile"
  pnpm install --frozen-lockfile
else
  if [ "${CI:-}" = "true" ]; then
    echo "✗ CI 环境下缺少 pnpm-lock.yaml"
    exit 1
  fi
  echo "⚠ 缺少 pnpm-lock.yaml，改用 pnpm install（建议之后提交 lock 文件）"
  pnpm install
fi

echo "→ pnpm run build"
pnpm run build

if [ ! -f dist/index.html ]; then
  echo "✗ 构建完成但未找到 dist/index.html，检查 vite build 输出目录"
  exit 1
fi

echo "✓ web/dist 已生成"
