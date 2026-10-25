-- knot.nvim —— Knot 的 Neovim 胶水层
--
-- 设计原则（docs/ARCHITECTURE.md）：插件只调用 `knot` CLI，不实现业务逻辑、
-- 不直连 HTTP、不碰 SQLite。所有数据都来自 CLI 的 `--json` 输出。
--
-- 安装（lazy.nvim）：
--   { dir = "/path/to/knot/nvim", ft = "markdown", config = function() require("knot").setup() end }
--
-- 命令：
--   :KnotList            笔记列表（picker）
--   :KnotSearch [keyword] 全文检索（服务端）
--   :KnotTags            标签面板
--   :KnotEdit [selector] 用 knot edit 编辑（交给外部编辑器）
--   :KnotShow [selector] 在缓冲区里只读预览
--   :KnotNew             新建笔记
--   :KnotSync            全量同步
--   :KnotRelated         当前笔记的关联笔记（共享标签）
--
-- 默认快捷键在 setup() 里配置，不与用户键位冲突（默认不映射任何键）。

local M = {}

-- 默认配置
local defaults = {
  -- knot 可执行文件；nil 时用 $KNOT_BIN 或 "knot"
  bin = nil,
  -- 打开 picker 的方式："auto"（有 telescope 用 telescope，否则 quickfix）
  picker = "auto",
  -- 关联笔记面板最多展示多少条
  related_limit = 30,
  -- 键位映射；设为 false 可关闭全部
  mappings = {
    list = "<leader>kl",
    search = "<leader>ks",
    tags = "<leader>kt",
    edit = "<leader>ke",
  },
}

local config = vim.deepcopy(defaults)

-- setup 保存配置并注册命令。重复调用是安全的（只覆盖配置）。
function M.setup(opts)
  config = vim.tbl_deep_extend("force", vim.deepcopy(defaults), opts or {})
  M._register_commands()
  if config.mappings and config.mappings ~= false then
    M._register_mappings()
  end
  return M
end

-- bin 返回可执行文件路径。
local function bin()
  if config.bin and config.bin ~= "" then
    return config.bin
  end
  local env = vim.env.KNOT_BIN
  if env and env ~= "" then
    return env
  end
  return "knot"
end

-- run 执行 knot 并在完成后回调 (stdout | nil)。
--
-- 用同步 wait 而不是 wait(timeout, cb)：`nvim -l` 与部分脚本入口不跑主循环，
-- 带回调的异步 wait 在那里永远不会触发。knot 命令都是毫秒级（本地缓存）
-- 或一次 HTTP（search/sync），同步阻塞对交互影响可以忽略。
-- UI 操作通过 vim.schedule 回到主循环执行，避免 "not allowed in fast event" 类错误。
local function run(args, on_done)
  local cmd = { bin() }
  vim.list_extend(cmd, args)
  local ok, sys = pcall(vim.system, cmd, { text = true })
  if not ok then
    vim.schedule(function()
      vim.notify("knot.nvim: 无法执行 " .. table.concat(cmd, " ") .. ": " .. tostring(sys), vim.log.levels.ERROR)
    end)
    if on_done then on_done(nil) end
    return
  end
  local obj = sys:wait(30000)
  if obj.code ~= 0 then
    local msg = (obj.stderr or ""):gsub("%s+$", "")
    if msg == "" then
      msg = "knot 退出码 " .. tostring(obj.code)
    end
    vim.schedule(function()
      vim.notify(msg, vim.log.levels.ERROR)
    end)
    if on_done then on_done(nil) end
    return
  end
  if on_done then on_done(obj.stdout or "") end
end

-- run_json 执行 knot 并解析 JSON；失败时回调 nil。
local function run_json(args, on_done)
  run(args, function(out)
    if out == nil then
      on_done(nil)
      return
    end
    local ok, data = pcall(vim.json.decode, out)
    if not ok then
      vim.notify("knot.nvim: 解析 JSON 失败: " .. tostring(data), vim.log.levels.ERROR)
      on_done(nil)
      return
    end
    on_done(data)
  end)
