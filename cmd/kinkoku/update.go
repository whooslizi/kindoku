package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const releasesURL = "https://api.github.com/repos/whooslizi/kindoku/releases/latest"

// arch is set at build time (armv6, armv7, arm64, x64) and picks the release asset.
var arch = ""

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// update reports the latest release; with --plugin <dir> it also installs it over that plugin folder.
func (x *app) update(ctx context.Context, a args) (any, error) {
	var rel release
	if err := x.get(ctx, releasesURL, func(b []byte) error { return json.Unmarshal(b, &rel) }); err != nil {
		return nil, fmt.Errorf("checking for updates: %w", err)
	}
	out := map[string]any{"current": version, "latest": rel.Tag, "available": rel.Tag != "" && rel.Tag != version}
	dir := a.flags["plugin"]
	if dir == "" || out["available"] != true {
		return out, nil
	}
	if arch == "" {
		return nil, errors.New("this is a dev build; install a release zip by hand")
	}
	name := "kinkoku-" + arch + ".koplugin.zip"
	url := ""
	for _, as := range rel.Assets {
		if as.Name == name {
			url = as.URL
		}
	}
	if url == "" {
		return nil, fmt.Errorf("release %s has no %s", rel.Tag, name)
	}
	if err := x.get(ctx, url, func(b []byte) error { return installPlugin(b, dir) }); err != nil {
		return nil, err
	}
	out["installed"] = rel.Tag
	return out, nil
}

func (x *app) get(ctx context.Context, url string, use func([]byte) error) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := x.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return use(b)
}

// installPlugin writes every kinkoku.koplugin/* file of the zip into dir, each via tmp + rename.
func installPlugin(b []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return err
	}
	n := 0
	for _, f := range zr.File {
		rel, ok := strings.CutPrefix(f.Name, "kinkoku.koplugin/")
		if !ok || f.FileInfo().IsDir() || !filepath.IsLocal(rel) {
			continue
		}
		if err := extract(f, filepath.Join(dir, rel)); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return errors.New("update zip has no kinkoku.koplugin folder")
	}
	return nil
}

func extract(f *zip.File, path string) error {
	r, err := f.Open()
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	w, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		os.Chmod(tmp, f.Mode().Perm()|0o644) // best-effort: FAT32 refuses chmod
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
