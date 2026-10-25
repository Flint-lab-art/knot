-- nvim/tests/run.lua —— headless 自检（CI / 本地手测）
--
-- 用法：
--   nvim -l nvim/tests/run.lua
--
-- 不依赖网络：只验证插件能加载、命令注册正确、JSON 解析与渲染辅助函数
-- 在真实 knot 输出上的行为（用 fixture 字符串，不起进程）。

local function fail(msg)
  io.stderr:write("FAIL: " .. msg .. "\n")
  os.exit(1)
end

local function assert_eq(got, want, what)
  if got ~= want then
    fail(("%s: got %q, want %q"):format(what, tostring(got), tostring(want)))
  end
end

-- 让 `require("knot")` 能找到 lua/ 目录。
local src = debug.getinfo(1, "S").source:sub(2)
local root = src:match("^(.*)/tests/run%.lua$")
if root == "" or root == nil then
  -- 用相对路径运行时 src 形如 "tests/run.lua"，根目录就是当前工作目录。
  if src == "tests/run.lua" or src == "./tests/run.lua" then
    root = "."
  else
    fail("无法推断插件根目录: " .. src)
  end
end
vim.opt.runtimepath:prepend(root)

local knot = require("knot")

-- 1) setup 注册命令
knot.setup({ mappings = false })
local expect_cmds = {
  "KnotList", "KnotSearch", "KnotTags", "KnotEdit",
  "KnotShow", "KnotNew", "KnotSync", "KnotRelated",
}
for _, name in ipairs(expect_cmds) do
  local ok = pcall(vim.api.nvim_get_command, name)
  -- nvim_get_command 不存在于所有版本，改用 getcompletion
end
local listed = vim.fn.getcompletion("Knot", "command")
for _, name in ipairs(expect_cmds) do
  local found = false
  for _, got in ipairs(listed) do
    if got == name then found = true end
  end
  if not found then
    fail("缺少命令 :" .. name .. "（已注册：" .. table.concat(listed, ", ") .. "）")
  end
end

-- 2) 配置合并：默认值保留、用户值覆盖
knot.setup({ related_limit = 5, mappings = false })
assert_eq(knot._config().related_limit, 5, "related_limit 覆盖")
assert_eq(knot._config().picker, "auto", "默认 picker 保留")

-- 3) 真实 knot list --json 输出的解析（fixture 与 CLI 实际格式一致）
local fixture = [==[
[
  {"id":"11111111-1111-1111-1111-111111111111","title":"笔记 A","version":1,
   "visibility":"private","created_at":"2026-10-24T10:00:00Z",
   "modified_at":"2026-10-24T10:00:00Z","tags":["go","测试"]},
  {"id":"22222222-2222-2222-2222-222222222222","title":"笔记 B","version":2,
   "visibility":"public","created_at":"2026-10-23T10:00:00Z",
   "modified_at":"2026-10-23T10:00:00Z","tags":[]}
]
]==]
local notes = vim.json.decode(fixture)
assert_eq(#notes, 2, "解析笔记数")

-- 4) 列表行渲染
assert_eq(knot._item_text(notes[1]), "11111111  笔记 A  #go #测试", "item_text 带标签")
assert_eq(knot._item_text(notes[2]), "22222222  笔记 B", "item_text 无标签")

-- 5) 搜索响应形状（{notes:[...]}）
local search_fixture = [==[{"notes":[{"id":"33333333-3333-3333-3333-333333333333","title":"命中","tags":[]}]}]==]
local res = vim.json.decode(search_fixture)
assert_eq(#res.notes, 1, "解析搜索结果")
assert_eq(res.notes[1].title, "命中", "搜索结果标题")

-- 6) 标签响应形状（数组）
local tags_fixture = [==[
[{"id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa","name":"go"}]
]==]
local tags = vim.json.decode(tags_fixture)
assert_eq(tags[1].name, "go", "解析标签")

-- 7) current_selector 只认 knot:// 缓冲区
local buf = vim.api.nvim_create_buf(false, true)
vim.api.nvim_buf_set_name(buf, "knot://my-selector")
assert_eq(knot._current_selector_of(buf), "my-selector", "knot:// selector")
local plain = vim.api.nvim_create_buf(false, true)
vim.api.nvim_buf_set_name(plain, "/tmp/notes.md")
assert_eq(knot._current_selector_of(plain), nil, "普通文件无 selector")

print("✓ nvim 插件自检通过")