end

-- current_selector 返回当前缓冲区对应的笔记 selector（仅 knot:// 缓冲区）。
local function current_selector()
  local name = vim.api.nvim_buf_get_name(0)
  return name:match("^knot://(.+)$")
end

-- item_text 把笔记渲染成 picker 的一行：短 id + 标题 + 标签。
local function item_text(note)
  local short_id = (note.id or ""):sub(1, 8)
  local line = short_id .. "  " .. (note.title or "(无标题)")
  if note.tags and #note.tags > 0 then
    line = line .. "  #" .. table.concat(note.tags, " #")
  end
  return line
end

-- has_telescope 检测 telescope 是否可用（不强制依赖）。
local function has_telescope()
  return (pcall(require, "telescope"))
end

-- open_picker 按配置选择展示方式。
--
-- quickfix 是零依赖兜底：任何 Neovim 都有，选中后回车跳转由 ftplugin 处理。
local function open_picker(notes, title)
  if #notes == 0 then
    vim.notify("knot: 没有结果", vim.log.levels.INFO)
    return
  end

  local use_telescope = config.picker == "telescope"
    or (config.picker == "auto" and has_telescope())

  if use_telescope then
    local ok, pickers = pcall(require, "telescope.pickers")
    local finders = ok and require("telescope.finders") or nil
    local conf = require("telescope.config").values
    local actions = require("telescope.actions")
    local action_state = require("telescope.actions.state")
    pickers
      .new({}, {
        prompt_title = title,
        finder = finders.new_table({
          results = notes,
          entry_maker = function(note)
            return {
              value = note,
              display = item_text(note),
              ordinal = (note.title or "") .. " " .. table.concat(note.tags or {}, " "),
            }
          end,
        }),
        sorter = conf.generic_sorter({}),
        attach_mappings = function(prompt_bufnr)
          actions.select_default:replace(function()
            local selection = action_state.get_selected_entry()
            actions.close(prompt_bufnr)
            if selection then
              M.show(selection.value.id)
            end
          end)
          return true
        end,
      })
      :find()
    return
  end

  -- quickfix 兜底：用文件名承载 id，跳转时由 BufReadCmd 拉正文。
  local items = {}
  for _, note in ipairs(notes) do
    table.insert(items, {
      filename = "knot://" .. tostring(note.id),
      text = item_text(note),
      idx = 0,
    })
  end
  vim.fn.setqflist({}, " ", { title = title, items = items })
  vim.cmd("copen")
end

-- ---------------- 公开命令实现 ----------------

-- M.list 列出本地缓存的笔记（秒开）。
function M.list()
  run_json({ "list", "--json" }, function(notes)
    if notes then
      open_picker(notes, "Knot · 笔记")
    end
  end)
end

-- M.search 服务端全文检索；无关键词时交互输入。
function M.search(keyword)
  local function do_search(kw)
    if not kw or kw == "" then
      return
    end
    run_json({ "search", kw, "--json", "--limit", "50" }, function(res)
      if not res then
        return
      end
      open_picker(res.notes or {}, "Knot · 搜索 " .. kw)
    end)
  end

  if keyword and keyword ~= "" then
    do_search(keyword)
    return
  end
  vim.ui.input({ prompt = "搜索笔记: " }, do_search)
end

-- M.tags 标签面板；选中后列出该标签下的笔记。
function M.tags()
  run_json({ "tag", "list", "--json" }, function(tags)
    if not tags then
      return
    end
    local names = {}
    for _, t in ipairs(tags) do
      table.insert(names, t.name)
    end
    if #names == 0 then
      vim.notify("knot: 没有标签", vim.log.levels.INFO)
      return
    end
    vim.ui.select(names, { prompt = "Knot · 标签" }, function(choice)
      if not choice then
        return
      end
      run_json({ "list", "--json", "--tag", choice }, function(notes)
        if notes then
          open_picker(notes, "Knot · #" .. choice)
        end
      end)
    end)
  end)
