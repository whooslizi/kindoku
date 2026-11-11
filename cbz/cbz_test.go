package cbz

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		`Re:Zero / Part 1?`: "Re_Zero _ Part 1_",
		"  .hidden.  ":      "hidden",
		"tab\there":         "tab_here",
		`a<b>c"d|e*f\g`:     "a_b_c_d_e_f_g",
		"":                  "_",
		"...":               "_",
		"Tiếng Việt":        "Tiếng Việt",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("あ", 100) // 300 bytes
	got := Sanitize(long)
	if len(got) > 200 || !utf8.ValidString(got) {
		t.Errorf("long name: %d bytes, valid=%v", len(got), utf8.ValidString(got))
	}
}

func TestChapterName(t *testing.T) {
	n := float32(12.5)
	if got := ChapterName(&n, "The End?", "k"); got != "012.5 - The End_" {
		t.Errorf("got %q", got)
	}
	if got := ChapterName(nil, "", "key/1"); got != "key_1" {
		t.Errorf("got %q", got)
	}
	if got := FormatNumber(&n); got != "12.5" {
		t.Errorf("FormatNumber = %q", got)
	}
}

func TestWriterAtomicWithComicInfo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m", "001.0 - x.cbz")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w.AddPage("001.png", []byte("one"))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("final file must not exist before Close")
	}
	w.AddPage("002.png", []byte("two"))
	size, err := w.Close(ComicInfo{Series: "S & T", Number: "1", Title: "x", Web: "https://e/1"})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if st.Size() != size {
		t.Fatalf("reported %d bytes, file has %d", size, st.Size())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal(".tmp left behind")
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var names []string
	var info ComicInfo
	for _, f := range z.File {
		names = append(names, f.Name)
		if f.Name == "ComicInfo.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			if err := xml.Unmarshal(b, &info); err != nil {
				t.Fatal(err)
			}
		}
	}
	if strings.Join(names, ",") != "001.png,002.png,ComicInfo.xml" {
		t.Fatalf("entries %v", names)
	}
	if info.Series != "S & T" || info.PageCount != 2 || info.Number != "1" || info.Web != "https://e/1" {
		t.Fatalf("ComicInfo %+v", info)
	}
}

func TestAbortRemovesTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.cbz")
	w, _ := Create(path)
	w.AddPage("001.png", []byte("x"))
	w.Abort()
	if m, _ := filepath.Glob(path + "*"); len(m) != 0 {
		t.Fatalf("left %v", m)
	}
}

func TestFlatName(t *testing.T) {
	n := float32(12.5)
	if got := FlatName("One: Piece", &n, "Romance Dawn", "k"); got != "One_ Piece - 012.5 - Romance Dawn" {
		t.Errorf("FlatName = %q", got)
	}
	long := strings.Repeat("Long title ", 40)
	if got := FlatName(long, &n, "", "k"); !strings.HasSuffix(got, " - 012.5") || len(got) > maxNameBytes-14 {
		t.Errorf("long title lost the chapter part: %q", got)
	}
}
