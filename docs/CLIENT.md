# CLI / TUI

## 命令总览

| 子命令 | 作用 |
|---|---|
| `knot tui` | 交互式 TUI 主界面 |
| `knot new` | 新建笔记，`--title` / `--tags` / `--visibility` |
| `knot edit [selector]` | 编辑（懒加载正文，归一化 hash 比对后提交） |
| `knot show [selector]` | 终端预览 markdown，`--render` 渲染 |
| `knot rm [selector]` | 删除，`-y` 跳过确认 |
| `knot list` | 列出笔记，`--tag` / `--short` / `--json` |
| `knot search` | 服务端 pg_trgm 全文检索 |
| `knot tag` | 标签管理：`list / add / rm / rename` |
| `knot sync` | 全量元数据对账，`--full` 强制重建 |
| `knot cache` | `info / clear --content / clear --all` |
| `knot completion` | bash / zsh / fish 补全 |

## Selector 规则

不传参数 → 唤起 fzf 选择面板；关键词 → 模糊匹配标题；完整 UUID → 精准定位（`--exact` 强制精确）。

MVP 用 `github.com/ktr0731/go-fuzzyfinder`，单二进制无外部依赖；二期检测系统 `fzf` 优先调用，否则 fallback。

当前实现（阶段 5 落地）：

- **完整 UUID**：直接 `GET /api/notes/:id`，不依赖本地缓存；
- **关键词**：在本地缓存里做**不区分大小写的子串匹配**；匹配到 1 条才执行，
  0 条提示「先跑 `knot sync`？」，多条则报错并列出候选（避免误删/误覆盖）；
- 交互式 fzf 面板与 `--exact` 留到二期，命令里暂时没有这两个入口。

## TUI 布局

```text
┌──────────────────────────────────────────────┐
│ 列表（左）           │ 详情 / 编辑 / 会话（右）│
│                      │                        │
│ > 笔记 A             │ # 笔记 A               │
│   笔记 B             │ 正文预览 / 编辑器      │
│   笔记 C             │                        │
│                      │                        │
├──────────────────────┴────────────────────────┤
│ 状态栏：同步状态 / 当前标签 / 快捷键提示       │
└──────────────────────────────────────────────┘
```

- 左栏列表，右栏详情；窄终端（< 100 列）自动切单栏。
- 底部状态栏常驻：同步状态、当前筛选、快捷键提示。
- `/` 唤起搜索框；`t` 打开标签面板；`s` 触发 sync；`q` 退出。
- 右栏顶部 tab：`正文` / `编辑` / `LLM 会话`（二期）。
- 二期 LLM 面板：右栏 tab 切换，调用 `POST /api/llm/chat`，**DeepSeek API Key 只存服务端**，不经过客户端。

实际实现（阶段 6）：

- 列表读**本地缓存**（秒开），正文按选中项懒加载；启动时后台同步一次，`s` 手动触发。
- 快捷键：`j/k` 或 `↑/↓` 移动、`g/G` 首尾、`/` 搜索（本地标题子串，回车生效）、
  `t` 标签面板（`↑/↓` + 回车筛选）、`Esc` 清除筛选/关闭面板、`s` 同步、`q` 退出。
- 全文检索走服务端 `knot search`；TUI 内搜索刻意只查本地，避免每敲一个字打一次 API。
- **编辑不在 TUI 内进行**：Bubble Tea 占着终端原始模式，起外部编辑器必须暂时移交终端。
  目前用 `knot edit` 完成编辑；TUI 内 `e` 入口留到二期（需要 suspend/restore 终端）。
- 正文里显示的是原始 Markdown（不做 glamour 渲染），保证与编辑内容一致。

## 编辑与懒加载流程