end

-- M.show 在只读缓冲区里预览笔记正文。
function M.show(selector)
  if not selector or selector == "" then
    vim.notify("knot: 需要 selector", vim.log.levels.WARN)
    return
  end
  run({ "show", selector }, function(out)
    if out == nil then
      return
    end
    vim.cmd("enew")
    local buf = vim.api.nvim_get_current_buf()
    vim.bo[buf].buftype = "nofile"
    vim.bo[buf].bufhidden = "wipe"
    vim.bo[buf].filetype = "markdown"
    vim.bo[buf].modifiable = true
    vim.api.nvim_buf_set_lines(buf, 0, -1, false, vim.split(out:gsub("\n$", ""), "\n", { plain = true }))
    vim.bo[buf].modifiable = false
    vim.bo[buf].readonly = true
    vim.api.nvim_buf_set_name(buf, "knot://" .. selector)
  end)
end

-- M.edit 交给 knot edit（临时文件与编辑器生命周期由 CLI 负责）。
--
-- 插件不做临时文件管理：knot edit 会起自己的编辑器进程。若想复用当前
-- Neovim 实例，把 CLI 配置改成：
--   [editor]
--   command = "nvim"
--   args = ["--server", "/path/to/server.pipe", "--remote-tab"]
--
-- 这里用异步执行：CLI 内部要等编辑器退出，同步等待会把 Neovim 卡死。
function M.edit(selector)
  selector = selector or current_selector()
  if not selector or selector == "" then
    vim.notify("knot: 需要 selector（或在 knot:// 缓冲区里执行）", vim.log.levels.WARN)
    return
  end
  local cmd = { bin(), "edit", selector }
  local ok, err = pcall(vim.system, cmd, { text = true }, function(obj)
    vim.schedule(function()
      if obj.code ~= 0 then
        local msg = (obj.stderr or ""):gsub("%s+$", "")
        vim.notify("knot edit 失败: " .. (msg ~= "" and msg or ("退出码 " .. tostring(obj.code))), vim.log.levels.ERROR)
      else
        vim.notify("knot: 编辑流程结束", vim.log.levels.INFO)
      end
    end)
  end)
  if not ok then
    vim.notify("knot.nvim: 无法执行: " .. tostring(err), vim.log.levels.ERROR)
  end
end

-- M.new_note 新建笔记：先问标题，再进 knot edit。
function M.new_note(title)
  local function do_new(t)
    if not t or t == "" then
      return
    end
    run({ "new", t }, function(out)
      if out == nil then
        return
      end
      local id = out:match("^(%x%x%x%x%x%x%x%x%-%x%x%x%x%-%x%x%x%x%-%x%x%x%x%-%x%x%x%x%x%x%x%x%x%x%x%x)")
      if id then
        vim.notify("knot: 已创建 " .. id .. "，可用 :KnotEdit 撰写正文", vim.log.levels.INFO)
        M.edit(id)
      end
    end)
  end

  if title and title ~= "" then
    do_new(title)
    return
  end
  vim.ui.input({ prompt = "笔记标题: " }, do_new)
end

-- M.sync 全量同步。
function M.sync()
  run({ "sync" }, function(out)
    if out then
      vim.notify("knot: " .. (out:gsub("%s+$", "")), vim.log.levels.INFO)
    end
  end)
end

