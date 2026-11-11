package source_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whooslizi/kinkoku/repo"
	"github.com/whooslizi/kinkoku/source"
)

// fakeSite serves synthetic pages for the TCB Scans source, whatever the host.
type fakeSite map[string]string

func (f fakeSite) RoundTrip(r *http.Request) (*http.Response, error) {
	body, ok := f[r.URL.Path]
	code := 200
	if !ok {
		code, body = 404, "not found"
	}
	return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

var site = fakeSite{
	"/projects": `<div class="bg-card border border-border rounded p-3 mb-3">
		<img class="w-24 h-24 object-cover rounded-lg" src="https://cdn/op.png">
		<a class="mb-3 text-white text-lg font-bold" href="/mangas/5/one-piece">One Piece</a></div>
	<div class="bg-card border border-border rounded p-3 mb-3">
		<a class="mb-3 text-white text-lg font-bold" href="/mangas/6/jujutsu">Jujutsu Kaisen</a></div>`,
	"/mangas/5/one-piece": `<div class="order-1 bg-card border border-border rounded py-3">
		<div class="flex items-center justify-center"><img src="https://cdn/cover.png"></div>
		<h1 class="my-3 font-bold text-3xl">One Piece</h1><p class="leading-6 my-3">Pirates.</p></div>
	<a class="bg-card border border-border rounded p-3 mb-3" href="/chapters/2/op-2">
		<div class="text-lg font-bold">One Piece Chapter 1101.5</div><div class="text-gray-500">Two</div></a>
	<a class="bg-card border border-border rounded p-3 mb-3" href="/chapters/1/op-1">
		<div class="text-lg font-bold">One Piece Chapter 1100</div><div class="text-gray-500">One</div></a>`,
	"/chapters/1/op-1": `<div class="flex flex-col items-center justify-center">
		<picture><img src="https://cdn/1.png"></picture><picture><img src="https://cdn/2.png"></picture></div>`,
}

func openTCB(t *testing.T) *source.Source {
	t.Helper()
	aix, err := os.ReadFile("testdata/en.tcbscans.aix")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "en.tcbscans")
	var inst repo.Installed
	if err := repo.InstallAix(context.Background(), aix, dir, &inst); err != nil {
		t.Fatal(err)
	}
	if len(inst.Unsupported) != 0 {
		t.Fatalf("unexpected unsupported imports %v", inst.Unsupported)
	}
	s, err := source.Open(context.Background(), dir, &http.Client{Transport: site})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestLoadSource(t *testing.T) {
	s := openTCB(t)
	if s.Info.Info.ID != "en.tcbscans" || s.Info.Info.Name != "TCB Scans" {
		t.Fatalf("info = %+v", s.Info.Info)
	}
	if !s.Has("get_search_manga_list") || s.Has("get_home") {
		t.Fatal("feature detection by exports is wrong")
	}
}

func TestSearchMangaPages(t *testing.T) {
	s := openTCB(t)
	res, err := s.Search("piece", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Key != "/mangas/5/one-piece" || res.Entries[0].Cover != "https://cdn/op.png" || res.HasNextPage {
		t.Fatalf("search = %+v", res)
	}
	all, err := s.Search("", 1, []source.FilterValue{{Type: "text", ID: "author", Value: "x"}})
	if err != nil || len(all.Entries) != 2 {
		t.Fatalf("empty query = %+v, %v", all, err)
	}

	m, err := s.MangaUpdate(source.Manga{Key: "/mangas/5/one-piece"}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "One Piece" || m.Description != "Pirates." || m.Viewer != 2 || len(m.Chapters) != 2 {
		t.Fatalf("manga = %+v", m)
	}
	c := m.Chapters[1]
	if c.Key != "/chapters/1/op-1" || c.Title != "One" || c.Number == nil || *c.Number != 1100 || c.Scanlators[0] != "TCB Scans" {
		t.Fatalf("chapter = %+v", c)
	}
	if n := m.Chapters[0].Number; n == nil || *n != 1101.5 {
		t.Fatalf("fractional chapter number = %v", n)
	}

	pages, err := s.PageList(m, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || pages[0].Kind != source.PageURL || pages[1].URL != "https://cdn/2.png" {
		t.Fatalf("pages = %+v", pages)
	}
	if s.Host.Len() != 0 {
		t.Fatalf("%d descriptors leaked", s.Host.Len())
	}
}

func TestSourceErrorMessage(t *testing.T) {
	s := openTCB(t)
	// the details selector is missing, so the source returns AidokuError::message
	_, err := s.MangaUpdate(source.Manga{Key: "/chapters/1/op-1"}, true, false)
	if err == nil || err.Error() != "Unable to find manga details" {
		t.Fatalf("err = %v", err)
	}
	_, err = s.MangaList(source.Listing{ID: "x"}, 1)
	if err == nil || !strings.Contains(err.Error(), "does not implement") {
		t.Fatalf("missing export err = %v", err)
	}
}
