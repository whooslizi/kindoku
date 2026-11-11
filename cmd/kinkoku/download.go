package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/whooslizi/kinkoku/cbz"
	"github.com/whooslizi/kinkoku/eink"
	"github.com/whooslizi/kinkoku/source"
	"github.com/whooslizi/kinkoku/store"
)

type dlOptions struct {
	eink bool
	img  eink.Options
	conc int
}

type chapterResult struct {
	Key     string `json:"key"`
	RelPath string `json:"relPath,omitempty"`
	Pages   int    `json:"pages,omitempty"`
	Bytes   int64  `json:"bytes,omitempty"`
	Skipped bool   `json:"skipped,omitempty"` // already downloaded
	Error   string `json:"error,omitempty"`
}

func (x *app) download(ctx context.Context, a args) (any, error) {
	const use = "download <src> <mangaKey> <chapterKey...> --out <folder> [--eink] [--dither] [--maxw N --maxh N] [--conc N] [--flat]"
	if err := a.need(3, use); err != nil {
		return nil, err
	}
	folder := a.flags["out"]
	if folder == "" {
		return nil, errors.New("usage: " + use)
	}
	o := dlOptions{eink: a.flags["eink"] == "true", img: eink.Options{Dither: a.flags["dither"] == "true"}}
	var err error
	if o.img.MaxW, err = a.int("maxw", 1072); err != nil {
		return nil, err
	}
	if o.img.MaxH, err = a.int("maxh", 1448); err != nil {
		return nil, err
	}
	if o.conc, err = a.int("conc", 2); err != nil {
		return nil, err
	}
	o.conc = min(4, max(1, o.conc))
	if o.img.MaxW < 1 || o.img.MaxH < 1 {
		return nil, errors.New("--maxw and --maxh must be positive")
	}

	// Cancellation: KOReader kills us (SIGTERM) or closes our pipes (EPIPE on the next progress line).
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	signal.Notify(make(chan os.Signal, 1), syscall.SIGPIPE) // turn SIGPIPE into a write error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	src, mangaKey, chapterKeys := a.pos[0], a.pos[1], a.pos[2:]
	s, err := x.open(ctx, src)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	s.Host.Ctx = ctx
	m, err := s.MangaUpdate(source.Manga{Key: mangaKey}, true, true)
	if err != nil {
		return nil, err
	}
	ix, err := store.LoadIndex(x.data)
	if err != nil {
		return nil, err
	}
	title := m.Title
	if title == "" {
		title = m.Key
	}
	// --flat saves chapters straight into the folder, named "<Manga> - <chapter>.cbz", without a cover
	flat := a.flags["flat"] == "true"
	mangaDir := cbz.Sanitize(title)
	if flat {
		mangaDir = ""
	}
	if err := os.MkdirAll(filepath.Join(folder, mangaDir), 0o755); err != nil {
		return nil, err
	}
	removeStaleTemps(filepath.Join(folder, mangaDir))
	if cover := filepath.Join(folder, mangaDir, "cover.jpg"); !flat && !exists(cover) && m.Cover != "" {
		if b, err := x.coverJPEG(s, m.Cover, o.img); err == nil {
			store.WriteAtomic(cover, b)
		}
	}

	progress := func(key string, p, n int) {
		line, _ := json.Marshal(map[string]any{"ch": key, "p": p, "n": n})
		if _, err := os.Stderr.Write(append(line, '\n')); err != nil {
			cancel()
		}
	}

	results := []chapterResult{}
	ok := 0
	for _, key := range chapterKeys {
		r := chapterResult{Key: key}
		ch := findChapter(m.Chapters, key)
		switch {
		case ch == nil:
			r.Error = "chapter not found"
		case ch.Locked:
			r.Error = "chapter is locked"
		default:
			if e := ix.Find(src, m.Key, key); e != nil && exists(filepath.Join(folder, e.RelPath)) {
				r.RelPath, r.Pages, r.Bytes, r.Skipped = e.RelPath, e.Pages, e.Bytes, true
				break
			}
			name := cbz.ChapterName(ch.Number, ch.Title, ch.Key)
			if flat {
				name = cbz.FlatName(title, ch.Number, ch.Title, ch.Key)
			}
			rel := filepath.ToSlash(filepath.Join(mangaDir, uniqueName(ix, src, m.Key, mangaDir, name, ch.Key)+".cbz"))
			var err error
			r.Pages, r.Bytes, err = x.downloadChapter(ctx, s, m, *ch, filepath.Join(folder, rel), o, progress)
			if err != nil {
				r.Error = err.Error()
				break
			}
			r.RelPath = rel
			ix.Put(store.Entry{Source: src, MangaKey: m.Key, ChapterKey: key, RelPath: rel, Title: title, Pages: r.Pages, Bytes: r.Bytes})
			if err := ix.Save(); err != nil {
				return nil, err
			}
		}
		if ctx.Err() != nil {
			return nil, errors.New("cancelled")
		}
		if r.Error == "" {
			ok++
		}
		results = append(results, r)
	}
	if ok == 0 && len(results) > 0 {
		return nil, fmt.Errorf("no chapter downloaded: %s", results[0].Error)
	}
	return map[string]any{"title": title, "folder": mangaDir, "chapters": results}, nil
}

