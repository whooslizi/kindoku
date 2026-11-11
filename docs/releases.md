# Releases

What changed in each release. Every push to `eoleun` publishes a release named `v0.1.<build>`,
so some build numbers are missing (v0.1.9, v0.1.13 and v0.1.14 were removed or never published).
Download releases from the [releases page](https://github.com/whooslizi/kinkoku/releases).

## Unreleased

- Settings: tap **Pages downloaded at once** to type a number from 1 to 4, instead of tapping
  repeatedly to cycle through the values.

## v0.1.15

- Check for updates now reads releases from the renamed `whooslizi/kinkoku` repository.
- README: a Documentation section and more credits.

## v0.1.12

- Fixed: Check for updates could stay stuck on "Updating…". The update replaced the running
  binary, which crashed it on the Kindle (SIGBUS), and the plugin then waited forever. The new
  binary is now staged as `kinkoku.new` and swapped in before the next run.
- If kinkoku stops without printing anything, the plugin now shows an error instead of hanging.
- Cancelling a command now cleans up the stopped process.

## v0.1.11

- About 3 times faster downloads with e-ink optimization on: faster PNG compression (files are
  about 6% larger) and a faster grayscale conversion for palette PNGs.

## v0.1.10

- GitHub Actions dependencies updated by Dependabot.

## v0.1.8

- Dependabot checks Go modules and GitHub Actions weekly.

## v0.1.7

- Status messages ("Loading…", "Downloading…") appear in a centered box with "Tap to cancel",
  like KOReader's own messages.

## v0.1.6

- Tap a chapter to download and open it. Hold chapters to select several, then tap
  **Download selected**.
- At the end of a chapter, the next one opens automatically, downloading it first if needed. If no
  next chapter is known, kinkoku asks the source for new chapters.
- Settings: **Reading direction** (left to right or right to left) for swipes and taps.
- README: how to report bugs and where the logs are.

## v0.1.5

- Failed commands are logged to `logs/error.log` and crashes to `logs/crash.log` in kinkoku's data
  folder.

## v0.1.4

- Releases are named with just the version number.

## v0.1.3

- Added DOCS.md, an overview of how the repo fits together.

## v0.1.2

- One download per device type (`kinkoku-armv7.koplugin.zip` and so on) instead of one zip with
  every binary.
- Settings: **Check for updates** installs the latest release for your device.

## v0.1.1

First release.

- `kinkoku`: a Go binary that runs Aidoku sources: source lists, install and update, search,
  listings, manga details, and chapter downloads as CBZ with e-ink optimization.
- `kinkoku.koplugin`: Library, Search all sources, Sources, Downloads and Settings screens in
  KOReader, plus "next chapter" at the end of a chapter.
