std = "luajit"
globals = { "G_reader_settings" }
max_line_length = false
-- `__` is the throwaway loop variable (`_` is the translation function)
ignore = { "212/self", "213/__", "213/___" }
