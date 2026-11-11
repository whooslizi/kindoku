--[[--
Kinkoku: read Aidoku sources in KOReader. Every action runs the kinkoku binary once and shows
the JSON it prints in a Menu.
--]]

local ButtonDialog = require("ui/widget/buttondialog")
local ConfirmBox = require("ui/widget/confirmbox")
local DataStorage = require("datastorage")
local Device = require("device")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local JSON = require("json")
local LuaSettings = require("luasettings")
local Menu = require("ui/widget/menu")
local NetworkMgr = require("ui/network/manager")
local PathChooser = require("ui/widget/pathchooser")
local TrapWidget = require("ui/widget/trapwidget")
local Trapper = require("ui/trapper")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local logger = require("logger")
local T = require("ffi/util").template
local _ = require("kinkoku_l10n")

local COMMUNITY_REPO = "https://aidoku-community.github.io/sources/"
local ARCHES = { armv6l = "armv6", armv7l = "armv7", armv8l = "armv7", aarch64 = "arm64", arm64 = "arm64", x86_64 = "x64" }
local STATUS = { [0] = "Unknown", "Ongoing", "Completed", "Cancelled", "Hiatus" }

local Kinkoku = WidgetContainer:extend{
    name = "kinkoku",
    is_doc_only = false,
}

local function shell_quote(s)
    return "'" .. tostring(s):gsub("'", "'\\''") .. "'"
end

-- JSON null may decode to a sentinel, not nil
local function list(x)
    return type(x) == "table" and x or {}
end

local function str(x)
    return type(x) == "string" and x or nil
end

local function read_file(path)
    local f = io.open(path, "rb")
    if not f then return nil end
    local s = f:read("*a")
    f:close()
    return s
end

function Kinkoku:init()
    self.data_dir = DataStorage:getSettingsDir() .. "/kinkoku"
    self.settings = LuaSettings:open(DataStorage:getSettingsDir() .. "/kinkoku.lua")
    self.screens = {}
    self.ui.menu:registerToMainMenu(self)
    if self.ui.document then
        self:hookEndOfBook()
    end
end

function Kinkoku:setting(key, default)
    local v = self.settings:readSetting(key)
    if v == nil then return default end
    return v
end

function Kinkoku:save(key, value)
    self.settings:saveSetting(key, value)
    self.settings:flush()
end

function Kinkoku:folder()
    return self:setting("folder", Device:isKindle() and "/mnt/us/manga" or (DataStorage:getDataDir() .. "/manga"))
end

function Kinkoku:binary()
    if self.bin == nil then
        local p = io.popen("uname -m")
        local machine = p and p:read("*l") or ""
        if p then p:close() end
        local arch = ARCHES[machine]
        local path = arch and (self.path .. "/bin/" .. arch .. "/kinkoku")
        if path and read_file(path) then
            os.execute("chmod +x " .. shell_quote(path))
            self.bin = path
        else
            self.bin = false
            self.bin_error = T(_("No kinkoku binary for this device (%1)."), machine)
        end
    end
    return self.bin
end

