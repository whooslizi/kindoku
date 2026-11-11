# Migrating to kinkoku

If you really trust a project someone memed into existence with your manga collection, here is how
to move it over. It's one command, run once from a computer.

## From Rakuyomi

You need a computer with Python 3 (preinstalled on macOS and most Linux; on Windows, install it from
[python.org](https://www.python.org/downloads/)).

1. Install kinkoku (see the [README](../README.md#install)). In KOReader, open kinkoku, add a source
   list and install the sources you used in Rakuyomi (menu, top left, then **Sources**).
2. Close KOReader and connect the e-reader to the computer over USB.
3. Download [`migrate-rakuyomi.py`](../scripts/migrate-rakuyomi.py) and run it with the path where
   the e-reader is mounted:

   ```sh
   python3 migrate-rakuyomi.py /Volumes/Kindle            # macOS
   python3 migrate-rakuyomi.py /media/$USER/Kindle        # Linux
   python migrate-rakuyomi.py E:\                         # Windows
   ```

   Add `--dry-run` first to see what it would do without changing anything. On a Kobo, add
   `--device-root /mnt/onboard`.

4. Eject the e-reader and open KOReader.

The script:

- adds every manga from Rakuyomi's library to kinkoku's Library;
- moves every downloaded chapter into your kinkoku download folder, renamed from Rakuyomi's hashed
  names to `<Manga title>/<chapter>.cbz`, so they show up in Downloads and "next chapter" works;
- keeps the reading position and KOReader history of those chapters;
- backs up every file it changes as `<file>.before-rakuyomi`.

Running it again is safe: anything already moved is skipped. If it lists sources to install, install
them in kinkoku to open those manga.

What doesn't carry over, because kinkoku doesn't have it: read/unread marks, trackers (AniList,
MyAnimeList and others), playlists, background update checks and Rakuyomi's own settings.

Once you're happy, you can delete `koreader/plugins/rakuyomi.koplugin` and `koreader/rakuyomi`.

## From anything else

Any reader that saves chapters as CBZ files: keep reading them from KOReader's file browser, or open
the manga in kinkoku and tap a chapter to download it there. If the app used Aidoku sources, install
the same ones in kinkoku and rebuild your library with **Search all sources** (hold a result to add
it).
