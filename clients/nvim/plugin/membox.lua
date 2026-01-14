local ui = require("membox")
vim.api.nvim_create_user_command("Membox", function() ui.open_ui() end, {})
vim.api.nvim_create_user_command("Mm", function() ui.open_ui() end, {})