--[[--
Runs the binary inside a Trapper coroutine. Returns the decoded JSON, or nil and an error.
on_progress(stderr_line) may return a new status text.
--]]
function Kinkoku:run(args, text, on_progress)
    local bin = self:binary()
    if not bin then return nil, self.bin_error end
    -- an update leaves the new binary next to the old one; swap it in while nothing runs it
    local staged = io.open(bin .. ".new", "rb")
    if staged then
        staged:close()
        os.rename(bin .. ".new", bin)
    end
    local pid_file = self.data_dir .. "/run.pid"
    local err_file = self.data_dir .. "/run.log"
    os.execute("mkdir -p " .. shell_quote(self.data_dir))
    local env = { "KINKOKU_DATA=" .. shell_quote(self.data_dir) }
    local lang = G_reader_settings:readSetting("language")
    if lang then table.insert(env, "KINKOKU_LANG=" .. shell_quote(lang:sub(1, 2))) end
    local flare = self:setting("flaresolverr", "")
    if flare ~= "" then table.insert(env, "KINKOKU_FLARESOLVERR=" .. shell_quote(flare)) end
    local quoted = {}
    for __, a in ipairs(args) do table.insert(quoted, shell_quote(a)) end
    -- dismissablePopen doesn't kill on cancel, so run.pid lets us. It also waits for stdout data
    -- forever: the trailing echo guarantees some even if the binary dies without printing.
    local cmd = string.format("env %s %s %s 2> %s & echo $! > %s; wait $!; echo",
        table.concat(env, " "), shell_quote(bin), table.concat(quoted, " "), shell_quote(err_file), shell_quote(pid_file))

    -- An invisible modal trap catches the tap that cancels; the centered box sits below it
    -- (non-modal), so it can be swapped for progress updates without losing the trap.
    local function box(t)
        local w = InfoMessage:new{ text = t, modal = false, dismissable = false }
        UIManager:show(w)
        return w
    end
    local trap = TrapWidget:new{}
    UIManager:show(trap)
    local msg = box(text .. "\n" .. _("Tap to cancel"))
    UIManager:forceRePaint()
    local shown = text
    local ticker
    if on_progress then
        ticker = function()
            local last
            for line in (read_file(err_file) or ""):gmatch("[^\n]+") do
                if line:sub(1, 1) == "{" then last = line end
            end
            local new_text = last and on_progress(last)
            if new_text and new_text ~= shown then
                local next_msg = box(new_text .. "\n" .. _("Tap to cancel"))
                UIManager:close(msg)
                msg, shown = next_msg, new_text
            end
            UIManager:scheduleIn(2, ticker)
        end
        UIManager:scheduleIn(2, ticker)
    end
    local completed, output = Trapper:dismissablePopen(cmd, trap)
    if ticker then UIManager:unschedule(ticker) end
    UIManager:close(msg)
    UIManager:close(trap)
    if not completed then
        local pid = tonumber(read_file(pid_file) or "")
        if pid then os.execute("kill " .. pid) end
        return nil, _("Cancelled.")
    end
    local ok, result = pcall(JSON.decode, output or "")
    if not ok or type(result) ~= "table" then
        logger.warn("kinkoku: bad output", output, read_file(err_file))
        local log = (read_file(err_file) or ""):sub(-300)
        return nil, T(_("Error: %1"), log ~= "" and log or _("kinkoku stopped unexpectedly."))
    end
    if str(result.error) then return nil, result.error end
    return result
end

function Kinkoku:task(fn)
    Trapper:wrap(function()
        local err = fn()
        if err then UIManager:show(InfoMessage:new{ text = err }) end
    end)
end

function Kinkoku:online(fn)
    NetworkMgr:runWhenOnline(function() self:task(fn) end)
end

function Kinkoku:show(title, items, opts)
    opts = opts or {}
    local menu = Menu:new{
        title = title,
        subtitle = opts.subtitle,
        item_table = items,
        covers_fullscreen = true,
        is_borderless = true,
        is_popout = false,
        title_bar_fm_style = true,
        title_bar_left_icon = opts.home and "appbar.menu" or nil,
    }
    if opts.home then
        menu.onLeftButtonTap = function() self:showNav() end
    else
        menu.onReturn = function() UIManager:close(menu) end
    end
    menu.onMenuHold = function(_menu, item)
        if item.hold_callback then item.hold_callback() end
        return true
    end
    menu.onCloseWidget = function(m)
        Menu.onCloseWidget(m)
        for i, s in ipairs(self.screens) do
            if s == m then table.remove(self.screens, i) break end
        end
    end
    table.insert(self.screens, menu)
    UIManager:show(menu)
    return menu
end

function Kinkoku:update(menu, items)
    menu:switchItemTable(nil, items, (menu.page - 1) * menu.perpage + 1)
end

function Kinkoku:closeAll()
    for i = #self.screens, 1, -1 do
        UIManager:close(self.screens[i])
    end
end

function Kinkoku:addToMainMenu(menu_items)
    menu_items.kinkoku = {
        text = _("Kinkoku"),
        sorting_hint = "search",
        callback = function() self:showLibrary() end,
    }
end

function Kinkoku:showNav()
    local dialog
    local function button(text, fn)
        return { { text = text, callback = function() UIManager:close(dialog) fn() end } }
    end
    dialog = ButtonDialog:new{
        shrink_unneeded_width = true,
        buttons = {
            button(_("Search all sources"), function() self:askSearch() end),
            button(_("Sources"), function() self:showSources() end),
            button(_("Downloads"), function() self:showDownloads() end),
            button(_("Settings"), function() self:showSettings() end),
        },
    }
    UIManager:show(dialog)
end

function Kinkoku:library()
    return self:setting("library", {})
end

function Kinkoku:inLibrary(source, key)
    for __, e in ipairs(self:library()) do
        if e.source == source and e.key == key then return true end
    end
    return false
end

