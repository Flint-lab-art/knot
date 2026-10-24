# Web 端（Vue 只读站）

## 定位

Vue 端仅做**只读展示**，无写入接口，只消费 `/api/public/*`。数据范围由服务端 `visibility='public'` 过滤，前端不做可见性判断。

- 无登录、无编辑、无删除；
- 无 SSR、无 SEO 深度优化（可加基础 meta）；
- 目标：公开笔记的浏览与阅读体验。

## 技术选型

| 项 | 选择 | 说明 |
|---|---|---|
| 框架 | Vue 3 + Vite | `pnpm create vue@latest`，选 Router + Vitest + Linter + Prettier |
| 语言 | TypeScript | 与 Go 后端 DTO 对齐，减少字段拼错；`vue-tsc` 做类型检查 |
| 路由 | Vue Router，`createWebHistory` | 与 Caddy SPA fallback 配套 |
| 状态 | 不引 Pinia | 只读站用模块级 `ref` / composable 足够 |
| JSX | 不启用 | SFC + template 足够 |
| E2E | 不做 | 手动验证 + 单测覆盖关键逻辑 |
| Markdown | `markdown-it` + `DOMPurify` | 见“渲染与安全” |
| 字体 | 自托管 Inter + Noto Sans CJK SC | 见“字体策略”，不使用 SF Pro |

## 路由与页面

| 路由 | 页面 | 说明 |
|---|---|---|
| `/` | 笔记列表 | 支持 `?tag=` 筛选、`?page=` 分页 |
| `/notes/:id` | 阅读页 | 非 public 返回 404 |
| `/:pathMatch(.*)*` | 404 | 提示 + 回列表按钮 |

标签筛选**不单独开页**，做成列表页顶部横向标签条，点击切换 `?tag=`。

### 全局壳

```text
┌────────────────────────────────────────────┐
│ Knot                                       │  顶栏，站名点回首页
├────────────────────────────────────────────┤
│                                            │
│              页面内容                      │
│                                            │
└────────────────────────────────────────────┘
```

MVP 不做页脚；如果加，只放一行版权/许可。

## 页面线框

### 列表页 `/`

```text
┌────────────────────────────────────────────┐
│ Knot                                       │
├────────────────────────────────────────────┤
│ [全部] [go] [notes] [linux] ...            │  标签条，选中高亮
├────────────────────────────────────────────┤
│ ┌────────────────────────────────────────┐ │
│ │ 笔记标题 A                             │ │
│ │ 2026-10-20 · go, notes                 │ │
│ └────────────────────────────────────────┘ │
│ ┌────────────────────────────────────────┐ │
│ │ 笔记标题 B                             │ │
│ │ 2026-10-18 · linux                     │ │
│ └────────────────────────────────────────┘ │
│ ┌────────────────────────────────────────┐ │
│ │ 笔记标题 C                             │ │
│ │ 2026-10-16 · go                        │ │
│ └────────────────────────────────────────┘ │
├────────────────────────────────────────────┤
│              [上一页] 1/3 [下一页]         │
└────────────────────────────────────────────┘
```

- **轻量卡片列表**，每篇笔记一张卡，细边框、小圆角、无默认阴影。
- **不显示摘要**。公开列表接口不返回正文，避免为摘要改接口；标题 + 时间 + 标签足够，卡片控制在三行以内。
- 标签条横向排列，多时折行；MVP 单选。
- 分页用“上一页/下一页”，不做页码跳转。

### 阅读页 `/notes/:id`

```text
┌────────────────────────────────────────────┐
│ ← 返回列表                                 │
├────────────────────────────────────────────┤
│ 笔记标题                                   │
│ 2026-10-20 · go, notes                     │
├────────────────────────────────────────────┤
│                                            │
│   Markdown 正文（渲染后）                  │
│   最大宽 760px，居中，不套卡片             │
│                                            │
├────────────────────────────────────────────┤
│ 标签：go  notes                            │  点击跳回列表 ?tag=
└────────────────────────────────────────────┘
```

- 正文最大宽度 760px，避免宽屏一行过长。
- **不套卡片**，正文直接铺在页面上，避免边框切割视线。
- 代码块横向滚动，不撑破布局。
- 标题层级、行高、段间距按“视觉风格”参数。

### 404

一句“页面不存在” + “回列表”按钮，不做花哨插画。

## 状态设计

每个拉数据的页面必须处理三种状态，不能只写 happy path。

| 状态 | 列表页 | 阅读页 |
|---|---|---|
| 加载中 | 骨架卡片或“加载中”占位 | “加载中”占位 |
| 空 | “还没有公开笔记” | —（404 覆盖） |
| 错误 | 区分网络错误 / 429 / 500，给重试按钮 | 同左 |
| 404 | — | “笔记不存在或未公开” + 回列表 |

**429**：公开接口有限流，被限时显示“请求过于频繁，请稍后再试”，不是白屏。`api/client.ts` 统一处理，页面只读错误类型。

**筛选与分页放 URL**：`/?tag=go&page=2`。刷新可恢复，可分享链接。不要用组件内 state 存筛选。

## 视觉风格

**Apple-inspired，阅读优先。** 取“排版 + 留白 + 系统字体 + 克制圆角阴影 + 微过渡”，列表页用轻量卡片，阅读页保持无卡片单栏。

