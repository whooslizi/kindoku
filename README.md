# kinkoku

A scrappy rewrite of the Aidoku integration for KOReader: read [Aidoku](https://github.com/Aidoku/Aidoku)
sources on your Kindle, download chapters as small CBZs into a folder you choose, and read them
offline. Fewer features, lower battery use.

- `kinkoku`: one static Go binary. It runs one command, prints JSON and exits. No daemon, no database.
- `kinkoku.koplugin`: a KOReader plugin that calls the binary and shows text-only, e-ink friendly menus.

## Documentation

- [DOCS.md](DOCS.md): how the repo fits together (the binary, the plugin, downloads, state on disk,
  releases, tests), for anyone who wants to understand or change kinkoku without reading every file.
- [docs/abi.md](docs/abi.md): every host function an Aidoku source can call, and which ones kinkoku
  implements.

## Install

1. Download the zip for your device from the [latest release](../../releases/latest):
   `kinkoku-armv7.koplugin.zip` for a Kindle Basic 11 (and most Kindles and Kobos), `armv6` for older
   ARM devices, `arm64` for 64-bit ARM, `x64` for desktop Linux.
2. Unzip it and copy the `kinkoku.koplugin` folder to `/mnt/us/koreader/plugins/` on the Kindle
   (or the `plugins/` folder of any KOReader install).
3. Restart KOReader. Open the top menu, then the search tab, then **Kinkoku**.
4. Tap the menu icon (top left), then **Sources**, **Add source list**. The Aidoku Community list is
   pre-filled. Then **Install sources**.

Each zip holds the binary for one architecture only (about 4 MB zipped). **Settings > Check for
updates** fetches the latest GitHub release for the same architecture and installs it in place.

## Use

- **Library** is the home screen: the manga you saved, with their source on the right. Hold one to
  remove it.
- The menu icon (top left) opens **Search all sources**, **Sources**, **Downloads** and **Settings**.
  Hold a search result to add it to or remove it from the library (saved ones show a star).
- A manga: tap a chapter to download and open it. To download several at once, hold chapters to
  select them, then tap **Download selected**. Downloaded chapters are marked "downloaded".
- **Downloads**: everything you downloaded, grouped by manga. Long-press a chapter to delete it.
- Turning past the last page of a chapter opens the next one, downloading it first if needed. If
  kinkoku doesn't know a next chapter yet, it asks the source for new ones.
- **Settings**: download folder (default `/mnt/us/manga`; changing it offers to move existing files),
  e-ink optimization, dithering, Wi-Fi off after downloads, reading direction (left to right:
  swipe left or tap the right side for the next page; right to left: the opposite, for manga),
  pages downloaded at once (1–4),
  FlareSolverr host, cookie import, check for updates.

Chapters are saved as `<folder>/<Manga Title>/<NNN.N> - <Chapter Title>.cbz` with a `ComicInfo.xml`,
plus `cover.jpg` once per manga. They are plain CBZs: KOReader opens them with the plugin disabled.

With e-ink optimization on (the default), pages are converted to 16-level grayscale, downscaled
(never upscaled) to fit the screen (strips taller than 2:1 are fitted to the width only), and saved
as 4-bit PNGs. In testing, a 14-page chapter went from 18 MB of source images to 4 MB.

## What's not here

No history, trackers, home feeds, background updates or notifications. Sources that need
JavaScript (WebView) are listed as unsupported. See
[docs/abi.md](docs/abi.md) for exactly which host functions are implemented.

## CLI

```
kinkoku repo add <url> | repo list | repo install <id> | repo update
kinkoku list
kinkoku search <src> <query> [--page N] [--filters JSON]
kinkoku search-all <query>
kinkoku listing <src> <listingId> [--page N]
kinkoku manga <src> <mangaKey>
kinkoku download <src> <mangaKey> <chapterKey...> --out <folder> [--eink] [--dither] [--maxw 1072 --maxh 1448] [--conc 2]
kinkoku index list | index rm <relPath> [--out <folder>] | index move <fromFolder> <toFolder>
kinkoku settings get <src> [key] | settings set <src> <key> <value>
kinkoku cookies import <cookies.txt> | cookies clear
kinkoku update [--plugin <dir>]
```

Output is JSON on stdout; errors exit non-zero with `{"error": "..."}`. `download` prints progress
lines `{"ch":"<chapterKey>","p":3,"n":20}` on stderr and removes its partial `.cbz.tmp` on SIGTERM.

State lives in `--data <dir>` (or `KINKOKU_DATA`, default `~/.kinkoku`) as plain JSON files:
`repos.json`, `sources/<id>/` (the unpacked source plus `defaults.json`), `cookies.json`, `index.json`.
Failed commands are appended to `logs/error.log` and crashes to `logs/crash.log` (on the Kindle:
`koreader/settings/kinkoku/logs/`).
Other environment variables: `KINKOKU_FLARESOLVERR` (e.g. `http://192.168.1.2:8191`, used only on
Cloudflare/DDoS-Guard 403/503 challenges) and `KINKOKU_LANG` (preferred source language).

`--filters` takes FilterValues such as
`[{"type":"select","id":"genre","value":"action"},{"type":"sort","id":"sort","index":1,"ascending":false}]`
(types: text, sort, check, select, multiselect, range).

## Build and test

```sh
go test ./...                              # unit tests (offline)
go test -tags e2e ./cmd/kinkoku            # live: install 3 sources, search, download a chapter each
luacheck kinkoku.koplugin
scripts/build.sh                           # builds kinkoku.koplugin/bin/{armv6,armv7,arm64,x64}/kinkoku
```

Every push to the default branch runs the tests, builds all architectures and publishes a GitHub
release with one `kinkoku-<arch>.koplugin.zip` per architecture.

## Reporting bugs

Open an issue with what you did, what you expected, and the logs. On a Kindle they are in
`/mnt/us/koreader/` (on other devices, the KOReader folder):

- `settings/kinkoku/logs/error.log`: every failed kinkoku command.
- `settings/kinkoku/logs/crash.log`: kinkoku crashes.
- `settings/kinkoku/run.log`: the output of the last kinkoku command.
- `crash.log`: KOReader's own log, for errors in the plugin.

Also include your device, the kinkoku version (Settings > Check for updates shows it) and the
source name.

## Credits

- [Aidoku](https://github.com/Aidoku/Aidoku): the manga reader whose sources kinkoku runs.
- [AidokuRunner](https://github.com/Aidoku/AidokuRunner) by Skittyblock: kinkoku's source runner follows
  its behavior (how sources are loaded, the host import modules, encoding and error conventions).
- [aidoku-rs](https://github.com/Aidoku/aidoku-rs) (MIT): the source SDK that defines the ABI.
- [Aidoku Community sources](https://github.com/Aidoku-Community/sources) (MIT/Apache-2.0); one of them
  is used as a test fixture (see `source/testdata/NOTICE`).
- [Rakuyomi](https://github.com/hanatsumi/rakuyomi) by hanatsumi, and its maintained fork
  [tachibana-shin/rakuyomi](https://github.com/tachibana-shin/rakuyomi): the KOReader manga plugin that
  showed Aidoku sources can run on e-readers, and the reference for kinkoku's download behavior.
- [KOReader](https://github.com/koreader/koreader): the reader kinkoku plugs into.
- [FlareSolverr](https://github.com/FlareSolverr/FlareSolverr): optional, for sites behind Cloudflare.
- [wazero](https://wazero.io), [cascadia](https://github.com/andybalholm/cascadia),
  golang.org/x/net/html and golang.org/x/image.

Not affiliated with Aidoku, Rakuyomi or KOReader.
