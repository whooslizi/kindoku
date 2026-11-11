//go:build e2e

package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Live test against the Aidoku Community repo: go test -tags e2e ./cmd/kinkoku
// Run it where these sites are reachable (some ISPs block manga hosts).
var e2eSources = []struct{ id, query string }{
	{"en.tcbscans", "one piece"},
	{"en.weebcentral", "berserk"},
	{"en.kingofshojo", "love"},
}

func cli(t *testing.T, data string, args ...string) map[string]any {
	t.Helper()
	out, err := run(context.Background(), append([]string{"--data", data}, args...))
	if err != nil {
		t.Fatalf("kinkoku %v: %v", args, err)
	}
	b, _ := json.Marshal(out)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("kinkoku %v: %v", args, err)
	}
	return m
}

func TestE2E(t *testing.T) {
	data, folder := t.TempDir(), t.TempDir()
	cli(t, data, "repo", "add", "https://aidoku-community.github.io/sources/")
	for _, s := range e2eSources {
		t.Run(s.id, func(t *testing.T) {
			if r := cli(t, data, "repo", "install", s.id); r["supported"] != true {
				t.Fatalf("install: %v", r)
			}
			res := cli(t, data, "search", s.id, s.query)
			entries, _ := res["entries"].([]any)
			if len(entries) == 0 {
				t.Fatalf("no search results: %v", res)
			}
			key := entries[0].(map[string]any)["key"].(string)
			m := cli(t, data, "manga", s.id, key)["manga"].(map[string]any)
			chapters, _ := m["chapters"].([]any)
			if len(chapters) == 0 {
				t.Fatalf("no chapters for %s", key)
			}
			var chKey string
			for _, c := range chapters {
				if c.(map[string]any)["locked"] != true {
					chKey = c.(map[string]any)["key"].(string)
					break
				}
			}
			dl := cli(t, data, "download", s.id, key, chKey, "--out", folder, "--eink")
			ch := dl["chapters"].([]any)[0].(map[string]any)
			if ch["error"] != nil {
				t.Fatalf("download: %v", ch)
			}
			z, err := zip.OpenReader(filepath.Join(folder, ch["relPath"].(string)))
			if err != nil {
				t.Fatal(err)
			}
			z.Close()
			if len(z.File) < 2 {
				t.Fatalf("cbz has %d entries", len(z.File))
			}
			// resume: a second run skips the indexed chapter
			again := cli(t, data, "download", s.id, key, chKey, "--out", folder)
			if again["chapters"].([]any)[0].(map[string]any)["skipped"] != true {
				t.Fatal("second download was not skipped")
			}
		})
	}
	if tmps, _ := filepath.Glob(filepath.Join(folder, "*", "*.tmp")); len(tmps) > 0 {
		t.Fatalf("temp files left: %v", tmps)
	}
}