```text
knot edit <selector>
  1. 查本地 note_meta
  2. GET /api/notes/:id（懒加载正文到 note_content_cache）
  3. 写入临时文件（如 /tmp/knot-edit-XXXX.md）
  4. 调用 editor.command（默认 nvim）
  5. 编辑器退出
  6. 读回临时文件，归一化 markdown（换行符、行尾空白）
  7. 计算 hash，与原始 hash 比对
  8. 无变化 → 不提交，清理临时文件
  9. 有变化 → PUT /api/notes/:id（带 base_version）
 10. 成功 → 更新本地 note_meta / note_content_cache
 11. 409 → 提示“服务端已变更，请重新拉取”，不覆盖
 12. 404 → 提示“笔记已在服务端删除”，清本地缓存
 13. 失败 → 保留临时文件路径，提示重试
```

- 外部编辑器无侵入，nvim 只是打开临时文件的进程，不依赖保存回调。
- 归一化 hash 避免空白/换行误判导致的空提交。

实际实现补充：

- 正文**总是**从服务端拉（不用本地缓存），避免用过期正文覆盖远端；
- 归一化 = `\r\n`/`\r` → `\n`、去掉每行行尾空白、去掉末尾多余空行后再补一个 `\n`；
- 编辑器非 0 退出、读回失败、PUT 失败（含 409）都会**保留临时文件**，
  错误信息里给出路径；成功或「无变化」才删除。
- `editor.args` 会拼在文件名之前，便于传 `--cmd "set ft=markdown"` 之类的参数。

## 删除流程

```text
knot rm <selector>
  → DELETE /api/notes/:id（幂等，不存在也 204）
  → 本地事务删 note_meta / note_tag_meta / note_content_cache
  → 失败：本地不动，提示重试
  → 断网：直接失败，不排队
```

## 同步

- `knot sync`：`GET /api/sync/snapshot`，本地事务 diff 对账（详见 `SYNC_CACHE.md`）。
- `knot sync --full`：先 `cache clear --all` 再全量拉，用于缓存损坏。
- 失败不推进本地状态，下次重试。

## 配置文件

`~/.config/knot/config.toml`（遵循 XDG，`os.UserConfigDir()`）；服务端用 `/opt/knot/config.toml`，`chmod 600`。仓库根目录的 `config.example.toml` 是两端字段的完整示例：

```toml
server_url = "https://knot.example.com"
token = "xxxxxxxx"

[editor]
command = "nvim"
# args = []

[cache]
# 留空则用 os.UserCacheDir()/knot/cache.db
# path = "/custom/path/cache.db"
```

加载顺序：默认值 → 配置文件 → 环境变量覆盖（`KNOT_SERVER_URL`、`KNOT_TOKEN`；服务端另有 `KNOT_PG_DSN`、`KNOT_LISTEN`）。

- 配置文件定位：`-c/--config` > `KNOT_CONFIG` > `$XDG_CONFIG_HOME/knot/config.toml`；文件不存在视为「首次使用」，不报错。
- 缓存库定位：`--cache-db` > `[cache].path` > `$XDG_CACHE_HOME/knot/cache.db`；目录自动创建（0700）。
- `editor.command` 加载时 `exec.LookPath` 检查，找不到明确报错。
- 需要网络/数据库的命令才加载配置：`knot completion` 等纯本地命令不会因缺配置失败。
- **token 不进公开 dotfiles 仓库**，dotfiles 里放 `config.toml.example`。

## 服务端 CLI

| 子命令 / 参数 | 作用 |
|---|---|
| `knot-server` | 启动 HTTP 服务 |
| `knot-server migrate` | 执行内嵌的 PG 迁移（部署/升级时先于启动运行） |
| `-c` / `--config` | 配置文件路径；不传则只用默认值与环境变量 |

配置加载顺序同客户端：默认值 → 配置文件 → 环境变量（`KNOT_PG_DSN`、`KNOT_LISTEN`）。迁移**不**在服务启动时自动执行，避免旧实例被新 schema 卡住。

## 换机迁移

只带 `~/.config/knot/config.toml`，缓存目录不带，首次 `knot sync` 重建。