-- M.related 当前笔记的关联笔记：共享任意标签的其他笔记。
--
-- 关联关系在客户端计算（服务端没有该接口，也不值得为 MVP 加）：
-- 先用 list 找到当前笔记拿到标签，再对每个标签 list --tag，按 id 去重。
function M.related(selector)
  local function compute(kw)
    -- list 是本地的、能按标题子串匹配；条目里带 tags。
    run_json({ "list", "--json" }, function(all)
      if not all then
        return
      end
      local me
      for _, n in ipairs(all) do
        if n.id == kw or (n.title or ""):find(kw, 1, true) then
          me = n
          break
        end
      end
      if not me then
        vim.notify("knot: 找不到当前笔记（先 :KnotSync？）", vim.log.levels.WARN)
        return
      end
      local tags = me.tags or {}
      if #tags == 0 then
        vim.notify("knot: 当前笔记没有标签，无法计算关联", vim.log.levels.INFO)
        return
      end

      local seen = { [me.id] = true }
      local merged = {}
      local pending = #tags
      for _, tag in ipairs(tags) do
        run_json({ "list", "--json", "--tag", tag }, function(notes)
          for _, n in ipairs(notes or {}) do
            if not seen[n.id] then
              seen[n.id] = true
              table.insert(merged, n)
            end
          end
          pending = pending - 1
          if pending == 0 then
            table.sort(merged, function(a, b)
              return (a.modified_at or "") > (b.modified_at or "")
            end)
            if #merged > config.related_limit then
              merged = vim.list_slice(merged, 1, config.related_limit)
            end
            open_picker(merged, "Knot · 关联笔记")
          end
        end)
      end
    end)
  end

  if selector and selector ~= "" then
    compute(selector)
    return
  end
  local from_buf = current_selector()
  if from_buf and from_buf ~= "" then
    compute(from_buf)
    return
  end
  vim.ui.input({ prompt = "当前笔记标题或 id: " }, function(kw)
    if kw and kw ~= "" then
      compute(kw)
    end
  end)
end

-- ---------------- 供测试使用的内部访问器 ----------------
--
-- headless 自检需要验证配置合并与渲染辅助函数；这些不是公开 API，
-- 生产代码不要依赖。

function M._config()
  return config
end

function M._item_text(note)
  return item_text(note)
end

-- _current_selector_of 读取指定缓冲区的 knot:// selector（测试用）。
function M._current_selector_of(bufnr)
  local name = vim.api.nvim_buf_get_name(bufnr)
  return name:match("^knot://(.+)$")
end

-- ---------------- 命令与键位 ----------------

local commands = {
  KnotList = { M.list, { desc = "Knot: 笔记列表" } },
  KnotSearch = { function(args) M.search(args) end, { desc = "Knot: 全文检索", nargs = 1 } },
  KnotTags = { M.tags, { desc = "Knot: 标签面板" } },
  KnotEdit = { function(args) M.edit(args) end, { desc = "Knot: 编辑笔记", nargs = 1 } },
  KnotShow = { function(args) M.show(args) end, { desc = "Knot: 预览笔记", nargs = 1 } },
  KnotNew = { function(args) M.new_note(args) end, { desc = "Knot: 新建笔记", nargs = 1 } },
  KnotSync = { M.sync, { desc = "Knot: 全量同步" } },
  KnotRelated = { function(args) M.related(args) end, { desc = "Knot: 关联笔记", nargs = 1 } },
}

function M._register_commands()
  for name, def in pairs(commands) do
    vim.api.nvim_create_user_command(name, function(opts)
      def[1](opts.args ~= "" and opts.args or nil)
    end, def[2])
  end
end

function M._register_mappings()
  local m = config.mappings
  local function map(lhs, rhs, desc)
    if lhs and lhs ~= "" then
      vim.keymap.set("n", lhs, rhs, { desc = desc, silent = true })
    end
  end
  map(m.list, function() M.list() end, "Knot: 列表")
  map(m.search, function() M.search() end, "Knot: 搜索")
  map(m.tags, function() M.tags() end, "Knot: 标签")
  map(m.edit, function() M.edit() end, "Knot: 编辑当前/选中笔记")
end

return M
