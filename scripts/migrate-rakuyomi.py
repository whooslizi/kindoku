#!/usr/bin/env python3
"""One-off: move Rakuyomi's library and downloads into kinkoku.

Run on a computer with the e-reader connected over USB, with KOReader closed:

    python3 scripts/migrate-rakuyomi.py /Volumes/Kindle

Moves every downloaded chapter (with its KOReader reading progress) into kinkoku's download folder
and index, updates KOReader's history, and adds Rakuyomi's library to kinkoku's Library. Files it
changes are backed up next to themselves as *.before-rakuyomi. Running it again is safe.
"""

import argparse
import base64
import glob
import hashlib
import json
import os
import shutil
import sqlite3
import sys
import time
import zipfile

MAX_NAME_BYTES = 200  # cbz.maxNameBytes


# --- KOReader's Lua settings files (return { ... } as written by its dump()) ---

def lua_load(text):
    i = text.index("return") + len("return")

    def ws():
        nonlocal i
        while i < len(text):
            if text[i].isspace():
                i += 1
            elif text.startswith("--", i):
                i = text.find("\n", i) + 1 or len(text)
            else:
                break

    def value():
        nonlocal i
        ws()
        c = text[i]
        if c == "{":
            i += 1
            items = {}
            n = 1
            while True:
                ws()
                if text[i] == "}":
                    i += 1
                    break
                if text[i] == "[":
                    i += 1
                    key = value()
                    ws()
                    i += 1  # ]
                    ws()
                    i += 1  # =
                    items[key] = value()
                else:
                    items[n] = value()
                    n += 1
                ws()
                if text[i] in ",;":
                    i += 1
            return items
        if c == '"':
            i += 1
            out = []
            while text[i] != '"':
                if text[i] == "\\":
                    i += 1
                    e = text[i]
                    if e.isdigit():
                        j = i
                        while j < i + 3 and text[j].isdigit():
                            j += 1
                        out.append(chr(int(text[i:j])))
                        i = j
                        continue
                    out.append({"n": "\n", "t": "\t", "r": "\r", "\n": "\n", "0": "\0"}.get(e, e))
                else:
                    out.append(text[i])
                i += 1
            i += 1
            return "".join(out)
        for word, v in (("true", True), ("false", False), ("nil", None)):
            if text.startswith(word, i):
                i += len(word)
                return v
        j = i
        while j < len(text) and (text[j] in "+-.eExX" or text[j].isalnum()):
            j += 1
        num = text[i:j]
        i = j
        return float(num) if any(ch in num for ch in ".eE") else int(num, 0)

    return value()


def lua_quote(s):
    out = []
    for ch in s:
        if ch in '"\\':
            out.append("\\" + ch)
        elif ch == "\n":
            out.append("\\n")
        elif ord(ch) < 32 or ord(ch) == 127:
            out.append("\\%03d" % ord(ch))
        else:
            out.append(ch)
    return '"' + "".join(out) + '"'


def lua_dump(v, indent=""):
    if isinstance(v, dict):
        lines = ["{"]
        keys = sorted(v, key=lambda k: (isinstance(k, str), k))
        for k in keys:
            key = "[%d]" % k if isinstance(k, int) else "[%s]" % lua_quote(k)
            lines.append("%s    %s = %s," % (indent, key, lua_dump(v[k], indent + "    ")))
        lines.append(indent + "}")
        return "\n".join(lines)
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, str):
        return lua_quote(v)
    if isinstance(v, float) and v.is_integer():
        return "%.1f" % v
    return repr(v)


def lua_list(t):
    return [t[k] for k in sorted(k for k in t if isinstance(k, int))] if isinstance(t, dict) else []


def write_atomic(path, data):
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write(data)
    os.replace(tmp, path)


def backup(path):
    if os.path.exists(path) and not os.path.exists(path + ".before-rakuyomi"):
        shutil.copy2(path, path + ".before-rakuyomi")


# --- kinkoku's file names (cbz.Sanitize, cbz.ChapterName, uniqueName in cmd/kinkoku) ---

def trim_bytes(s, n):
    while len(s.encode()) > n:
        s = s[:-1]
    return s.rstrip(" .")


def sanitize(name):
    s = "".join("_" if ord(c) < 0x20 or ord(c) == 0x7F or c in '<>:"/\\|?*' else c
                for c in name if c != "�")
    s = trim_bytes(s.strip(" ."), MAX_NAME_BYTES)
    return s or "_"


def chapter_name(number, title, key):
    parts = []
    if number is not None:
        parts.append("%05.1f" % number)
    if title:
        parts.append(title)
    if not parts:
        parts.append(key)
    return trim_bytes(sanitize(" - ".join(parts)), MAX_NAME_BYTES - 14)


def unique_name(index, src, manga, folder, number, title, key):
    name = chapter_name(number, title, key)
    rel = folder + "/" + name + ".cbz"
    for e in index:
        if e["relPath"] == rel and (e["source"], e["mangaKey"], e["chapterKey"]) != (src, manga, key):
            return name + " [" + hashlib.sha1(key.encode()).hexdigest()[:6] + "]"
    return name


def rakuyomi_file(src, manga, chapter):
    digest = hashlib.sha256((src + manga + chapter).encode()).digest()
    return base64.urlsafe_b64encode(digest).decode().rstrip("=") + ".cbz"