func findChapter(cs []source.Chapter, key string) *source.Chapter {
	for i := range cs {
		if cs[i].Key == key {
			return &cs[i]
		}
	}
	return nil
}

// uniqueName returns name, suffixed with a key hash if another chapter (e.g. a different
// scanlation of the same number) already uses it.
func uniqueName(ix *store.Index, src, mangaKey, dir, name, chapterKey string) string {
	rel := filepath.ToSlash(filepath.Join(dir, name+".cbz"))
	for _, e := range ix.Entries {
		if e.RelPath == rel && !(e.Source == src && e.MangaKey == mangaKey && e.ChapterKey == chapterKey) {
			sum := sha1.Sum([]byte(chapterKey))
			return name + " [" + hex.EncodeToString(sum[:3]) + "]"
		}
	}
	return name
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func removeStaleTemps(dir string) {
	tmps, _ := filepath.Glob(filepath.Join(dir, "*.cbz.tmp"))
	for _, t := range tmps {
		os.Remove(t)
	}
}

type page struct {
	data []byte
	ext  string
	err  error
}

func (x *app) downloadChapter(ctx context.Context, s *source.Source, m source.Manga, ch source.Chapter, path string, o dlOptions, progress func(string, int, int)) (int, int64, error) {
	pages, err := s.PageList(m, ch)
	if err != nil {
		return 0, 0, err
	}
	if len(pages) == 0 {
		return 0, 0, errors.New("chapter has no pages")
	}
	// Images built by the source live in its (single-threaded) store: take them out up front.
	imgs := make([]image.Image, len(pages))
	for i, p := range pages {
		if p.Kind == source.PageImage {
			imgs[i] = s.Host.Image(p.Image)
			s.Host.Remove(p.Image)
		}
	}

	w, err := cbz.Create(path)
	if err != nil {
		return 0, 0, err
	}
	ctx, cancel := context.WithCancel(ctx) // stops remaining fetches if a page fails
	defer cancel()
	results := make([]chan page, len(pages))
	for i := range results {
		results[i] = make(chan page, 1)
	}
	sem := make(chan struct{}, o.conc)
	go func() {
		for i, p := range pages {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[i] <- page{err: ctx.Err()}
				continue
			}
			go func() {
				defer func() { <-sem }()
				results[i] <- x.fetchPage(ctx, s, p, imgs[i], o)
			}()
		}
	}()
	// Write in order; at most o.conc pages are in flight, so memory stays bounded.
	for i := range pages {
		p := <-results[i]
		if p.err == nil {
			p.err = w.AddPage(fmt.Sprintf("%03d.%s", i+1, p.ext), p.data)
		}
		if p.err != nil {
			w.Abort()
			return 0, 0, fmt.Errorf("page %d: %w", i+1, p.err)
		}
		progress(ch.Key, i+1, len(pages))
	}
	size, err := w.Close(cbz.ComicInfo{
		Series: m.Title, Number: cbz.FormatNumber(ch.Number), Title: ch.Title, Web: firstNonEmpty(ch.URL, m.URL),
	})
	return w.Pages(), size, err
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func (x *app) fetchPage(ctx context.Context, s *source.Source, p source.Page, img image.Image, o dlOptions) page {
	switch p.Kind {
	case source.PageImage:
		if img == nil {
			return page{err: errors.New("source returned an invalid image")}
		}
		return finish(img, nil, o)
	case source.PageText:
		return page{err: errors.New("text pages (novels) are not supported")}
	case source.PageZip:
		return page{err: errors.New("zip pages are not supported")}
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			case <-ctx.Done():
				return page{err: ctx.Err()}
			}
		}
		req, err := s.ImageRequest(p.URL, p.Context)
		if err != nil {
			return page{err: err}
		}
		s.Host.Do(req)
		switch {
		case req.Err != nil:
			lastErr = req.Err
			continue
		case req.Status >= 400:
			lastErr = fmt.Errorf("HTTP %d", req.Status)
			continue
		}
		if s.Has("process_page_image") {
			img, raw, err := s.ProcessPage(req, req.Status, req.RespHdr, req.Data, p.Context)
			if err != nil {
				return page{err: err}
			}
			return finish(img, raw, o)
		}
		return finish(nil, req.Data, o)
	}
	return page{err: lastErr}
}

// finish encodes a page: e-ink PNG when enabled, otherwise the original bytes (or a PNG for
// images the source drew itself).
func finish(img image.Image, raw []byte, o dlOptions) page {
	switch {
	case o.eink && img != nil:
		return page{data: eink.Encode(eink.Process(img, o.img)), ext: "png"}
	case o.eink:
		if out, ok := eink.Optimize(raw, o.img); ok {
			return page{data: out, ext: "png"}
		}
	case img != nil:
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return page{err: err}
		}
		return page{data: buf.Bytes(), ext: "png"}
	}
	return page{data: raw, ext: extOf(raw)}
}

