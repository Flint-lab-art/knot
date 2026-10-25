# 手动测试手册

本机手动验证 Knot 的完整功能。自动化测试（`make check`、`make e2e`）已覆盖逻辑正确性，
**本手册聚焦它们替代不了的部分：真实终端里的观感、编辑器的实际交互、浏览器里的排版与交互、
以及"我作为使用者会不会觉得别扭"。**

- 预计总时长：60–90 分钟（可分段做，每段独立）
- 环境：本地 PG + 本地 Go 服务 + 本地 Vite，不需要公网
- 每节末尾有「通过标准」，不满足就记下来，不要跳过

> 约定：`$K` 表示 `./bin/knot`，`$B` 表示 `http://127.0.0.1:8080`。
> 命令都在仓库根目录执行。

---

## 0. 准备（5 分钟）

### 0.1 先确认 PG 怎么连

Knot 的数据库就是本机的 PostgreSQL，**用 psql 直接建库即可**，不需要什么特殊环境。

```sh
# 看看本机 PG 在不在
pg_isready

# 看看有哪些可登录角色
psql -U postgres -d postgres -tAc "select rolname from pg_roles where rolcanlogin"
```

两种情况：

| 现象 | 说明 | 怎么连 |
|---|---|---|
| `psql -U postgres` 直接进得去（不要密码） | 本地 socket 是 `trust`，**推荐** | 一直用 `-U postgres` |
| 提示要密码 | `pg_hba` 是 `scram-sha-256`/`md5` | 用 `PGPASSWORD=xxx psql -U postgres` 或写 `~/.pgpass` |

> 本机当前情况：只有 `postgres` 一个角色，`psql -U postgres` 免密可进。
> 直接敲 `psql`（不带 `-U`）会用你的系统用户名 `kairo` 去连，会报
> `role "kairo" does not exist` —— 这就是为什么每条命令都要带 `-U postgres`。

### 0.2 建测试库

```sh
cd /path/to/knot

# 编译
make build-cli

# 建一个专用测试库（不要用正式数据）
psql -U postgres -d postgres -c 'CREATE DATABASE knot_manual'
psql -U postgres -d knot_manual -c 'CREATE EXTENSION IF NOT EXISTS pg_trgm'
```

想进交互式环境看看的话，`psql -U postgres -d knot_manual` 进去，用 `\dt` 看表、
`\d notes` 看结构、`\l` 看所有库，`\q` 退出。**下面的步骤都不需要进 psql 环境**，
单条 `-c` 就够了。

### 0.3 写客户端配置

CLI 需要知道服务端地址和 token：

```sh
mkdir -p ~/.config/knot
cat > ~/.config/knot/config.toml <<'EOF'
server_url = "http://127.0.0.1:8080"
token = "manual-test-token"

[editor]
command = "nvim"

[cache]
path = "/tmp/knot-manual/cache.db"
EOF
chmod 600 ~/.config/knot/config.toml
mkdir -p /tmp/knot-manual
```

### 0.4 起服务（**新开一个终端，保持运行**）

```sh
# 终端 A
make build-server >/dev/null

# 迁移
KNOT_PG_DSN='postgres://postgres@/knot_manual?host=/run/postgresql&sslmode=disable' \
  ./bin/knot-server-linux-amd64 migrate

# 起服务
KNOT_PG_DSN='postgres://postgres@/knot_manual?host=/run/postgresql&sslmode=disable' \
KNOT_TOKEN=manual-test-token \
KNOT_LISTEN=127.0.0.1:8080 \
  ./bin/knot-server-linux-amd64
```

> **为什么这里用这么长的连接串？** 服务端是程序，要一份明确的 DSN。
> `postgres://postgres@/knot_manual?host=/run/postgresql&sslmode=disable` 的意思是：
> 用户 `postgres`、库 `knot_manual`、走本机 Unix socket（不经过 TCP）、不开 SSL。
> 你在终端里手动操作时不用它，`psql -U postgres -d knot_manual` 是同一件事的简写。
>
> 如果以后改成 TCP + 密码认证，DSN 换成
> `postgres://postgres:密码@127.0.0.1:5432/knot_manual?sslmode=disable` 即可。

> 如果你更习惯 `go run ./cmd/knot-server` 也行，只要环境变量一致。
> 如果 PG 不是 peer 认证，把 DSN 换成你自己的。

**验收点**

