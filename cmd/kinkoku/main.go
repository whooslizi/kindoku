// Command kinkoku runs Aidoku sources from the command line: one command, JSON on stdout, exit.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	_ "time/tzdata" // Kindles ship without a zoneinfo database

	"github.com/whooslizi/kinkoku/repo"
	"github.com/whooslizi/kinkoku/source"
	"github.com/whooslizi/kinkoku/store"
)

var version = "dev"

const usage = `kinkoku ` + "%s" + ` — Aidoku sources for KOReader

  repo add <url> | repo list | repo install <id> | repo update
  list
  search <src> <query> [--page N] [--filters JSON]
  search-all <query>
  listing <src> <listingId> [--page N]
  manga <src> <mangaKey>
  download <src> <mangaKey> <chapterKey...> --out <folder> [--eink] [--dither] [--maxw 1072 --maxh 1448] [--conc 2]
  index list | index rm <relPath> [--out <folder>] | index move <fromFolder> <toFolder>
  settings get <src> [key] | settings set <src> <key> <value>
  cookies import <file> | cookies clear
  update [--plugin <dir>]   check for (and install) the latest release

Global: --data <dir> (or KINKOKU_DATA). Env: KINKOKU_FLARESOLVERR, KINKOKU_LANG.
`

type args struct {
	pos   []string
	flags map[string]string
}

var boolFlags = map[string]bool{"eink": true, "dither": true}

func parseArgs(in []string) (args, error) {
	a := args{flags: map[string]string{}}
	for i := 0; i < len(in); i++ {
		name, ok := strings.CutPrefix(in[i], "--")
		if !ok {
			a.pos = append(a.pos, in[i])
			continue
		}
		if k, v, has := strings.Cut(name, "="); has {
			a.flags[k] = v
		} else if boolFlags[name] {
			a.flags[name] = "true"
		} else if i+1 < len(in) {
			a.flags[name] = in[i+1]
			i++
		} else {
			return a, fmt.Errorf("--%s needs a value", name)
		}
	}
	return a, nil
}

func (a args) int(name string, def int) (int, error) {
	v, ok := a.flags[name]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("--%s: %w", name, err)
	}
	return n, nil
}

func (a args) need(n int, what string) error {
	if len(a.pos) < n {
		return fmt.Errorf("usage: %s", what)
	}
	return nil
}

type app struct {
	data   string
	jar    *store.Jar
	client *http.Client
	repos  *repo.Manager
}

func main() {
	out, err := run(context.Background(), os.Args[1:])
	if err != nil {
		json.NewEncoder(os.Stdout).Encode(map[string]string{"error": err.Error()})
		os.Exit(1)
	}
	if out != nil {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.Encode(out)
	}
}

func run(ctx context.Context, argv []string) (any, error) {
	a, err := parseArgs(argv)
	if err != nil {
		return nil, err
	}
	if len(a.pos) == 0 || a.pos[0] == "help" {
		fmt.Fprintf(os.Stderr, usage, version)
		return nil, nil
	}
	if a.pos[0] == "version" {
		return map[string]string{"version": version, "arch": arch}, nil
	}
	data := a.flags["data"]
	if data == "" {
		data = os.Getenv("KINKOKU_DATA")
	}
	if data == "" {
		home, _ := os.UserHomeDir()
		data = filepath.Join(home, ".kinkoku")
	}
	if err := os.MkdirAll(data, 0o755); err != nil {
		return nil, err
	}
	jar, err := store.LoadJar(data)
	if err != nil {
		return nil, fmt.Errorf("cookies.json: %w", err)
	}
	defer jar.Save()
	client := newClient(data, jar)
	x := &app{data: data, jar: jar, client: client, repos: &repo.Manager{Data: data, Client: client}}

	cmd, rest := a.pos[0], a.pos[1:]
	a.pos = rest
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch cmd {
	case "repo":
		return x.repo(ctx, sub, a)
	case "list":
		return x.repos.List()
	case "search":
		return x.search(ctx, a)
	case "search-all":
		return x.searchAll(ctx, a)
	case "listing":
		return x.listing(ctx, a)
	case "manga":
		return x.manga(ctx, a)
	case "download":
		return x.download(ctx, a)
	case "index":
		return x.index(sub, a)
	case "settings":
		return x.settings(sub, a)
	case "cookies":
		return x.cookies(sub, a)
	case "update":
		return x.update(ctx, a)
	}
	return nil, fmt.Errorf("unknown command %q (try `kinkoku help`)", cmd)
}

func (x *app) repo(ctx context.Context, sub string, a args) (any, error) {
	switch sub {
	case "add":
		if err := a.need(2, "repo add <url>"); err != nil {
			return nil, err
		}
		r, err := x.repos.Add(a.pos[1])
		if err != nil {
			return nil, err
		}
		return map[string]any{"url": r.URL, "name": r.Name, "sources": len(r.Sources)}, nil
	case "list":
		rs, err := x.repos.Repos()
		if err != nil {
			return nil, err
		}
		installed, err := x.repos.List()
		if err != nil {
			return nil, err
		}
		have := map[string]int{}
		for _, s := range installed {
			have[s.ID] = s.Version
		}
		type row struct {
			repo.Entry
			Repo      string `json:"repo"`
			Installed int    `json:"installed,omitempty"` // installed version
		}
		out := []row{}
		for _, r := range rs {
			for _, e := range r.Sources {
				out = append(out, row{Entry: e, Repo: r.Name, Installed: have[e.ID]})
			}
		}
		return out, nil
	case "install":
		if err := a.need(2, "repo install <id>"); err != nil {
			return nil, err
		}
		inst, err := x.repos.Install(ctx, a.pos[1])
		if err != nil {
			return nil, err
		}
		return map[string]any{"id": a.pos[1], "version": inst.Version, "supported": len(inst.Unsupported) == 0, "unsupported": inst.Unsupported}, nil
	case "update":
		return x.repos.Update(ctx)
	}
	return nil, errors.New("usage: repo add <url> | repo list | repo install <id> | repo update")
}

