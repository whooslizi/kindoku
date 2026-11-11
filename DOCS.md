# How kinkoku works

A map of the repo for people who want to understand it without reading every file. For install
and usage, see [README.md](README.md). For the host functions a source can call, see
[docs/abi.md](docs/abi.md).

## The big picture

```
KOReader
  kinkoku.koplugin/main.lua      menus, settings, library, "next chapter"
        |  runs once per action:  kinkoku <command> <args>   (JSON on stdout, progress on stderr)
        v
kinkoku (Go binary)
  cmd/kinkoku      CLI commands, HTTP client, downloads, self-update
  repo/            source lists and installed sources
  source/          loads one Aidoku source (.wasm) and calls its exports
  abi/             host functions the source imports (net, html, std, defaults, canvas)
  eink/            page images -> 16-gray PNGs sized for the screen
  cbz/             writes chapters as .cbz with ComicInfo.xml
  store/           plain JSON state files, written atomically; cookie jar
```

The plugin keeps no connection to the binary. Every action starts the binary, reads the JSON it
prints, and the binary exits. This keeps memory low on a 512 MB Kindle and means there is no
daemon to manage.

## What an Aidoku source is

A source is an `.aix` file: a zip with `Payload/main.wasm`, `source.json` (name, languages,
listings) and an optional `settings.json`. The wasm is built with
[aidoku-rs](https://github.com/Aidoku/aidoku-rs). It exports functions such as
`get_search_manga_list` and `get_page_list`, and imports host functions to make HTTP requests,
parse HTML, read settings and draw on a canvas. kinkoku is the host: it runs the wasm with
[wazero](https://wazero.io) and provides those imports, following
[AidokuRunner](https://github.com/Aidoku/AidokuRunner).

## Life of a command

Take `kinkoku search en.tcbscans "one piece"`:

1. `cmd/kinkoku/main.go` parses the arguments, opens the data folder (`KINKOKU_DATA`), loads the
   cookie jar and builds the HTTP client (`http.go`: browser User-Agent, optional FlareSolverr).
2. `app.open` checks that the source is installed and doesn't need JavaScript, then
   `source.Open` compiles `main.wasm`, registers the settings defaults, links the host modules
   (`abi.Host.Link`) and calls the source's `start` export.
3. `Source.Search` encodes the arguments with postcard (`abi/postcard.go`), puts them in the host
   object table, and calls the export. The source makes requests through `abi/net.go` and parses
   HTML through `abi/html.go`. It returns a pointer to a `[len][cap][payload]` buffer.
4. `source/models.go` decodes the payload into Go structs, the export's buffer is freed with
   `free_result`, and `main` prints the result as JSON.

Errors come back as `{"error": "..."}` with a non-zero exit code.

## Host objects and encoding

- The host keeps a table of objects (requests, HTML nodes, images, byte buffers). The source only
  sees integer descriptors (`rid`). `std.destroy` frees one. See `abi/host.go`.
- Structured data crosses the boundary in [postcard](https://postcard.jamesmunns.com/wire-format)
  format. Field order in `source/models.go` matches the aidoku-rs structs exactly; changing the
  order breaks decoding.
- Imports kinkoku doesn't implement are linked to stubs that log `unimplemented: module.name` and
  return -1, so a source fails with a clear message instead of refusing to load.
- Sources importing the `js` module (WebView, JavaScript) are marked unsupported and never run.

## Downloads

`kinkoku download <src> <manga> <chapters...> --out <folder>` (`cmd/kinkoku/download.go`):

1. Fetches the manga and its chapters, saves `cover.jpg` once per manga folder.
2. For each chapter: skips it if the index already has it, asks the source for the page list,
   and fetches pages with 1 to 4 requests at a time. Pages are written in order, so memory holds
   at most that many pages.
3. Each page goes through `source.ProcessPage` (for sources that descramble images), then
   `eink.Optimize` when `--eink` is set: grayscale, Catmull-Rom downscale to the screen, 16 gray
   levels, 4-bit PNG. Strips taller than 2:1 (webtoons) are fitted to the screen width only.
4. `cbz.Writer` writes `<Chapter>.cbz.tmp`, adds `ComicInfo.xml`, and renames it into place. On
   failure or SIGTERM the `.tmp` is deleted.
5. The chapter is added to `index.json`, which maps source, manga and chapter keys to the file's
   path relative to the download folder.

Progress lines such as `{"ch":"<key>","p":3,"n":20}` go to stderr. The plugin reads them to show
"Chapter 1/3, page 3/20".

## State on disk

Everything is plain JSON in the data folder, written with `store.WriteAtomic` (temp file + rename):

| file | contents |
|---|---|
| `repos.json` | source lists you added and their sources |
| `sources/<id>/` | the unpacked source, `kinkoku.json` (version, unsupported imports), `defaults.json` (its settings) |
| `cookies.json` | the cookie jar |
| `index.json` | downloaded chapters |
| `flaresolverr.json` | User-Agent per host, saved after a FlareSolverr solve |
| `cache/cover.jpg` | the cover shown on the manga details screen |
| `logs/error.log` | one line per failed command: time, version, arch, arguments, error |
| `logs/crash.log` | Go panic traces from any goroutine (`debug.SetCrashOutput`) |

The plugin keeps its own settings (download folder, library, toggles, chapter order for "next
chapter") in KOReader's `settings/kinkoku.lua`.

## The plugin

`kinkoku.koplugin/main.lua` is one file:

- `Kinkoku:run` starts the binary with `Trapper:dismissablePopen`, shows a cancellable message,
  and kills the process (its pid is written to `run.pid`) if the user cancels.
- `Kinkoku:show` opens one full-screen `Menu` per screen. Back closes only that screen.
- Library is the home screen. The top-left menu opens Search all sources, Sources, Downloads and
  Settings.
- After a chapter's last page, `hookEndOfBook` offers the next chapter, downloading it if needed.
- `kinkoku_l10n.lua` holds Vietnamese translations; English strings are the keys.

## Builds and releases

- `scripts/build.sh` cross-compiles static binaries into `kinkoku.koplugin/bin/<arch>/kinkoku`
  and bakes in the version and architecture.
- `.github/workflows/release.yml` runs vet, tests and luacheck on every push. On the default
  branch it publishes a release with one `kinkoku-<arch>.koplugin.zip` per architecture.
- `kinkoku update --plugin <dir>` (Settings > Check for updates) downloads the zip for its own
  architecture from the latest release and replaces the plugin files.

## Tests

- `go test ./...` is offline. `source/source_test.go` runs a real source
  (`source/testdata/en.tcbscans.aix`) against a fake website.
- `go test -tags e2e ./cmd/kinkoku` uses the network: installs three community sources, searches,
  and downloads a chapter from each. Sites change, so failures here may not be kinkoku bugs.
- `luacheck kinkoku.koplugin` lints the plugin.
