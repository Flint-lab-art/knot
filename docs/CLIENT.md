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

`~/.config/knot/config.toml`（遵循 XDG，`os.UserConfigDir()`）：

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

加载顺序：默认值 → 配置文件 → 环境变量覆盖（`KNOT_SERVER_URL`、`KNOT_TOKEN`）。

- `editor.command` 加载时 `exec.LookPath` 检查，找不到明确报错。
- **token 不进公开 dotfiles 仓库**，dotfiles 里放 `config.toml.example`。

## 换机迁移

只带 `~/.config/knot/config.toml`，缓存目录不带，首次 `knot sync` 重建。