- [ ] 终端 A 打出 `knot-server listening addr=127.0.0.1:8080`
- [ ] `./bin/knot-server-linux-amd64 migrate` 输出「PostgreSQL 迁移完成」
- [ ] 另开终端：`curl -s http://127.0.0.1:8080/healthz` 返回 `{"status":"ok"}`

---

## 1. CLI 基础流程（15 分钟）

### 1.1 新建

```sh
./bin/knot new "第一篇笔记" --tags go,笔记 --body "# 你好

这是**正文**，含 \`代码\` 和 [链接](https://example.com)。"
```

**看什么**

- [ ] 输出是 `<uuid>  第一篇笔记`，uuid 是标准 36 位
- [ ] `knot list` 里能看到它，带 `#go #笔记`
- [ ] `knot show 第一篇笔记` 输出标题、标签、可见性、版本、正文
- [ ] 进数据库亲眼确认（可选，但能建立「CLI 真在写库」的实感）：

      ```sh
      psql -U postgres -d knot_manual -c "select id, title, visibility, version from notes"
      psql -U postgres -d knot_manual -c "select * from tags"
      ```


### 1.2 用真编辑器写正文

```sh
./bin/knot edit 第一篇笔记
```

**看什么（这一节最关键，自动化测不了）**

- [ ] nvim 正常打开，能正常输入中文（没有乱码/方块）
- [ ] 文件内容就是刚才的 Markdown
- [ ] 什么都不改直接 `:wq` → 输出「没有变化，未提交」
- [ ] 再 `edit` 一次，只在行尾敲几个空格再 `:wq` → 仍提示「没有变化，未提交」
- [ ] 再 `edit` 一次，真的加一段文字 → 输出「已保存 …（版本 2）」
- [ ] `knot show 第一篇笔记` 能看到新加的文字

> 若第 3、4 条失败（空白差异被当成修改），说明归一化有漏。

### 1.3 编辑器异常

```sh
# 临时把 editor 改成一个会失败的命令
sed -i 's|command = "nvim"|command = "false"|' ~/.config/knot/config.toml
./bin/knot edit 第一篇笔记; echo "退出码=$?"
```

**看什么**

- [ ] 退出码非 0
- [ ] 错误信息里有「内容保留在 /tmp/knot-edit-xxxx.md」
- [ ] 那个临时文件**真的存在**，里面是完整正文（用 `cat` 看一眼）
- [ ] 改回 `nvim`：`sed -i 's|command = "false"|command = "nvim"|' ~/.config/knot/config.toml`

### 1.4 标签

```sh
./bin/knot tag add 第一篇笔记 rust
./bin/knot tag list
./bin/knot tag rename rust rustlang
./bin/knot tag rm 第一篇笔记 rustlang
./bin/knot list --tag go
```

**看什么**

- [ ] `tag add` 后 `show` 里出现 `rust`
- [ ] `tag rename` 后全局标签列表里变成 `rustlang`
- [ ] `tag rm` 后 `show` 里不再有
- [ ] `list --tag go` 只列出带 go 的

### 1.5 搜索

```sh
./bin/knot search 正文
./bin/knot search "" --tag go
./bin/knot search 不存在的词
```

**看什么**

- [ ] 第一条命中「第一篇笔记」
- [ ] 不存在的词显示「没有匹配的笔记」而不是报错
- [ ] `search`（不带参数）给出明确提示而非静默

### 1.6 删除

```sh
./bin/knot new "待删除" --body "x"
./bin/knot rm 待删除        # 会问你一二次确认
```

**看什么**

- [ ] 问「删除 <uuid>「待删除」？[y/N]」，输入 `n` 后显示「已取消」
- [ ] 再来一次输入 `y` → 「已删除 …」
- [ ] `knot rm -y xxx` 跳过确认
- [ ] 删完 `knot list` 里没有了
- [ ] 对同一个再删一次（UUID）→ 仍返回 204 语义，不报错

**1.x 通过标准**：以上全绿，且全程没有任何一次让你「猜它到底做了什么」。

---

## 2. TUI（20 分钟）

```sh
./bin/knot tui
```

> 必须在真实终端里跑，不要在 IDE 的内嵌终端里（有的会吞按键）。

### 2.1 布局

- [ ] 120 列：左边列表、右边详情、中间竖线、底部状态栏
- [ ] 把终端拉窄到 < 100 列：自动变单栏（只剩详情，顶部一行标题）
- [ ] 再拉宽：恢复双栏
- [ ] 中文标题不撑破边框、不出现错位的半个字

