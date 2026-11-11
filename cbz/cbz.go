// Package cbz writes chapters as CBZ archives with a ComicInfo.xml.
package cbz

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const maxNameBytes = 200

// Sanitize makes a FAT32-safe file name of at most 200 bytes.
func Sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteByte('_')
		case r == utf8.RuneError:
		default:
			b.WriteRune(r)
		}
	}
	s := strings.Trim(b.String(), " .")
	for len(s) > maxNameBytes {
		_, size := utf8.DecodeLastRuneInString(s)
		s = strings.TrimRight(s[:len(s)-size], " .")
	}
	if s == "" {
		return "_"
	}
	return s
}

// ChapterName is "<NNN.N> - <Title>" (either part may be missing), without the extension.
func ChapterName(number *float32, title, key string) string {
	var parts []string
	if number != nil {
		parts = append(parts, fmt.Sprintf("%05.1f", *number))
	}
	if title != "" {
		parts = append(parts, title)
	}
	if len(parts) == 0 {
		parts = append(parts, key)
	}
	// leave room for a " [xxxxxx]" collision suffix and ".cbz"
	return trimBytes(Sanitize(strings.Join(parts, " - ")), maxNameBytes-14)
}

func trimBytes(s string, n int) string {
	for len(s) > n {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return strings.TrimRight(s, " .")
}

type ComicInfo struct {
	XMLName   xml.Name `xml:"ComicInfo"`
	Series    string   `xml:"Series"`
	Number    string   `xml:"Number,omitempty"`
	Title     string   `xml:"Title,omitempty"`
	PageCount int      `xml:"PageCount"`
	Web       string   `xml:"Web,omitempty"`
}

// FlatName is ChapterName prefixed with the manga title, for downloads saved without manga
// folders. The title is shortened first so the chapter part always survives.
func FlatName(manga string, number *float32, title, key string) string {
	return trimBytes(Sanitize(trimBytes(Sanitize(manga), maxNameBytes/2)+" - "+ChapterName(number, title, key)), maxNameBytes-14)
}

// FormatNumber renders a chapter number without trailing zeros ("12", "12.5").
func FormatNumber(n *float32) string {
	if n == nil {
		return ""
	}
	return strconv.FormatFloat(float64(*n), 'f', -1, 32)
}

// Writer builds path+".tmp" and renames it to path on Close.
type Writer struct {
	path  string
	f     *os.File
	z     *zip.Writer
	pages int
}

func Create(path string) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return nil, err
	}
	return &Writer{path: path, f: f, z: zip.NewWriter(f)}, nil
}

func (w *Writer) AddPage(name string, data []byte) error {
	fw, err := w.z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: time.Now()})
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	w.pages++
	return err
}

func (w *Writer) Pages() int { return w.pages }

// Close writes ComicInfo.xml, syncs and renames into place, returning the final size.
func (w *Writer) Close(info ComicInfo) (int64, error) {
	info.PageCount = w.pages
	fw, err := w.z.CreateHeader(&zip.FileHeader{Name: "ComicInfo.xml", Method: zip.Deflate, Modified: time.Now()})
	if err == nil {
		fw.Write([]byte(xml.Header))
		enc := xml.NewEncoder(fw)
		enc.Indent("", "  ")
		err = enc.Encode(info)
	}
	if err == nil {
		err = w.z.Close()
	}
	if err == nil {
		err = w.f.Sync()
	}
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(w.f.Name())
		return 0, err
	}
	st, err := os.Stat(w.f.Name())
	if err != nil {
		return 0, err
	}
	return st.Size(), os.Rename(w.f.Name(), w.path)
}

func (w *Writer) Abort() {
	w.f.Close()
	os.Remove(w.f.Name())
}
