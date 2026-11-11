package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/whooslizi/kinkoku/store"
)

func TestMoveAll(t *testing.T) {
	from, to, data := t.TempDir(), filepath.Join(t.TempDir(), "new"), t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write(filepath.Join(from, "A", "001.0.cbz"), "chapter one")
	write(filepath.Join(from, "A", "cover.jpg"), "cover")
	write(filepath.Join(to, "B", "002.0.cbz"), "already moved") // interrupted earlier run
	ix, _ := store.LoadIndex(data)
	ix.Put(store.Entry{Source: "s", MangaKey: "a", ChapterKey: "1", RelPath: "A/001.0.cbz"})
	ix.Put(store.Entry{Source: "s", MangaKey: "b", ChapterKey: "2", RelPath: "B/002.0.cbz"})
	ix.Put(store.Entry{Source: "s", MangaKey: "c", ChapterKey: "3", RelPath: "C/missing.cbz"})
	ix.Put(store.Entry{Source: "s", MangaKey: "d", ChapterKey: "4", RelPath: "../escape.cbz"})

	out, err := moveAll(ix, from, to)
	if err != nil {
		t.Fatal(err)
	}
	res := out.(map[string]any)
	if res["moved"] != 2 || len(res["failed"].([]moveFailure)) != 2 {
		t.Fatalf("result %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(to, "A", "001.0.cbz")); string(b) != "chapter one" {
		t.Fatal("chapter not copied")
	}
	if !exists(filepath.Join(to, "A", "cover.jpg")) {
		t.Fatal("cover not moved")
	}
	if exists(filepath.Join(from, "A")) {
		t.Fatal("empty source folder left behind")
	}
	if _, err := moveAll(ix, to, to+"/"); err == nil {
		t.Fatal("same folder must be rejected")
	}
}

func TestParseArgs(t *testing.T) {
	a, err := parseArgs([]string{"download", "s", "m", "c1", "--out", "/x", "--eink", "c2", "--maxw=800"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.pos) != 5 || a.pos[4] != "c2" || a.flags["out"] != "/x" || a.flags["eink"] != "true" || a.flags["maxw"] != "800" {
		t.Fatalf("%+v", a)
	}
	if _, err := parseArgs([]string{"search", "--page"}); err == nil {
		t.Fatal("missing flag value must fail")
	}
}

func TestIndexRmKeepsFlatFolder(t *testing.T) {
	data, folder := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(folder, "A - 001.0.cbz"), []byte("x"), 0o644)
	ix, _ := store.LoadIndex(data)
	ix.Put(store.Entry{Source: "s", MangaKey: "a", ChapterKey: "1", RelPath: "A - 001.0.cbz"})
	ix.Save()
	x := &app{data: data}
	if _, err := x.index("rm", args{pos: []string{"rm", "A - 001.0.cbz"}, flags: map[string]string{"out": folder}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Errorf("download folder was removed with its last chapter: %v", err)
	}
}