### 2.2 导航

> 先在 CLI 里多造几篇：`for i in $(seq 1 8); do ./bin/knot new "测试笔记 $i" --tags t$((i%3)); done`

- [ ] `j` / `↓` 逐条下移，右栏正文跟着换
- [ ] `k` / `↑` 逐条上移
- [ ] `g` 跳到第一条，`G` 跳到最后一条
- [ ] 快速连按 `j` 十几次：**不会**显示成别的笔记的正文（正文错配是重点检查项）
- [ ] 列表超出屏幕时能滚动，光标始终可见

### 2.3 标签面板

- [ ] `t` 打开标签面板，列出所有标签
- [ ] `j`/`k` 在标签间移动
- [ ] 回车 → 列表只剩该标签的笔记，状态栏显示当前筛选（如 `[go]`）
- [ ] `Esc` 关闭面板；再按 `Esc` 清除筛选，列表恢复全部

### 2.4 搜索

- [ ] `/` 出现输入框，能输入中文
- [ ] 回车 → 列表按标题过滤
- [ ] `Esc` 取消搜索恢复全部

### 2.5 同步

- [ ] 在**另一个终端**里 `./bin/knot new "外部新增"`（会写服务端）
- [ ] TUI 里按 `s` → 状态栏先显示「同步中…」，后显示「已同步：写入 1」
- [ ] 列表里出现「外部新增」
- [ ] 同步进行中连按 `s` → 只显示「同步进行中…」，不会重复发起

### 2.6 异常与退出

- [ ] 服务端停掉（终端 A 里 Ctrl+C），按 `s` → 状态栏显示同步失败，TUI 不崩
- [ ] 把服务端重新起来，再按 `s` → 恢复
- [ ] `q` 正常退出，终端状态恢复正常（光标回来、没有残留字符）
- [ ] `Ctrl+C` 也能退出

**2.x 通过标准**：全程没有错位、闪烁、卡死；快速操作时不出现「张冠李戴」的正文。

---

## 3. Web 端（20 分钟）

### 3.1 准备数据

```sh
./bin/knot new "公开：Go 并发" --tags go,并发 --visibility public --body "$(cat <<'MD'
# Go 并发模式

## goroutine

```go
go func() {
    fmt.Println("hello")
}()
```

## 表格

| 机制 | 用途 |
| --- | --- |
| channel | 通信 |
| mutex | 互斥 |

## 列表

- 第一项
- 第二项
  - 嵌套项

> 引用块测试

[外链测试](https://go.dev)

**加粗** 与 *斜体* 与 `行内代码`。
MD
)"

./bin/knot new "公开：第二篇" --tags go --visibility public --body "第二篇正文"
./bin/knot new "私密：不该出现" --tags 秘密 --visibility private --body "私密正文"
```

起前端（**再开一个终端，保持运行**）：

```sh
# 终端 C
make web-dev
```

打开 `http://localhost:5173`。

### 3.2 列表页

- [ ] 只见两篇「公开」，**看不到**「私密：不该出现」
- [ ] 标签条显示 `全部 / go / 并发`，**没有** `秘密`
- [ ] 每张卡片是「标题 + 日期 + 标签」，没有摘要
- [ ] 鼠标悬停卡片：边框变深，**没有**位移或放大
- [ ] 点标签 `go` → URL 变成 `/?tag=go`，列表只剩带 go 的
- [ ] 点 `全部` → 恢复
- [ ] **刷新页面**（F5）→ 筛选条件保持（因为写进了 URL）
- [ ] 直接访问 `http://localhost:5173/?tag=不存在的标签` → 显示「标签「…」下还没有公开笔记」

### 3.3 阅读页

- [ ] 点标题进入 `/notes/<uuid>`
- [ ] 标题、日期、标签都在
- [ ] 正文最大宽度 760px，宽屏下居中，**没有**卡片边框
- [ ] h1/h2/h3 层级清晰，标题上方留白明显大于下方
- [ ] 代码块有背景色、能横向滚动（不会把布局撑破）
- [ ] 表格有边框、能横向滚动
- [ ] 嵌套列表缩进正确
- [ ] 引用块有左侧竖线
- [ ] 外链点击在新标签页打开
- [ ] 底部标签区点击 → 回到列表并带上筛选
- [ ] 「← 返回列表」可用
- [ ] 浏览器地址栏直接粘一个存在的 `/notes/<uuid>` 刷新 → 正常显示（SPA fallback）