function Kinkoku:toggleLibrary(source, source_name, key, title)
    local lib = self:library()
    for i, e in ipairs(lib) do
        if e.source == source and e.key == key then
            table.remove(lib, i)
            self:save("library", lib)
            return false
        end
    end
    table.insert(lib, { source = source, source_name = source_name, key = key, title = title })
    table.sort(lib, function(a, b) return a.title:lower() < b.title:lower() end)
    self:save("library", lib)
    return true
end

function Kinkoku:libraryItems()
    local items = {}
    for __, e in ipairs(self:library()) do
        table.insert(items, {
            text = e.title,
            mandatory = e.source_name,
            callback = function() self:showManga({ id = e.source, name = e.source_name }, e.key) end,
            hold_callback = function()
                UIManager:show(ConfirmBox:new{
                    text = T(_("Remove '%1' from library?"), e.title),
                    ok_text = _("Remove"),
                    ok_callback = function()
                        self:toggleLibrary(e.source, e.source_name, e.key, e.title)
                        self:update(self.library_menu, self:libraryItems())
                    end,
                })
            end,
        })
    end
    if #items == 0 then
        table.insert(items, { text = _("Library is empty. Use the menu (top left) to find manga."), dim = true })
    end
    return items
end

function Kinkoku:showLibrary()
    self:closeAll()
    self.library_menu = self:show(_("Library"), self:libraryItems(), { home = true })
end

function Kinkoku:refreshLibrary()
    if self.library_menu then self:update(self.library_menu, self:libraryItems()) end
end

