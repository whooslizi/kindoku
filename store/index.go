package store

import (
	"path/filepath"
	"time"
)

// Entry is one downloaded chapter. RelPath is relative to the download folder.
type Entry struct {
	Source     string `json:"source"`
	MangaKey   string `json:"mangaKey"`
	ChapterKey string `json:"chapterKey"`
	RelPath    string `json:"relPath"`
	Pages      int    `json:"pages"`
	Bytes      int64  `json:"bytes"`
	TS         int64  `json:"ts"`
}

type Index struct {
	path    string
	Entries []Entry
}

func LoadIndex(dataDir string) (*Index, error) {
	ix := &Index{path: filepath.Join(dataDir, "index.json")}
	if err := ReadJSON(ix.path, &ix.Entries); err != nil {
		return nil, err
	}
	if ix.Entries == nil {
		ix.Entries = []Entry{}
	}
	return ix, nil
}

func (ix *Index) Save() error { return WriteJSON(ix.path, ix.Entries) }

func (ix *Index) Find(source, manga, chapter string) *Entry {
	for i := range ix.Entries {
		e := &ix.Entries[i]
		if e.Source == source && e.MangaKey == manga && e.ChapterKey == chapter {
			return e
		}
	}
	return nil
}

func (ix *Index) Put(e Entry) {
	if e.TS == 0 {
		e.TS = time.Now().Unix()
	}
	if old := ix.Find(e.Source, e.MangaKey, e.ChapterKey); old != nil {
		*old = e
		return
	}
	ix.Entries = append(ix.Entries, e)
}

func (ix *Index) Remove(relPath string) bool {
	for i, e := range ix.Entries {
		if e.RelPath == relPath {
			ix.Entries = append(ix.Entries[:i], ix.Entries[i+1:]...)
			return true
		}
	}
	return false
}