### 3.4 404 与异常

- [ ] 访问 `http://localhost:5173/notes/00000000-0000-0000-0000-000000000000` → 显示「笔记不存在或未公开」，有「回列表」
- [ ] 访问私密笔记的 UUID → 同样是 404 页（不能泄露它存在）
- [ ] 访问 `/asdf/gh` → 404 页
- [ ] 停掉终端 A 的服务端，刷新列表页 → 显示「网络连接失败…」+ 重试按钮，**不是白屏**
- [ ] 重启服务端，点「重试」→ 恢复

### 3.5 暗色模式

- [ ] 系统切到暗色（或浏览器 DevTools 里模拟 `prefers-color-scheme: dark`）
- [ ] 背景变黑、正文变浅、次要文字变灰，没有刺眼的白块
- [ ] 卡片边框在暗色下可见但不过亮

### 3.6 限流（429）

在终端里连打公开列表接口（TUI/Web 不经过它，但能验证服务端行为）：

```sh
for i in $(seq 1 10); do curl -s -o /dev/null -w '%{http_code} ' "$B/api/public/notes"; done; echo
```

- [ ] 前 5 次 200，之后出现 429
- [ ] `curl -s -D - -o /dev/null "$B/api/public/notes" | grep -i retry-after` 有值
- [ ] 等 3 秒后恢复 200
- [ ] 浏览器里狂按 F5 刷新列表页 → 出现「请求过于频繁，请 N 秒后再试」

### 3.7 字体与中文

- [ ] 中文显示为思源黑体（不是系统默认宋体/楷体）
- [ ] 中英文混排时行高一致、基线对齐
- [ ] 数字和英文是 Inter（跟系统字体能看出差别）
- [ ] 首屏加载没有明显的中文「先方框后变字」跳跃

**3.x 通过标准**：视觉上「像一个成品站」，交互没有诡异跳转，错误态都有出路。

---

## 4. Neovim 插件（15 分钟）

在 `~/.config/nvim/init.lua` 里加入（或临时用 `nvim -u` 测试）：

```lua
vim.opt.runtimepath:prepend("/path/to/knot/nvim")
require("knot").setup()
```

```sh
nvim
```

### 4.1 命令

- [ ] `:KnotList` → 打开笔记列表（没装 telescope 时是 quickfix 窗口）
- [ ] 列表条目形如 `1234abcd  标题  #tag`
- [ ] 回车 → 跳到 `knot://<id>` 缓冲区，内容是只读预览
- [ ] 在预览缓冲区里按 `i` 不能编辑（只读）
- [ ] `:KnotSearch 正文` → quickfix 里出现结果
- [ ] `:KnotTags` → 选择器列出标签，选中后列出该标签下的笔记
- [ ] `:KnotRelated` → 在 `knot://` 缓冲区里直接执行，列出共享标签的其他笔记
- [ ] `:KnotSync` → 提示「knot: 同步完成…」

### 4.2 编辑

- [ ] `:KnotEdit <id>` → 起了一个外部 nvim 进程（默认行为）
- [ ] 编辑保存后回到原 nvim，提示「knot: 编辑流程结束」
- [ ] 编辑期间**原 nvim 不卡死**（可以按 `:` 试一下）

> 如果你装了 telescope，再测一遍 `:KnotList` 应该走 telescope 界面。

**4.x 通过标准**：命令都能用，不阻塞 Neovim，出错时有可读提示。

---

## 5. 同步与缓存（10 分钟）

```sh
./bin/knot list                 # 先看当前条数
./bin/knot sync
./bin/knot sync                 # 第二次
```

- [ ] 第一次有变更摘要或「已是最新」
- [ ] 第二次必是「已是最新，无需变更」

**破坏缓存后重建**

```sh
./bin/knot cache info
./bin/knot cache clear --all
./bin/knot list                 # 应该空了
./bin/knot sync
./bin/knot list                 # 数据回来
```

- [ ] `cache info` 显示笔记数/标签数/绑定数/正文数/最近同步时间
- [ ] `clear --all` 后本地为空
- [ ] `sync` 后完全恢复
- [ ] `cache clear`（不带参数）报错并提示要 `--content` 或 `--all`