function Kinkoku:askSearch(src)
    local dialog
    dialog = InputDialog:new{
        title = src and src.name or _("Search all sources"),
        input = self.last_query or "",
        buttons = { {
            { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
            { text = _("Search"), is_enter_default = true, callback = function()
                local q = dialog:getInputText()
                UIManager:close(dialog)
                self.last_query = q
                if src then
                    self:showResults(src, { "search", src.id, q }, q, 1)
                else
                    self:searchAll(q)
                end
            end },
        } },
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function Kinkoku:resultItem(src, m, refresh)
    local starred = self:inLibrary(src.id, m.key)
    return {
        text = (starred and "★ " or "") .. m.title,
        mandatory = src.name,
        callback = function() self:showManga(src, m.key) end,
        hold_callback = function()
            local added = self:toggleLibrary(src.id, src.name, m.key, m.title)
            UIManager:show(InfoMessage:new{
                text = T(added and _("Added '%1' to library") or _("Removed '%1' from library"), m.title),
                timeout = 2,
            })
            self:refreshLibrary()
            refresh()
        end,
    }
end

function Kinkoku:searchAll(q)
    self:online(function()
        local results, err = self:run({ "search-all", q }, _("Searching…"))
        if not results then return err end
        local menu
        local function items()
            local out = {}
            for __, r in ipairs(list(results)) do
                local src = { id = r.source, name = r.name }
                for ___, m in ipairs(list(r.entries)) do
                    table.insert(out, self:resultItem(src, m, function() self:update(menu, items()) end))
                end
                if str(r.error) then
                    table.insert(out, { text = r.error, mandatory = r.name, dim = true })
                end
            end
            if #out == 0 then table.insert(out, { text = _("No results."), dim = true }) end
            return out
        end
        menu = self:show(_("Search all sources"), items(), { subtitle = _("Hold a result to add or remove it from your library") })
    end)
end

function Kinkoku:showResults(src, args, title, page, menu)
    self:online(function()
        local full = { "--page", tostring(page) }
        for __, a in ipairs(args) do table.insert(full, a) end
        local r, err = self:run(full, _("Loading…"))
        if not r then return err end
        local function items()
            local out = {}
            for __, m in ipairs(list(r.entries)) do
                table.insert(out, self:resultItem(src, m, function() self:update(menu, items()) end))
            end
            if #out == 0 then table.insert(out, { text = _("No results."), dim = true }) end
            if r.hasNextPage == true then
                table.insert(out, { text = _("Next page"), callback = function()
                    self:showResults(src, args, title, page + 1, menu)
                end })
            end
            return out
        end
        if menu then
            menu:switchItemTable(nil, items(), 1)
        else
            menu = self:show(title, items(), { subtitle = _("Hold a result to add or remove it from your library") })
        end
    end)
end

function Kinkoku:showSources()
    self:task(function()
        local sources, err = self:run({ "list" }, _("Loading…"))
        if not sources then return err end
        local items = {}
        for __, s in ipairs(list(sources)) do
            if s.supported == true then
                table.insert(items, { text = s.name, mandatory = "v" .. s.version, callback = function() self:showSource(s) end })
            else
                table.insert(items, { text = s.name, mandatory = _("needs JavaScript"), dim = true })
            end
        end
        table.insert(items, { text = _("Install sources"), callback = function() self:showInstall() end })
        table.insert(items, { text = _("Add source list"), callback = function() self:addRepo() end })
        table.insert(items, { text = _("Update sources"), callback = function() self:updateSources() end })
        self:show(_("Sources"), items)
    end)
end

function Kinkoku:addRepo()
    local dialog
    dialog = InputDialog:new{
        title = _("Source list URL"),
        input = COMMUNITY_REPO,
        buttons = { {
            { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
            { text = _("Add"), is_enter_default = true, callback = function()
                local url = dialog:getInputText()
                UIManager:close(dialog)
                self:online(function()
                    local r, err = self:run({ "repo", "add", url }, _("Loading…"))
                    if not r then return err end
                    self:showInstall()
                end)
            end },
        } },
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function Kinkoku:showInstall()
    self:task(function()
        local entries, err = self:run({ "repo", "list" }, _("Loading…"))
        if not entries then return err end
        if #list(entries) == 0 then
            self:addRepo()
            return
        end
        local items = {}
        for __, e in ipairs(list(entries)) do
            local installed = type(e.installed) == "number" and e.installed > 0
            table.insert(items, {
                text = e.name,
                mandatory = (installed and _("installed") .. " · " or "") .. table.concat(list(e.languages), ", "),
                callback = function()
                    self:online(function()
                        local r, ierr = self:run({ "repo", "install", e.id }, _("Loading…"))
                        if not r then return ierr end
                        UIManager:show(InfoMessage:new{ text = T(_("Installed %1."), e.name), timeout = 2 })
                    end)
                end,
            })
        end
        self:show(_("Install sources"), items)
    end)
end

function Kinkoku:updateSources()
    self:online(function()
        local r, err = self:run({ "repo", "update" }, _("Loading…"))
        if not r then return err end
        local msg = #list(r.updated) > 0 and T(_("Updated: %1"), table.concat(r.updated, ", ")) or _("Everything is up to date.")
        if #list(r.errors) > 0 then msg = msg .. "\n" .. table.concat(r.errors, "\n") end
        UIManager:show(InfoMessage:new{ text = msg })
    end)
end

function Kinkoku:showSource(src)
    local items = {
        { text = _("Search"), callback = function() self:askSearch(src) end },
    }
    for __, l in ipairs(list(src.listings)) do
        table.insert(items, { text = l.name, callback = function()
            self:showResults(src, { "listing", src.id, l.id }, l.name, 1)
        end })
    end
    table.insert(items, { text = _("Source settings"), callback = function() self:showSourceSettings(src) end })
    self:show(src.name, items)
end

-- Only switch and select settings get a UI; the rest keep their defaults.
function Kinkoku:showSourceSettings(src, menu)
    self:task(function()
        local r, err = self:run({ "settings", "get", src.id }, _("Loading…"))
        if not r then return err end
        local values = list(r.values)
        local items = {}
        local function add(settings)
            for __, s in ipairs(list(settings)) do
                local value = values[s.key]
                if s.type == "group" or s.type == "page" then
                    add(s.items)
                elseif s.type == "switch" then
                    table.insert(items, {
                        text = s.title or s.key,
                        mandatory = value and _("on") or _("off"),
                        callback = function() self:setSourceSetting(src, s.key, tostring(not value), menu) end,
                    })
                elseif s.type == "select" and type(s.values) == "table" then
                    local current = value
                    for i, v in ipairs(s.values) do
                        if v == value then current = list(s.titles)[i] or v end
                    end
                    table.insert(items, {
                        text = s.title or s.key,
                        mandatory = tostring(current or ""),
                        callback = function()
                            local choices, picker = {}, nil
                            for i, v in ipairs(s.values) do
                                table.insert(choices, { text = list(s.titles)[i] or v, callback = function()
                                    UIManager:close(picker)
                                    self:setSourceSetting(src, s.key, v, menu)
                                end })
                            end
                            picker = self:show(s.title or s.key, choices)
                        end,
                    })
                end
            end
        end
        add(r.settings)
        if menu then
            self:update(menu, items)
        else
            menu = self:show(_("Source settings"), items)
        end
    end)
end

function Kinkoku:setSourceSetting(src, key, value, menu)
    self:task(function()
        local r, err = self:run({ "settings", "set", src.id, key, value }, _("Loading…"))
        if not r then return err end
        self:showSourceSettings(src, menu)
    end)
end

local function chapter_label(c)
    local parts = {}
    if type(c.volume) == "number" then table.insert(parts, "Vol." .. c.volume) end
    if type(c.number) == "number" then table.insert(parts, "#" .. c.number) end
    if str(c.title) and c.title ~= "" then table.insert(parts, c.title) end
    if #parts == 0 then table.insert(parts, c.key) end
    return table.concat(parts, " ")
end

-- rememberChapters stores chapter order (oldest first) so "Next chapter" works offline.
function Kinkoku:rememberChapters(src_id, manga)
    local chapters = list(manga.chapters)
    local order = {}
    for i = #chapters, 1, -1 do
        table.insert(order, { key = chapters[i].key })
    end
    local all = self:setting("chapter_order", {})
    all[src_id .. "|" .. manga.key] = { chapters = order, ts = os.time() }
    local keys = {}
    for k in pairs(all) do table.insert(keys, k) end
    table.sort(keys, function(a, b) return (all[a].ts or 0) > (all[b].ts or 0) end)
    for i = 31, #keys do all[keys[i]] = nil end
    self:save("chapter_order", all)
end

function Kinkoku:showManga(src, key)
    self:online(function()
        local r, err = self:run({ "manga", src.id, key }, _("Loading…"))
        if not r then return err end
        self:rememberChapters(src.id, r.manga)
        self.current = { src = src, manga = r.manga, downloaded = list(r.downloaded), cover = str(r.coverPath), selected = {} }
        self.current.menu = self:show(r.manga.title, self:mangaItems(), { subtitle = _("Tap a chapter to read it, hold to select several") })
    end)
end

function Kinkoku:mangaItems()
    local cur = self.current
    local m = cur.manga
    local n = 0
    for __ in pairs(cur.selected) do n = n + 1 end
    local refresh = function() self:update(cur.menu, self:mangaItems()) end
    local items = {
        { text = _("Details"), callback = function() self:showDetails() end },
        { text = self:inLibrary(cur.src.id, m.key) and _("Remove from library") or _("Add to library"), callback = function()
            self:toggleLibrary(cur.src.id, cur.src.name, m.key, m.title)
            self:refreshLibrary()
            refresh()
        end },
        { text = T(_("Download selected (%1)"), n), callback = function() self:downloadSelected() end },
        { text = _("Select all not downloaded"), callback = function()
            for __, c in ipairs(list(m.chapters)) do
                if not cur.downloaded[c.key] and c.locked ~= true then cur.selected[c.key] = true end
            end
            refresh()
        end },
        { text = _("Clear selection"), callback = function()
            cur.selected = {}
            refresh()
        end },
    }
    for __, c in ipairs(list(m.chapters)) do
        local rel = str(cur.downloaded[c.key])
        local state = (rel and _("downloaded")) or (c.locked == true and _("locked")) or (cur.selected[c.key] and _("selected")) or nil
        table.insert(items, {
            text = chapter_label(c),
            mandatory = state,
            bold = cur.selected[c.key] or nil,
            callback = function()
                if rel then
                    self:openChapter(rel)
                elseif c.locked ~= true then
                    self:downloadChapters({ c.key }, true)
                end
            end,
            hold_callback = function()
                if not rel and c.locked ~= true then
                    cur.selected[c.key] = not cur.selected[c.key] or nil
                    refresh()
                end
            end,
        })
    end
    return items
end

function Kinkoku:showDetails()
    local m = self.current.manga
    local text = m.title .. "\n"
    if #list(m.authors) > 0 then text = text .. table.concat(m.authors, ", ") .. "\n" end
    text = text .. T(_("Status: %1"), _(STATUS[m.status or 0] or "Unknown")) .. "\n\n" .. (str(m.description) or "")
    local image
    if self.current.cover then
        local RenderImage = require("ui/renderimage")
        local w = math.floor(Device.screen:getWidth() / 3)
        image = RenderImage:renderImageFile(self.current.cover, false, w, math.floor(w * 1.5))
    end
    UIManager:show(InfoMessage:new{ text = text, image = image, show_icon = image ~= nil, height = math.floor(Device.screen:getHeight() * 0.8) })
end

function Kinkoku:downloadSelected()
    local keys = {}
    for __, c in ipairs(list(self.current.manga.chapters)) do
        if self.current.selected[c.key] then table.insert(keys, 1, c.key) end
    end
    if #keys == 0 then
        UIManager:show(InfoMessage:new{ text = _("Hold chapters to select them first."), timeout = 3 })
        return
    end
    self:downloadChapters(keys)
end

-- downloadChapters downloads chapters of the open manga; with open_after, it then opens the first.
function Kinkoku:downloadChapters(keys, open_after)
    local cur = self.current
    self:download(cur.src.id, cur.manga.key, keys, { quiet = open_after, done = function(result)
        for __, ch in ipairs(list(result.chapters)) do
            if str(ch.relPath) then cur.downloaded[ch.key] = ch.relPath end
            cur.selected[ch.key] = nil
        end
        self:update(cur.menu, self:mangaItems())
        local first = list(result.chapters)[1]
        if open_after and first and str(first.relPath) then self:openChapter(first.relPath) end
    end })
end

-- download runs one binary for all chapters. opts: done(result); quiet skips the summary when
-- everything succeeded; wifi_was_on overrides the Wi-Fi state to restore afterwards.
function Kinkoku:download(src_id, manga_key, keys, opts)
    opts = opts or {}
    local wifi_was_on = opts.wifi_was_on
    if wifi_was_on == nil then wifi_was_on = NetworkMgr:isWifiOn() end
    self:online(function()
        local index = {}
        for i, k in ipairs(keys) do index[k] = i end
        local screen = Device.screen
        local args = { "download", src_id, manga_key }
        for __, k in ipairs(keys) do table.insert(args, k) end
        for __, a in ipairs({
            "--out", self:folder(),
            "--conc", tostring(self:setting("concurrency", 2)),
            "--maxw", tostring(math.min(screen:getWidth(), screen:getHeight())),
            "--maxh", tostring(math.max(screen:getWidth(), screen:getHeight())),
        }) do table.insert(args, a) end
        if self:setting("eink", true) then table.insert(args, "--eink") end
        if self:setting("dither", false) then table.insert(args, "--dither") end

        local r, err = self:run(args, _("Downloading…"), function(line)
            local ok, p = pcall(JSON.decode, line)
            if ok and type(p) == "table" and p.p then
                return T(_("Chapter %1/%2, page %3/%4"), index[p.ch] or "?", #keys, p.p, p.n)
            end
        end)
        self:restoreWifi(wifi_was_on)
        if not r then return err end
        local ok_count, failures = 0, {}
        for __, ch in ipairs(list(r.chapters)) do
            if str(ch.error) then
                table.insert(failures, T(_("%1 failed: %2"), ch.key, ch.error))
            else
                ok_count = ok_count + 1
            end
        end
        if not opts.quiet or #failures > 0 then
            local msg = T(_("Downloaded %1 chapter(s)."), ok_count)
            if #failures > 0 then msg = msg .. "\n" .. table.concat(failures, "\n") end
            UIManager:show(InfoMessage:new{ text = msg, timeout = #failures == 0 and 3 or nil })
        end
        if opts.done then opts.done(r) end
    end)
end

function Kinkoku:restoreWifi(was_on)
    if not was_on and self:setting("wifi_off", true) and Device:hasWifiToggle() then
        NetworkMgr:turnOffWifi()
    end
end

function Kinkoku:openChapter(rel)
    local path = self:folder() .. "/" .. rel
    self:closeAll()
    if self.ui.document then
        self.ui:switchDocument(path)
    else
        require("apps/reader/readerui"):showReader(path)
    end
end

function Kinkoku:showDownloads(menu)
    self:task(function()
        local entries, err = self:run({ "index", "list" }, _("Loading…"))
        if not entries then return err end
        local groups, order = {}, {}
        for __, e in ipairs(list(entries)) do
            local manga = e.relPath:match("^(.-)/[^/]*$") or e.relPath
            if not groups[manga] then
                groups[manga] = {}
                table.insert(order, manga)
            end
            table.insert(groups[manga], e)
        end
        table.sort(order)
        local items = {}
        for __, manga in ipairs(order) do
            table.insert(items, { text = manga, mandatory = #groups[manga], callback = function()
                self:showDownloadedManga(manga, groups[manga])
            end })
        end
        if #items == 0 then table.insert(items, { text = _("No downloads yet."), dim = true }) end
        if menu then
            self:update(menu, items)
        else
            self.downloads_menu = self:show(_("Downloads"), items, { subtitle = _("Hold a chapter to delete it") })
        end
    end)
end

function Kinkoku:showDownloadedManga(manga, entries)
    table.sort(entries, function(a, b) return a.relPath < b.relPath end)
    local items = {}
    local menu
    for __, e in ipairs(entries) do
        table.insert(items, {
            text = e.relPath:match("([^/]*)%.cbz$") or e.relPath,
            callback = function() self:openChapter(e.relPath) end,
            hold_callback = function()
                UIManager:show(ConfirmBox:new{
                    text = _("Delete this chapter?") .. "\n" .. e.relPath,
                    ok_text = _("Delete"),
                    ok_callback = function()
                        self:task(function()
                            local r, err = self:run({ "index", "rm", e.relPath, "--out", self:folder() }, _("Loading…"))
                            if not r then return err end
                            UIManager:close(menu)
                            self:showDownloads(self.downloads_menu)
                        end)
                    end,
                })
            end,
        })
    end
    menu = self:show(manga, items)
end

function Kinkoku:settingsItems()
    local function toggle(key, default, label)
        local on = self:setting(key, default)
        return { text = label, mandatory = on and _("on") or _("off"), callback = function()
            self:save(key, not on)
            self:update(self.settings_menu, self:settingsItems())
        end }
    end
    return {
        { text = _("Download folder"), mandatory = self:folder(), callback = function() self:chooseFolder() end },
        toggle("eink", true, _("E-ink page optimization")),
        toggle("dither", false, _("Dithering")),
        toggle("wifi_off", true, _("Turn Wi-Fi off after downloads")),
        { text = _("Reading direction"), mandatory = self:setting("reading_direction", "ltr") == "rtl" and _("Right to left") or _("Left to right"), callback = function()
            self:save("reading_direction", self:setting("reading_direction", "ltr") == "rtl" and "ltr" or "rtl")
            self:update(self.settings_menu, self:settingsItems())
        end },
        { text = _("Pages downloaded at once"), mandatory = self:setting("concurrency", 2), callback = function() self:editConcurrency() end },
        { text = _("FlareSolverr host"), mandatory = self:setting("flaresolverr", ""), callback = function() self:editFlareSolverr() end },
        { text = _("Import cookies.txt"), callback = function() self:importCookies() end },
        { text = _("Clear cookies"), callback = function()
            self:task(function()
                local r, err = self:run({ "cookies", "clear" }, _("Loading…"))
                if not r then return err end
                UIManager:show(InfoMessage:new{ text = _("Cookies cleared."), timeout = 2 })
            end)
        end },
        { text = _("Check for updates"), callback = function() self:checkUpdate() end },
    }
end

function Kinkoku:checkUpdate()
    self:online(function()
        local r, err = self:run({ "update" }, _("Checking for updates…"))
        if not r then return err end
        if r.available ~= true then
            UIManager:show(InfoMessage:new{ text = T(_("Kinkoku %1 is up to date."), r.current), timeout = 3 })
            return
        end
        UIManager:show(ConfirmBox:new{
            text = T(_("Kinkoku %1 is available (you have %2). Update now?"), r.latest, r.current),
            ok_text = _("Update"),
            ok_callback = function()
                self:online(function()
                    local u, uerr = self:run({ "update", "--plugin", self.path }, _("Updating…"))
                    if not u then return uerr end
                    UIManager:askForRestart(T(_("Updated to %1. Restart KOReader to use it."), u.installed))
                end)
            end,
        })
    end)
end

function Kinkoku:showSettings()
    self.settings_menu = self:show(_("Settings"), self:settingsItems())
end

function Kinkoku:chooseFolder()
    local old = self:folder()
    UIManager:show(PathChooser:new{
        select_file = false,
        show_files = false,
        path = old,
        onConfirm = function(path)
            if path == old then return end
            self:save("folder", path)
            self:update(self.settings_menu, self:settingsItems())
            UIManager:show(ConfirmBox:new{
                text = _("Move existing downloads to the new folder?"),
                ok_text = _("Move"),
                cancel_text = _("Leave"),
                ok_callback = function()
                    self:task(function()
                        local r, err = self:run({ "index", "move", old, path }, _("Loading…"))
                        if not r then return err end
                        local msg = T(_("Moved %1 file(s)."), r.moved)
                        for __, f in ipairs(list(r.failed)) do
                            msg = msg .. "\n" .. T(_("%1 failed: %2"), f.relPath, f.error)
                        end
                        UIManager:show(InfoMessage:new{ text = msg })
                    end)
                end,
            })
        end,
    })
end

function Kinkoku:editConcurrency()
    local dialog
    dialog = InputDialog:new{
        title = _("Pages downloaded at once"),
        description = _("1 to 4. Higher is faster on good Wi-Fi."),
        input = tostring(self:setting("concurrency", 2)),
        input_type = "number",
        buttons = { {
            { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
            { text = _("Save"), is_enter_default = true, callback = function()
                local n = tonumber(dialog:getInputText())
                if not n or n ~= math.floor(n) or n < 1 or n > 4 then
                    UIManager:show(InfoMessage:new{ text = _("Enter a whole number from 1 to 4."), timeout = 3 })
                    return
                end
                self:save("concurrency", n)
                UIManager:close(dialog)
                self:update(self.settings_menu, self:settingsItems())
            end },
        } },
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function Kinkoku:editFlareSolverr()
    local dialog
    dialog = InputDialog:new{
        title = _("FlareSolverr host"),
        description = _("e.g. http://192.168.1.2:8191 (empty = off)"),
        input = self:setting("flaresolverr", ""),
        buttons = { {
            { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
            { text = _("Save"), is_enter_default = true, callback = function()
                self:save("flaresolverr", dialog:getInputText())
                UIManager:close(dialog)
                self:update(self.settings_menu, self:settingsItems())
            end },
        } },
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

function Kinkoku:importCookies()
    UIManager:show(PathChooser:new{
        select_directory = false,
        path = Device:isKindle() and "/mnt/us" or DataStorage:getDataDir(),
        onConfirm = function(file)
            self:task(function()
                local r, err = self:run({ "cookies", "import", file }, _("Loading…"))
                if not r then return err end
                UIManager:show(InfoMessage:new{ text = T(_("Imported %1 cookie(s)."), r.imported), timeout = 2 })
            end)
        end,
    })
end

-- chapterRel returns the open document's path in the download folder, or nil if it isn't a chapter.
function Kinkoku:chapterRel()
    local file = self.ui.document and self.ui.document.file or ""
    local folder = self:folder() .. "/"
    if file:sub(1, #folder) ~= folder or not file:match("%.cbz$") then return nil end
    return file:sub(#folder + 1)
end

function Kinkoku:onReaderReady()
    local view = self.ui.view
    if not view or not self:chapterRel() then return end
    -- set directly: ReaderView:onToggleReadingOrder would pop a notification on every chapter
    view.inverse_reading_order = self:setting("reading_direction", "ltr") == "rtl"
    view:setupTouchZones()
end

-- wraps ReaderStatus so it runs before other EndOfBook handlers
function Kinkoku:hookEndOfBook()
    local status = self.ui.status
    local rel = self:chapterRel()
    if not status or not status.onEndOfBook or not rel then return end
    local original = status.onEndOfBook
    status.onEndOfBook = function(s, ...)
        if self:onChapterEnd(rel) then return true end
        return original(s, ...)
    end
end

local function next_chapter(order, key)
    for i, c in ipairs(order and order.chapters or {}) do
        if c.key == key then return order.chapters[i + 1] end
    end
end

-- onChapterEnd opens the next chapter, downloading it first if needed. When the next chapter isn't
-- known yet, it asks the source, which may have published new ones.
function Kinkoku:onChapterEnd(rel)
    local bin = self:binary()
    if not bin then return false end
    local p = io.popen(string.format("KINKOKU_DATA=%s %s index list 2>/dev/null", shell_quote(self.data_dir), shell_quote(bin)))
    local out = p and p:read("*a")
    if p then p:close() end
    local ok, entries = pcall(JSON.decode, out or "")
    if not ok then return false end
    local current
    local by_key = {}
    for __, e in ipairs(list(entries)) do
        if e.relPath == rel then current = e end
        by_key[e.source .. "|" .. e.mangaKey .. "|" .. e.chapterKey] = e
    end
    if not current then return false end
    local manga_id = current.source .. "|" .. current.mangaKey
    local wifi_was_on = NetworkMgr:isWifiOn()
    local function open(next_ch)
        local downloaded = by_key[manga_id .. "|" .. next_ch.key]
        if downloaded then
            self:openChapter(downloaded.relPath)
            return
        end
        self:download(current.source, current.mangaKey, { next_ch.key }, { quiet = true, wifi_was_on = wifi_was_on, done = function(r)
            local ch = list(r.chapters)[1]
            if ch and str(ch.relPath) then self:openChapter(ch.relPath) end
        end })
    end
    local next_ch = next_chapter(self:setting("chapter_order", {})[manga_id], current.chapterKey)
    if next_ch then
        open(next_ch)
        return true
    end
    self:online(function()
        local r, err = self:run({ "manga", current.source, current.mangaKey }, _("Looking for the next chapter…"))
        if r then
            self:rememberChapters(current.source, r.manga)
            next_ch = next_chapter(self:setting("chapter_order", {})[manga_id], current.chapterKey)
        end
        if not next_ch then
            self:restoreWifi(wifi_was_on)
            return err or _("This was the last chapter.")
        end
        UIManager:nextTick(function() open(next_ch) end)
    end)
    return true
end

return Kinkoku
