package store

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestIndexRelativePaths(t *testing.T) {
	dir := t.TempDir()
	ix, _ := LoadIndex(dir)
	ix.Put(Entry{Source: "s", MangaKey: "m", ChapterKey: "c1", RelPath: "Manga/001.0.cbz", Pages: 3})
	ix.Put(Entry{Source: "s", MangaKey: "m", ChapterKey: "c1", RelPath: "Manga/001.0 - x.cbz", Pages: 4}) // replaces
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	ix2, _ := LoadIndex(dir)
	if len(ix2.Entries) != 1 || ix2.Entries[0].Pages != 4 || ix2.Entries[0].TS == 0 {
		t.Fatalf("entries %+v", ix2.Entries)
	}
	if ix2.Find("s", "m", "c1") == nil || ix2.Find("s", "m", "c2") != nil {
		t.Fatal("Find")
	}
	if !ix2.Remove("Manga/001.0 - x.cbz") || len(ix2.Entries) != 0 {
		t.Fatal("Remove")
	}
}

func TestCookies(t *testing.T) {
	dir := t.TempDir()
	j, _ := LoadJar(dir)
	n, err := j.ImportNetscape(strings.NewReader(`# Netscape HTTP Cookie File
.example.com	TRUE	/	TRUE	4102444800	cf_clearance	abc
#HttpOnly_www.other.com	FALSE	/app	FALSE	0	sid	x	y
bad line
`))
	if err != nil || n != 2 {
		t.Fatalf("imported %d, %v", n, err)
	}
	j.SetCookies(mustURL("https://example.com/"), []*http.Cookie{{Name: "gone", Value: "1", MaxAge: -1}})
	if err := j.Save(); err != nil {
		t.Fatal(err)
	}
	j2, _ := LoadJar(dir)
	names := func(u string) string {
		var s []string
		for _, c := range j2.Cookies(mustURL(u)) {
			s = append(s, c.Name+"="+c.Value)
		}
		return strings.Join(s, ";")
	}
	if got := names("https://img.example.com/x"); got != "cf_clearance=abc" {
		t.Errorf("subdomain: %q", got)
	}
	if got := names("http://example.com/"); got != "" {
		t.Errorf("secure cookie sent over http: %q", got)
	}
	if got := names("http://www.other.com/app/1"); got != "sid=x\ty" {
		t.Errorf("host-only path cookie: %q", got)
	}
	if got := names("http://sub.www.other.com/app"); got != "" {
		t.Errorf("host-only cookie leaked to subdomain: %q", got)
	}
	if err := j2.Clear(); err != nil || len(j2.Cookies(mustURL("https://example.com/"))) != 0 {
		t.Fatal("Clear")
	}
}

func mustURL(s string) *url.URL { u, _ := url.Parse(s); return u }