```text
字号
  正文 17px / 行高 1.7
  H1 32px / 700
  H2 24px / 600
  H3 20px / 600
  元信息 14px / 次要色

间距
  内容区最大宽 760px，左右 padding 24px（窄屏 16px）
  段落间距 1em
  标题上间距 1.5em，下 0.5em

颜色
  亮色
    背景 #ffffff
    正文 #1d1d1f
    次要 #6e6e73
    分隔 #d2d2d7
  暗色
    背景 #000000
    正文 #f5f5f7
    次要 #86868b
    分隔 #424245
  强调 单色，链接与选中标签共用

圆角
  卡片 10px
  标签 pill 999px
  按钮 / 代码块 / 输入框 8px

阴影
  卡片默认无阴影
  浮层 0 4px 16px rgba(0,0,0,0.08)
  其余不用

动效
  transition 150–200ms cubic-bezier(0.4, 0, 0.2, 1)
  只用于 color / background / border / opacity
  卡片 hover 仅边框色加深，不做位移/放大
  不做滚动动画、入场动画、视差

暗色
  跟随 prefers-color-scheme，CSS 变量切换
  不做手动切换按钮（MVP）
```

### 卡片参数（仅列表页）

```text
边框       1px solid 分隔色
圆角       10px
内边距     16px 20px
卡片间距   10px
背景       与页面同色，不浮起
默认阴影   无
hover      边框色加深，不做位移/放大
内容       标题 + 时间 + 标签，最多三行，不显示摘要
```

**响应式两档**：宽屏单栏居中，窄屏（< 768px）padding 收窄到 16px，卡片内边距收到 14px 16px。不做独立移动版。

## 字体策略（合规优先）

**禁止**在 Web 端嵌入或使用任何来源的 SF Pro 字体文件。苹果授权仅限本地 UI mock-up，不覆盖 Web 分发。

**首选方案**：自托管开源字体，保证跨平台一致。

```css
font-family: "Inter Variable", "Noto Sans CJK SC",
             -apple-system, BlinkMacSystemFont, "Segoe UI",
             system-ui, sans-serif;
```

- 英文/数字：**Inter**，OFL 协议，`@fontsource-variable/inter`。
- 中文：**Noto Sans CJK SC**（思源黑体），OFL-1.1，`noto-sans-cjk-sc`，按需引入 regular/bold。
- 代码：`ui-monospace, "SF Mono", Menlo, Consolas, monospace`。**SF Mono 只在系统已安装时回退**，不分发字体文件。

**降级方案**（不自托管时）：纯系统字体栈 `-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`。苹果设备体验最佳，其他平台回退系统 UI 字体。

## 数据层

```text
src/api/
├── client.ts      # fetch 薄封装：base URL、JSON、错误码、429
├── notes.ts       # getNotes(params) / getNote(id)
└── tags.ts        # getTags()
```

- 统一 base URL `/api/public`，开发时由 Vite 代理到 `127.0.0.1:8080`。
- 错误统一抛带 `code` / `msg` 的错误，页面按类型渲染。
- 不引 axios，`fetch` 足够。

**状态管理**：不引 Pinia。需要跨组件共享的（当前筛选、列表缓存）放 `src/composables/useNotes.ts` 模块级 `ref`：

```ts
// src/composables/useNotes.ts
const notes = ref<NoteMeta[]>([])
const loading = ref(false)
export function useNotes() { ... }
```

出现“需要持久化主题”“多页面共享带失效逻辑的缓存”时再考虑 Pinia。

## Markdown 渲染与安全

- `markdown-it` 渲染 + `DOMPurify` sanitize。
- 禁用 raw HTML，或严格 sanitize 后再插入。
- 与 Caddy CSP 对齐：`script-src 'self'`，不引入内联脚本。
- **Vitest 必须覆盖**：
  - `<script>alert(1)</script>` 被移除；
  - `<img onerror=...>` 的 `onerror` 被剥离；
  - `javascript:` 链接被替换或移除；
  - 正常 GFM（表格、代码块、任务列表）不被误伤。

这一组测试比 API 封装测试价值更高，是公开站的 XSS 底线。

## 构建与开发

开发时前后端分开跑：

```bash
make run-server   # 后端 127.0.0.1:8080
make web-dev      # Vite 开发服务器，/api 代理到后端
```

`web/vite.config.ts`：

```ts
server: {
  proxy: { '/api': 'http://127.0.0.1:8080' },
}
```

生产构建：

```bash
make web-build    # 调 scripts/web-build.sh，产出 web/dist
```

`web/dist` 由 Caddy 托管，`try_files {path} /index.html` 做 SPA fallback。详见 `DEPLOYMENT.md`。

## 测试

| 对象 | 内容 |
|---|---|
| `api/client.ts` | 错误码映射、429 处理 |
| `composables/useNotes.ts` | 筛选、分页参数拼装 |
| Markdown 渲染 | **sanitize 安全用例**（最高优先级） |
| 组件 | 列表空态/错误态渲染，阅读页 404 |

不引 Playwright/Cypress，SPA fallback 和 API 代理用 `curl` 手测。

## 不做的事

- 写入、登录、编辑、删除
- Pinia、JSX、E2E
- SSR、SEO 深度优化
- 公开端搜索（`/api/search` 留给 CLI，公开端标签筛选足够）
- 收藏、点赞、评论
- 手动暗色切换按钮
- 独立移动版
- 阅读页套卡片（列表页轻量卡片可以，阅读页不行）
- 重阴影 / 大圆角卡片墙
- 动画库、滚动视差、玻璃拟态

## 与后端契约

- 只调 `/api/public/*`，不带 token。
- 列表与阅读接口返回字段以 `API.md` 为准；不假设返回 `version`、`raw_md` 以外的内部字段。
- 服务端已按 `visibility='public'` 过滤，前端**不做**可见性判断，也不缓存私有数据。
