package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPlugin(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, mode os.FileMode, body string) {
		h := &zip.FileHeader{Name: name}
		h.SetMode(mode)
		w, _ := zw.CreateHeader(h)
		w.Write([]byte(body))
	}
	add("kinkoku.koplugin/main.lua", 0o644, "new lua")
	add("kinkoku.koplugin/bin/armv7/kinkoku", 0o755, "bin")
	add("kinkoku.koplugin/../evil", 0o644, "x")
	add("other/file", 0o644, "x")
	zw.Close()

	root := t.TempDir()
	dir := filepath.Join(root, "kinkoku.koplugin")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "main.lua"), []byte("old"), 0o644)
	if err := installPlugin(buf.Bytes(), dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "main.lua")); string(b) != "new lua" {
		t.Errorf("main.lua = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "bin/armv7/kinkoku")); err == nil {
		t.Error("the running binary must not be replaced")
	}
	if fi, err := os.Stat(filepath.Join(dir, "bin/armv7/kinkoku.new")); err != nil || fi.Mode()&0o100 == 0 {
		t.Errorf("binary: %v %v", fi, err)
	}
	for _, p := range []string{filepath.Join(root, "evil"), filepath.Join(dir, "other")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s should not exist", p)
		}
	}
	if err := installPlugin([]byte("not a zip"), dir); err == nil {
		t.Error("bad zip accepted")
	}
}