**外部改动同步**

在另一个终端直接改数据库：

```sh
psql -d knot_manual -c "update notes set title='数据库改的标题' where title='公开：第二篇'"
./bin/knot sync
./bin/knot list | grep 数据库
```

- [ ] `sync` 报告「更新 1」
- [ ] `list` 显示新标题

**离线场景**（先确保在线 `show` 过一次，让正文进缓存）

- [ ] 停掉服务端，`./bin/knot show 第一篇笔记` → **能显示正文**，
      且 stderr 有一行「⚠ 连不上服务端，显示的是本地缓存」
- [ ] `./bin/knot list` **仍可用**（本地缓存）
- [ ] `./bin/knot tui` 仍能打开列表；选中**已 `show` 过**的笔记时右栏显示缓存正文，
      顶部有一行「（离线：显示本地缓存正文）」，不崩不空白
- [ ] 选中**没 `show` 过**的笔记 → 右栏显示加载失败提示，不崩
- [ ] 找一篇**从没 `show` 过**的笔记，停服务后 `show` → 报错并提示
      「联网后先跑 `knot sync`，再用 `knot show` 打开一次即可缓存正文」
- [ ] 服务端删掉一篇已缓存的笔记，再 `show` → 提示「笔记已在服务端删除」，
      **不会**用旧缓存糊弄你

> 注意：`knot sync` 只拉元数据，**不拉正文**。正文要等第一次 `show`/`edit` 才进缓存。

---

## 6. 错误路径（10 分钟）

逐条确认「报错说人话，且不误导」：

| 操作 | 期望 |
|---|---|
| [ ] `./bin/knot show 不存在的` | 提示没有匹配，并建议先 `knot sync` |
| [ ] `./bin/knot show`（不带参数） | 提示缺少 selector |
| [ ] `./bin/knot new`（不带标题） | 提示缺标题，并给出两种写法 |
| [ ] `./bin/knot new x --visibility secret` | 提示可选 private / public |
| [ ] `KNOT_CONFIG=/dev/null ./bin/knot list` | 明确提示配置读取问题（不是 panic） |
| [ ] 把 token 改成错的，`./bin/knot list` | 401 相关提示，不是空白 |
| [ ] `./bin/knot tui </dev/null` | 提示需要交互式终端 |
| [ ] `./bin/knot edit` 时的冲突（见下） | 提示版本冲突并保留文件 |

**冲突场景**（重要）：

```sh
# 终端 1：用会修改服务端的假编辑器触发并发
ID=$(./bin/knot list --json | python3 -c 'import json,sys;print(json.load(sys.stdin)[0]["id"])')
./bin/knot edit "$ID" &
# 在编辑器打开期间，立刻在另一个终端直接 PUT 改这篇
```

- [ ] 保存时提示「服务端已变更（版本冲突）…本次内容保留在 …」
- [ ] 服务端正文**没有被覆盖**
- [ ] 临时文件还在，内容是你改的

---

## 7. 验收清单汇总

做完后回来对一遍：

| 区块 | 通过标准 |
|---|---|
| 0 准备 | 服务、迁移、healthz 都正常 |
| 1 CLI | 全程输出明确，编辑三种情况（无变化/空白差异/真修改）行为正确 |
| 2 TUI | 无错位、无错配、窄/宽自动切换、异常不崩 |
| 3 Web | 视觉成品感、URL 可分享、三态齐全、429 有提示 |
| 4 nvim | 命令可用、不阻塞 |
| 5 同步 | 缓存可清可重建、离线可用 |
| 6 错误 | 每条报错都说人话 |

任何一项不满足，按「现象 + 复现步骤 + 期望 vs 实际」记下来。

---

## 附：环境清理

```sh
# 停服务：终端 A Ctrl+C，终端 C Ctrl+C
psql -U postgres -d postgres -c 'DROP DATABASE IF EXISTS knot_manual'
rm -rf /tmp/knot-manual
rm -f ~/.config/knot/config.toml        # 如果这个配置是专为测试建的
```

## 附：再跑一遍自动化

手测发现的问题修完后，回归：

```sh
make check        # 单测 + lint
make e2e          # 真实 PG 端到端（119 项）
make e2e-web KNOT_WEB_E2E_BASE=http://127.0.0.1:8080   # 前端连真实后端
nvim -l nvim/tests/run.lua                             # 插件自检
```