def count_pages(path):
    try:
        with zipfile.ZipFile(path) as z:
            return sum(1 for n in z.namelist() if not n.endswith("/") and not n.endswith(".xml"))
    except zipfile.BadZipFile:
        return 0


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("root", help="where the e-reader is mounted, e.g. /Volumes/Kindle")
    ap.add_argument("--device-root", default="/mnt/us", help="the same folder as the e-reader sees it (Kindle: /mnt/us, Kobo: /mnt/onboard)")
    ap.add_argument("--dry-run", action="store_true", help="only print what would happen")
    a = ap.parse_args()

    root = a.root.rstrip("/")
    dev = a.device_root.rstrip("/")
    to_local = lambda p: root + p[len(dev):] if p.startswith(dev + "/") else p
    to_device = lambda p: dev + p[len(root):]
    koreader = root + "/koreader"
    raku = koreader + "/rakuyomi"
    kdata = koreader + "/settings/kinkoku"
    settings_path = koreader + "/settings/kinkoku.lua"
    history_path = koreader + "/history.lua"
    index_path = kdata + "/index.json"

    if not os.path.exists(raku + "/database.db"):
        sys.exit("No Rakuyomi data at " + raku)
    if not os.path.isdir(kdata):
        sys.exit("Open kinkoku in KOReader once (and install your sources) before migrating.")

    settings = lua_load(open(settings_path, encoding="utf-8").read()) if os.path.exists(settings_path) else {}
    folder_dev = settings.get("folder") or dev + "/manga"
    folder = to_local(folder_dev)
    index = json.load(open(index_path)) if os.path.exists(index_path) else []

    db = sqlite3.connect("file:%s?mode=ro" % (raku + "/database.db"), uri=True)
    rs = json.load(open(raku + "/settings.json")) if os.path.exists(raku + "/settings.json") else {}
    files = {}
    for d in [raku + "/downloads"] + ([to_local(rs["storage_path"])] if rs.get("storage_path") else []):
        for f in glob.glob(glob.escape(d) + "/*.cbz"):
            if not os.path.basename(f).startswith("._"):
                files[os.path.basename(f)] = f

    # downloads
    moved = {}
    have = {(e["source"], e["mangaKey"], e["chapterKey"]) for e in index}
    rows = db.execute("""SELECT c.source_id, c.manga_id, c.chapter_id, coalesce(c.title, ''), c.chapter_number,
        coalesce(i.title, c.manga_id) FROM chapter_informations c
        LEFT JOIN manga_informations i USING (source_id, manga_id)""")
    for src, manga, ch, title, number, manga_title in rows:
        old = files.get(rakuyomi_file(src, manga, ch))
        if not old or (src, manga, ch) in have:
            continue
        mdir = sanitize(manga_title)
        rel = mdir + "/" + unique_name(index, src, manga, mdir, number, title, ch) + ".cbz"
        new = folder + "/" + rel
        if os.path.exists(new):
            print("skip, already exists:", rel)
            continue
        print(os.path.basename(old), "->", rel)
        index.append({"source": src, "mangaKey": manga, "chapterKey": ch, "relPath": rel,
                      "pages": count_pages(old), "bytes": os.path.getsize(old), "ts": int(time.time())})
        moved[to_device(old)] = to_device(new)
        if a.dry_run:
            continue
        os.makedirs(os.path.dirname(new), exist_ok=True)
        shutil.move(old, new)
        sdr = old[:-4] + ".sdr"
        if os.path.isdir(sdr):
            shutil.move(sdr, new[:-4] + ".sdr")
            for meta in glob.glob(glob.escape(new[:-4] + ".sdr") + "/metadata.*.lua"):
                text = open(meta, encoding="utf-8").read()
                write_atomic(meta, text.replace(lua_quote(to_device(old)), lua_quote(to_device(new))))
        # saved per file so an interruption never leaves a moved chapter out of the index
        backup(index_path)
        write_atomic(index_path, json.dumps(index, indent=2, ensure_ascii=False))

    if moved and not a.dry_run and os.path.exists(history_path):
        text = open(history_path, encoding="utf-8").read()
        for old, new in moved.items():
            text = text.replace(lua_quote(old), lua_quote(new))
        backup(history_path)
        write_atomic(history_path, text)

    # library
    def source_name(sid):
        try:
            info = json.load(open("%s/sources/%s/source.json" % (kdata, sid)))
            return (info.get("info") or info).get("name") or sid
        except OSError:
            return None

    library = lua_list(settings.get("library", {}))
    known = {(e.get("source"), e.get("key")) for e in library}
    added, missing = 0, set()
    for sid, manga, title in db.execute("""SELECT l.source_id, l.manga_id, coalesce(i.title, l.manga_id)
            FROM manga_library l LEFT JOIN manga_informations i USING (source_id, manga_id)"""):
        if (sid, manga) in known:
            continue
        name = source_name(sid)
        if name is None:
            missing.add(sid)
        library.append({"source": sid, "source_name": name or sid, "key": manga, "title": title})
        added += 1
    if added and not a.dry_run:
        library.sort(key=lambda e: e["title"].lower())
        settings["library"] = {i + 1: e for i, e in enumerate(library)}
        backup(settings_path)
        write_atomic(settings_path, "-- ./settings/kinkoku.lua\nreturn " + lua_dump(settings) + "\n")

    print("\n%s %d chapters and %s %d manga to the library." % (
        "Would move" if a.dry_run else "Moved", len(moved), "would add" if a.dry_run else "added", added))
    if missing:
        print("Install these sources in kinkoku to open their manga:", ", ".join(sorted(missing)))


if __name__ == "__main__":
    main()
