# web

Knot 的公开只读站（Vue 3 + Vite + TypeScript）。规格见仓库根目录的 `docs/WEB.md`。

## 环境

- Node.js `^22.18.0 || >=24.12.0`
- 包管理器：**pnpm**（不使用 npm / yarn；没有 pnpm 时先 `corepack enable`）

## 常用命令

```sh
pnpm install         # 首次安装依赖
pnpm dev             # 开发服务器；/api 代理到 127.0.0.1:8080
pnpm build           # 类型检查 + 生产构建到 web/dist
pnpm type-check      # 仅类型检查（vue-tsc）
pnpm test:unit       # Vitest（默认 watch）
pnpm test:unit -- --run      # 单次运行（提交前）
pnpm lint            # oxlint + eslint（带 --fix，会改文件）
pnpm format          # Prettier 格式化 src/
```

## 约定

- 使用 TypeScript；类型定义放在 `src/types/`，与 `docs/API.md` 的 DTO 字段保持一致。
- 路径别名 `@` → `src`。
- 格式：无分号、单引号、行宽 100（见 `.prettierrc.json`）。
- 不引入 Pinia / JSX / E2E。
- `pnpm-lock.yaml` 必须提交；不要生成 `package-lock.json`。
