// Package repo manages Aidoku source lists (index.min.json) and installed sources.
package repo

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/whooslizi/kinkoku/source"
	"github.com/whooslizi/kinkoku/store"
)

type Entry struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     int      `json:"version"`
	IconURL     string   `json:"iconURL,omitempty"`
	DownloadURL string   `json:"downloadURL"`
	Languages   []string `json:"languages,omitempty"`
	Rating      int      `json:"contentRating"`
	BaseURL     string   `json:"baseURL,omitempty"`
}

type Repo struct {
	URL     string  `json:"url"`
	Name    string  `json:"name"`
	Sources []Entry `json:"sources"`
}

// Installed is what kinkoku records next to an unpacked source.
type Installed struct {
	Repo        string   `json:"repo,omitempty"`
	Version     int      `json:"version"`
	Unsupported []string `json:"unsupported,omitempty"`
}

type Manager struct {
	Data   string
	Client *http.Client
}

func (m *Manager) reposPath() string          { return filepath.Join(m.Data, "repos.json") }
func (m *Manager) SourceDir(id string) string { return filepath.Join(m.Data, "sources", id) }

func (m *Manager) Repos() ([]Repo, error) {
	var rs []Repo
	err := store.ReadJSON(m.reposPath(), &rs)
	return rs, err
}

func (m *Manager) get(u string, limit int64) ([]byte, error) {
	resp, err := m.Client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("GET %s: response too large", u)
	}
	return b, err
}

func normalize(u string) string {
	if !strings.HasSuffix(u, ".json") {
		u = strings.TrimSuffix(u, "/") + "/index.min.json"
	}
	return u
}

func (m *Manager) fetch(u string) (Repo, error) {
	b, err := m.get(u, 16<<20)
	if err != nil {
		return Repo{}, err
	}
	var r Repo
	if err := json.Unmarshal(b, &r); err != nil {
		return Repo{}, fmt.Errorf("%s: not an Aidoku source list: %w", u, err)
	}
	r.URL = u
	return r, nil
}

func (m *Manager) Add(u string) (Repo, error) {
	r, err := m.fetch(normalize(u))
	if err != nil {
		return r, err
	}
	rs, err := m.Repos()
	if err != nil {
		return r, err
	}
	out := []Repo{r}
	for _, o := range rs {
		if o.URL != r.URL {
			out = append(out, o)
		}
	}
	return r, store.WriteJSON(m.reposPath(), out)
}

func (m *Manager) Install(ctx context.Context, id string) (Installed, error) {
	rs, err := m.Repos()
	if err != nil {
		return Installed{}, err
	}
	for _, r := range rs {
		for _, e := range r.Sources {
			if e.ID == id {
				return m.install(ctx, r, e)
			}
		}
	}
	return Installed{}, fmt.Errorf("source %q not found in any repo (run `repo add` first)", id)
}

func (m *Manager) install(ctx context.Context, r Repo, e Entry) (Installed, error) {
	base, err := url.Parse(r.URL)
	if err != nil {
		return Installed{}, err
	}
	ref, err := url.Parse(e.DownloadURL)
	if err != nil {
		return Installed{}, err
	}
	aix, err := m.get(base.ResolveReference(ref).String(), 64<<20)
	if err != nil {
		return Installed{}, err
	}
	inst := Installed{Repo: r.URL, Version: e.Version}
	return inst, InstallAix(ctx, aix, m.SourceDir(e.ID), &inst)
}

// InstallAix unpacks an .aix (zip with Payload/*) into dir, replacing it only once the new copy is
// complete, and records inst (with the JS imports found) as kinkoku.json.
func InstallAix(ctx context.Context, aix []byte, dir string, inst *Installed) error {
	z, err := zip.NewReader(bytes.NewReader(aix), int64(len(aix)))
	if err != nil {
		return fmt.Errorf("bad .aix: %w", err)
	}
	tmp := dir + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, f := range z.File {
		name, ok := strings.CutPrefix(f.Name, "Payload/")
		if !ok || name == "" || strings.HasSuffix(name, "/") {
			continue
		}
		if strings.Contains(name, "/") || strings.Contains(name, `\`) || name == ".." {
			return fmt.Errorf("bad .aix: unexpected path %q", f.Name)
		}
		if f.UncompressedSize64 > 32<<20 {
			return fmt.Errorf("bad .aix: %s too large", name)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 32<<20))
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(tmp, name), b, 0o644); err != nil {
			return err
		}
	}
	if _, err := source.ReadInfo(tmp); err != nil {
		return fmt.Errorf("bad .aix: %w", err)
	}
	if inst.Unsupported, err = source.Unsupported(ctx, tmp); err != nil {
		return fmt.Errorf("bad .aix: main.wasm: %w", err)
	}
	if err := store.WriteJSON(filepath.Join(tmp, "kinkoku.json"), inst); err != nil {
		return err
	}
	// keep the user's settings across updates
	if b, err := os.ReadFile(filepath.Join(dir, "defaults.json")); err == nil {
		os.WriteFile(filepath.Join(tmp, "defaults.json"), b, 0o644)
	}
	old := dir + ".old"
	os.RemoveAll(old)
	if err := os.Rename(dir, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.Rename(old, dir)
		return err
	}
	return os.RemoveAll(old)
}

type InstalledSource struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Version     int              `json:"version"`
	Languages   []string         `json:"languages"`
	Listings    []source.Listing `json:"listings"`
	Unsupported []string         `json:"unsupported,omitempty"`
	Supported   bool             `json:"supported"`
}

func (m *Manager) List() ([]InstalledSource, error) {
	dirs, err := os.ReadDir(filepath.Join(m.Data, "sources"))
	if errors.Is(err, os.ErrNotExist) {
		return []InstalledSource{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []InstalledSource{}
	for _, d := range dirs {
		if !d.IsDir() || strings.HasSuffix(d.Name(), ".tmp") || strings.HasSuffix(d.Name(), ".old") {
			continue
		}
		dir := filepath.Join(m.Data, "sources", d.Name())
		info, err := source.ReadInfo(dir)
		if err != nil {
			continue
		}
		var inst Installed
		store.ReadJSON(filepath.Join(dir, "kinkoku.json"), &inst)
		ls := info.Listings
		if ls == nil {
			ls = []source.Listing{}
		}
		out = append(out, InstalledSource{
			ID: info.Info.ID, Name: info.Info.Name, Version: info.Info.Version,
			Languages: info.Info.Languages, Listings: ls,
			Unsupported: inst.Unsupported, Supported: len(inst.Unsupported) == 0,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *Manager) Update(ctx context.Context) (map[string]any, error) {
	rs, err := m.Repos()
	if err != nil {
		return nil, err
	}
	errs := []string{}
	for i, r := range rs {
		nr, err := m.fetch(r.URL)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		rs[i] = nr
	}
	if err := store.WriteJSON(m.reposPath(), rs); err != nil {
		return nil, err
	}
	installed, err := m.List()
	if err != nil {
		return nil, err
	}
	updated := []string{}
	for _, s := range installed {
		for _, r := range rs {
			for _, e := range r.Sources {
				if e.ID == s.ID && e.Version > s.Version {
					if _, err := m.install(ctx, r, e); err != nil {
						errs = append(errs, s.ID+": "+err.Error())
					} else {
						updated = append(updated, s.ID)
					}
				}
			}
		}
	}
	return map[string]any{"updated": updated, "errors": errs}, nil
}