func (x *app) open(ctx context.Context, id string) (*source.Source, error) {
	dir := x.repos.SourceDir(id)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("source %q is not installed", id)
	}
	var inst repo.Installed
	store.ReadJSON(filepath.Join(dir, "kinkoku.json"), &inst)
	if len(inst.Unsupported) > 0 {
		return nil, fmt.Errorf("source %q needs JavaScript, which kinkoku does not support", id)
	}
	return source.Open(ctx, dir, x.client)
}

func (x *app) search(ctx context.Context, a args) (any, error) {
	if err := a.need(2, "search <src> <query> [--page N] [--filters JSON]"); err != nil {
		return nil, err
	}
	page, err := a.int("page", 1)
	if err != nil {
		return nil, err
	}
	var filters []source.FilterValue
	if f := a.flags["filters"]; f != "" {
		if err := json.Unmarshal([]byte(f), &filters); err != nil {
			return nil, fmt.Errorf("--filters: %w", err)
		}
	}
	s, err := x.open(ctx, a.pos[0])
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.Search(a.pos[1], page, filters)
}

// searchAll searches page 1 of every installed, supported source, three at a time.
func (x *app) searchAll(ctx context.Context, a args) (any, error) {
	if err := a.need(1, "search-all <query>"); err != nil {
		return nil, err
	}
	installed, err := x.repos.List()
	if err != nil {
		return nil, err
	}
	type result struct {
		Source  string         `json:"source"`
		Name    string         `json:"name"`
		Entries []source.Manga `json:"entries"`
		Error   string         `json:"error,omitempty"`
	}
	out := []result{}
	for _, s := range installed {
		if s.Supported {
			out = append(out, result{Source: s.ID, Name: s.Name, Entries: []source.Manga{}})
		}
	}
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i := range out {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			r := &out[i]
			s, err := x.open(ctx, r.Source)
			if err == nil {
				var res source.MangaPageResult
				res, err = s.Search(a.pos[0], 1, nil)
				s.Close()
				if res.Entries != nil {
					r.Entries = res.Entries
				}
			}
			if err != nil {
				r.Error = err.Error()
			}
		}()
	}
	wg.Wait()
	return out, nil
}

func (x *app) listing(ctx context.Context, a args) (any, error) {
	if err := a.need(2, "listing <src> <listingId> [--page N]"); err != nil {
		return nil, err
	}
	page, err := a.int("page", 1)
	if err != nil {
		return nil, err
	}
	s, err := x.open(ctx, a.pos[0])
	if err != nil {
		return nil, err
	}
	defer s.Close()
	ls, err := s.Listings()
	if err != nil {
		return nil, err
	}
	l := source.Listing{ID: a.pos[1], Name: a.pos[1]}
	for _, o := range ls {
		if o.ID == a.pos[1] {
			l = o
		}
	}
	return s.MangaList(l, page)
}

func (x *app) manga(ctx context.Context, a args) (any, error) {
	if err := a.need(2, "manga <src> <mangaKey>"); err != nil {
		return nil, err
	}
	s, err := x.open(ctx, a.pos[0])
	if err != nil {
		return nil, err
	}
	defer s.Close()
	m, err := s.MangaUpdate(source.Manga{Key: a.pos[1]}, true, true)
	if err != nil {
		return nil, err
	}
	ix, err := store.LoadIndex(x.data)
	if err != nil {
		return nil, err
	}
	downloaded := map[string]string{}
	for _, e := range ix.Entries {
		if e.Source == a.pos[0] && e.MangaKey == m.Key {
			downloaded[e.ChapterKey] = e.RelPath
		}
	}
	return map[string]any{"manga": m, "downloaded": downloaded, "coverPath": x.cachedCover(s, m.Cover)}, nil
}

func (x *app) settings(sub string, a args) (any, error) {
	if (sub != "get" && sub != "set") || len(a.pos) < 2 || (sub == "set" && len(a.pos) < 4) {
		return nil, errors.New("usage: settings get <src> [key] | settings set <src> <key> <value>")
	}
	id := a.pos[1]
	dir := x.repos.SourceDir(id)
	if _, err := source.ReadInfo(dir); err != nil {
		return nil, fmt.Errorf("source %q is not installed", id)
	}
	defs, err := source.LoadDefaults(dir)
	if err != nil {
		return nil, err
	}
	if sub == "set" {
		if err := defs.SetString(a.pos[2], a.pos[3]); err != nil {
			return nil, fmt.Errorf("%s: %w", a.pos[2], err)
		}
	}
	all := defs.JSON()
	if len(a.pos) >= 3 {
		return map[string]any{a.pos[2]: all[a.pos[2]]}, nil
	}
	return map[string]any{"values": all, "settings": source.ReadSettings(dir)}, nil
}

func (x *app) cookies(sub string, a args) (any, error) {
	switch sub {
	case "import":
		if err := a.need(2, "cookies import <file>"); err != nil {
			return nil, err
		}
		f, err := os.Open(a.pos[1])
		if err != nil {
			return nil, err
		}
		defer f.Close()
		n, err := x.jar.ImportNetscape(f)
		if err != nil {
			return nil, err
		}
		return map[string]int{"imported": n}, x.jar.Save()
	case "clear":
		return map[string]bool{"cleared": true}, x.jar.Clear()
	}
	return nil, errors.New("usage: cookies import <file> | cookies clear")
}
