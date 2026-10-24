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
  echo "✗ 未找到 node，请先安装 Node.js >= 20"
  exit 1
fi
if ! command -v npm >/dev/null 2>&1; then
  echo "✗ 未找到 npm"
  exit 1
fi

echo "→ Node $(node -v) / npm $(npm -v)"
cd "${WEB_DIR}"

if [ -f package-lock.json ]; then
  echo "→ npm ci"
  npm ci
else
  if [ "${CI:-}" = "true" ]; then
    echo "✗ CI 环境下缺少 package-lock.json"
    exit 1
  fi
  echo "⚠ 缺少 package-lock.json，改用 npm install（建议之后提交 lock 文件）"
  npm install
fi

echo "→ npm run build"
npm run build

if [ ! -f dist/index.html ]; then
  echo "✗ 构建完成但未找到 dist/index.html，检查 vite build 输出目录"
  exit 1
fi

echo "✓ web/dist 已生成"
