# knot.nvim

Knot 的 Neovim 胶水插件。**只调用 `knot` CLI，不实现业务逻辑**（见
`docs/ARCHITECTURE.md`）：不直连 HTTP、不读 SQLite，所有数据都来自 CLI 的
`--json` 输出。

## 要求

- Neovim ≥ 0.9（用到 `vim.system`）
- `knot` 可执行文件在 `PATH` 里，或设置 `vim.g.knot_bin` / `$KNOT_BIN`

## 安装

**lazy.nvim（本地目录）**

```lua
{
  dir = "/path/to/knot/nvim",
  event = "VeryLazy",
  config = function()
    require("knot").setup()
  end,
}
```

**手动（把 `lua/` 加进 runtimepath）**

```lua
vim.opt.runtimepath:prepend("/path/to/knot/nvim")
require("knot").setup()
```

## 配置

```lua
require("knot").setup({
  -- knot 可执行文件；nil 时用 $KNOT_BIN 或 "knot"
  bin = nil,
  -- "auto"：有 telescope 用 telescope，否则退回 quickfix
  picker = "auto",
  related_limit = 30,
  -- 设为 false 关闭全部默认键位
  mappings = {
    list   = "<leader>kl",
    search = "<leader>ks",
    tags   = "<leader>kt",
    edit   = "<leader>ke",
  },
})
```

不装 telescope 也能用：面板退回 quickfix 列表，回车跳到对应的
`knot://<selector>` 缓冲区。

## 命令

| 命令 | 说明 |
|---|---|
| `:KnotList` | 本地缓存的笔记列表（秒开，不访问网络） |
| `:KnotSearch [keyword]` | 服务端全文检索；不传关键词时交互输入 |
| `:KnotTags` | 标签面板，选中后列出该标签下的笔记 |
| `:KnotShow [selector]` | 在只读 `markdown` 缓冲区里预览正文 |
| `:KnotEdit [selector]` | 交给 `knot edit`；在 `knot://` 缓冲区里可省略 selector |
| `:KnotNew [title]` | 新建笔记，然后进入编辑 |
| `:KnotSync` | 全量元数据同步 |
| `:KnotRelated [selector]` | 共享任意标签的其他笔记 |

## 关于编辑

插件**不做**临时文件管理：`knot edit` 自己会创建临时文件并启动编辑器
（`[editor].command`）。如果希望复用当前 Neovim 实例而不是再起一个，
把 CLI 配置改成：

```toml
[editor]
command = "nvim"
args = ["--server", "/run/user/1000/nvim.pipe", "--remote-tab"]
```

`--remote-tab` 之前的部分必须是有效的 server 地址；直接写死路径即可，
不需要动态生成。

编辑命令是异步执行的（`vim.system` 不带回调 wait），所以 CLI 等待编辑器
退出时 Neovim 不会卡住。

## 关联笔记是怎么算的

服务端没有「关联笔记」接口，MVP 也不值得加：插件先 `list --json` 找到当前
笔记的标签，再对每个标签 `list --json --tag <name>`，按 id 去重、按
`modified_at` 倒序，取前 `related_limit` 条。全部走本地缓存，不产生网络请求。

## 自检

```sh
nvim -l nvim/tests/run.lua
```

验证插件能加载、命令注册完整、JSON 解析与列表行渲染在真实 CLI 输出格式上
正常。不依赖网络与 Neovim 交互界面。