func extOf(b []byte) string {
	switch http.DetectContentType(b) {
	case "image/png":
		return "png"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return "jpg"
}

// coverJPEG fetches a cover and returns it as a JPEG no larger than the screen.
func (x *app) coverJPEG(s *source.Source, url string, o eink.Options) ([]byte, error) {
	req, err := s.ImageRequest(url, nil)
	if err != nil {
		return nil, err
	}
	s.Host.Do(req)
	if req.Err != nil {
		return nil, req.Err
	}
	if req.Status >= 400 {
		return nil, fmt.Errorf("HTTP %d", req.Status)
	}
	img, _, err := image.Decode(bytes.NewReader(req.Data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, eink.Process(img, o), &jpeg.Options{Quality: 85})
	return buf.Bytes(), err
}

// cachedCover saves the detail-screen cover to <data>/cache/cover.jpg; "" if it can't.
func (x *app) cachedCover(s *source.Source, url string) string {
	if url == "" {
		return ""
	}
	b, err := x.coverJPEG(s, url, eink.Options{MaxW: 600, MaxH: 800})
	if err != nil {
		return ""
	}
	p := filepath.Join(x.data, "cache", "cover.jpg")
	if store.WriteAtomic(p, b) != nil {
		return ""
	}
	return p
}

// safeRel rejects index paths that would escape the download folder.
func safeRel(rel string) error {
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(filepath.Clean(filepath.FromSlash(rel)), "..") {
		return fmt.Errorf("invalid path %q", rel)
	}
	return nil
}

func (x *app) index(sub string, a args) (any, error) {
	ix, err := store.LoadIndex(x.data)
	if err != nil {
		return nil, err
	}
	switch sub {
	case "list":
		return ix.Entries, nil
	case "rm":
		if err := a.need(2, "index rm <relPath> [--out <folder>]"); err != nil {
			return nil, err
		}
		rel := a.pos[1]
		if err := safeRel(rel); err != nil {
			return nil, err
		}
		if !ix.Remove(rel) {
			return nil, fmt.Errorf("%q is not in the index", rel)
		}
		if folder := a.flags["out"]; folder != "" {
			p := filepath.Join(folder, filepath.FromSlash(rel))
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			if dir := filepath.Dir(p); filepath.Clean(dir) != filepath.Clean(folder) {
				removeIfOnlyCover(dir)
			}
		}
		return map[string]string{"removed": rel}, ix.Save()
	case "move":
		if err := a.need(3, "index move <fromFolder> <toFolder>"); err != nil {
			return nil, err
		}
		return moveAll(ix, a.pos[1], a.pos[2])
	}
	return nil, errors.New("usage: index list | index rm <relPath> [--out <folder>] | index move <from> <to>")
}

// removeIfOnlyCover deletes a manga folder once its last chapter is gone.
func removeIfOnlyCover(dir string) {
	if cbzs, _ := filepath.Glob(filepath.Join(dir, "*.cbz")); len(cbzs) == 0 {
		os.Remove(filepath.Join(dir, "cover.jpg"))
		os.Remove(dir) // only succeeds if empty
	}
}

type moveFailure struct {
	RelPath string `json:"relPath"`
	Error   string `json:"error"`
}

// moveAll moves every indexed chapter (and each manga's cover) from one folder to another:
// copy, verify the size, then delete the original.
func moveAll(ix *store.Index, from, to string) (any, error) {
	if filepath.Clean(from) == filepath.Clean(to) {
		return nil, errors.New("source and destination are the same folder")
	}
	moved := 0
	failed := []moveFailure{}
	dirs := map[string]bool{}
	for _, e := range ix.Entries {
		if err := safeRel(e.RelPath); err != nil {
			failed = append(failed, moveFailure{e.RelPath, err.Error()})
			continue
		}
		dir := filepath.Dir(filepath.FromSlash(e.RelPath))
		if dir != "." && !dirs[dir] {
			dirs[dir] = true
			moveFile(filepath.Join(from, dir, "cover.jpg"), filepath.Join(to, dir, "cover.jpg")) // optional
		}
		if err := moveFile(filepath.Join(from, filepath.FromSlash(e.RelPath)), filepath.Join(to, filepath.FromSlash(e.RelPath))); err != nil {
			failed = append(failed, moveFailure{e.RelPath, err.Error()})
			continue
		}
		moved++
	}
	for dir := range dirs {
		removeIfOnlyCover(filepath.Join(from, dir))
	}
	return map[string]any{"moved": moved, "failed": failed}, nil
}

func moveFile(src, dst string) error {
	st, err := os.Stat(src)
	if errors.Is(err, os.ErrNotExist) {
		if exists(dst) { // moved by an earlier, interrupted run
			return nil
		}
		return errors.New("file is missing")
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != st.Size() {
		err = fmt.Errorf("size mismatch: copied %d of %d bytes", n, st.Size())
	}
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if dt, err := os.Stat(dst); err != nil || dt.Size() != st.Size() {
		return errors.New("copy verification failed")
	}
	return os.Remove(src)
}
