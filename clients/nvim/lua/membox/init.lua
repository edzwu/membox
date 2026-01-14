local M = {}

local state = {
  last_line = "task ls",
  row_map = {},

  buf_prompt = nil,
  buf_result = nil,
  win_prompt = nil,
  win_result = nil,

  old_laststatus = nil,
  tab = nil,
}

local function trim(s)
  s = tostring(s or "")
  s = s:gsub("^%s+", ""):gsub("%s+$", "")
  return s
end

local function err(msg)
  vim.notify("membox: " .. tostring(msg), vim.log.levels.ERROR)
end

local function decode_json(s)
  return vim.fn.json_decode(s)
end

-- 建议：用 memboxd（更稳），不绑 python 环境
-- 你也可以在 init.lua 里设置：
--   vim.g.membox_core_cmd = "/ABS/PATH/.venv/bin/memboxd"
local function run_core(line, cb)
  state.last_line = line
  local input = vim.fn.json_encode({ line = line })

  local core = vim.g.membox_core_cmd or "python3"
  local cmd
  if core == "python3" then
    cmd = { "python3", "-m", "membox.apps.memboxd", "--stdin" }
  else
    cmd = { core, "--stdin" }
  end

  if vim.system then
    vim.system(cmd, { stdin = input, text = true }, function(obj)
      cb(obj.stdout or "")
    end)
  else
    cb(vim.fn.system(cmd, input))
  end
end

local function pad(s, w)
  s = tostring(s or "")
  if #s >= w then return s:sub(1, w) end
  return s .. string.rep(" ", w - #s)
end

local function render_table(env)
  local view = env.view
  local cols = view.columns or {}
  local rows = view.rows or {}

  local widths = {}
  for i, c in ipairs(cols) do
    widths[i] = c.width or math.max(#c.label, 6)
  end

  local lines = {}
  local header = {}
  for i, c in ipairs(cols) do
    table.insert(header, pad(c.label, widths[i]))
  end
  table.insert(lines, table.concat(header, "  "))

  local sep = {}
  for i, w in ipairs(widths) do
    table.insert(sep, string.rep("-", w))
  end
  table.insert(lines, table.concat(sep, "  "))

  state.row_map = {}
  for idx, r in ipairs(rows) do
    local parts = {}
    for i, c in ipairs(cols) do
      table.insert(parts, pad(r[c.key] or "", widths[i]))
    end
    table.insert(lines, table.concat(parts, "  "))
    state.row_map[idx + 2] = r -- data starts at line 3
  end

  vim.bo[state.buf_result].modifiable = true
  vim.api.nvim_buf_set_lines(state.buf_result, 0, -1, false, lines)
  vim.bo[state.buf_result].modifiable = false
end

local function handle_output(out)
  local ok, env = pcall(decode_json, out)
  if not ok then return err("bad json: " .. tostring(out)) end
  if not env.ok then return err(env.error or "error") end

  if env.view and env.view.type == "table" then
    render_table(env)
  else
    err("unsupported view")
  end
end

local function exec_line(line)
  run_core(line, function(out)
    handle_output(out)
    vim.cmd("startinsert")
  end)
end

local function refresh()
  exec_line(state.last_line)
end

local function mark_done()
  if not (state.win_result and vim.api.nvim_win_is_valid(state.win_result)) then return end
  local lnum = vim.api.nvim_win_get_cursor(state.win_result)[1]
  local row = state.row_map[lnum]
  if not row or not row.id then return end
  run_core("task done " .. tostring(row.id), function(_)
    refresh()
  end)
end

local function close_ui()
  -- restore laststatus
  if state.old_laststatus ~= nil then
    vim.o.laststatus = state.old_laststatus
  end

  -- prefer closing the UI tabpage
  if state.tab and vim.api.nvim_tabpage_is_valid(state.tab) then
    local tabs = vim.api.nvim_list_tabpages()
    if #tabs > 1 then
      pcall(vim.api.nvim_set_current_tabpage, state.tab)
      pcall(vim.cmd, "tabclose")
    end
  end

  -- hard fallback: close windows if still around
  if state.win_prompt and vim.api.nvim_win_is_valid(state.win_prompt) then
    pcall(vim.api.nvim_win_close, state.win_prompt, true)
  end
  if state.win_result and vim.api.nvim_win_is_valid(state.win_result) then
    pcall(vim.api.nvim_win_close, state.win_result, true)
  end

  -- reset state (avoid using stale handles)
  state.win_prompt, state.win_result = nil, nil
  state.buf_prompt, state.buf_result = nil, nil
  state.tab = nil
end

function M.open_ui()
  vim.cmd("tabnew")
  state.tab = vim.api.nvim_get_current_tabpage()

  state.old_laststatus = vim.o.laststatus
  vim.o.laststatus = 3

  -- TOP: result window
  state.win_result = vim.api.nvim_get_current_win()
  state.buf_result = vim.api.nvim_create_buf(false, true)

  -- ✅ result 不作为“标签标题”，避免 tabline/bufferline 用它
  vim.api.nvim_buf_set_name(state.buf_result, "membox://panel")
  vim.bo[state.buf_result].buflisted = false

  vim.api.nvim_win_set_buf(state.win_result, state.buf_result)

  vim.bo[state.buf_result].buftype = "nofile"
  vim.bo[state.buf_result].bufhidden = "wipe"
  vim.bo[state.buf_result].swapfile = false
  vim.bo[state.buf_result].modifiable = false
  vim.wo[state.win_result].cursorline = true

  -- BOTTOM: prompt window
  vim.cmd("botright split")
  vim.cmd("resize 1")
  state.win_prompt = vim.api.nvim_get_current_win()
  vim.wo[state.win_prompt].winfixheight = true

  state.buf_prompt = vim.api.nvim_create_buf(false, true)

  -- ✅ 关键：prompt 是当前窗口，所以把它命名为 membox
  vim.api.nvim_buf_set_name(state.buf_prompt, "membox")
  vim.bo[state.buf_prompt].buflisted = true

  vim.api.nvim_win_set_buf(state.win_prompt, state.buf_prompt)

  vim.bo[state.buf_prompt].buftype = "prompt"
  vim.bo[state.buf_prompt].bufhidden = "wipe"
  vim.bo[state.buf_prompt].swapfile = false

  vim.fn.prompt_setprompt(state.buf_prompt, "membox> ")
  vim.fn.prompt_setcallback(state.buf_prompt, function(line)
    local prompt = vim.fn.prompt_getprompt(state.buf_prompt) or ""
    line = tostring(line or "")
    if prompt ~= "" and line:sub(1, #prompt) == prompt then
      line = line:sub(#prompt + 1)
    end

    line = trim(line)

    if line == "q" or line == "quit" or line == "exit" then
      close_ui()
      return
    end

    if line == "" then
      line = state.last_line
    end

    exec_line(line)
  end)

  -- keymaps (result)
  vim.keymap.set("n", "d", mark_done, { noremap = true, silent = true, buffer = state.buf_result })
  vim.keymap.set("n", "r", refresh,   { noremap = true, silent = true, buffer = state.buf_result })
  vim.keymap.set("n", "q", close_ui,  { noremap = true, silent = true, buffer = state.buf_result })

  -- keymaps (prompt)
  vim.keymap.set("n", "q", close_ui,      { noremap = true, silent = true, buffer = state.buf_prompt })
  vim.keymap.set("i", "<C-c>", close_ui,  { noremap = true, silent = true, buffer = state.buf_prompt })

  refresh()
  vim.cmd("startinsert")

  -- （可选）强制刷新 tabline/bufferline
  vim.cmd("redrawtabline")
end

return M