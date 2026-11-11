// Package store keeps kinkoku's state as plain files written atomically (tmp + rename).
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// WriteAtomic writes b to path via a temp file in the same directory, then renames it.
func WriteAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	f.Chmod(0o644) // CreateTemp makes 0600 files; best-effort since FAT32 (/mnt/us) refuses chmod
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

func WriteJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, b)
}

// ReadJSON decodes path into v; a missing file leaves v untouched and is not an error.
func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
