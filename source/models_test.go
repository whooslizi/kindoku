package source

import (
	"reflect"
	"testing"

	"github.com/whooslizi/kinkoku/abi"
)

// Same case as AidokuRunner's ModelCodingTests.testMangaCoding, plus a chapter.
func TestMangaCoding(t *testing.T) {
	n, ts := float32(12.5), int64(1700000000)
	m := Manga{
		Key: "1", Title: "Manga 1", Authors: []string{"Author"}, Description: "Description",
		Tags: []string{"Tag"}, Status: 1, ContentRating: 1, Viewer: 4,
		Chapters: []Chapter{{Key: "c1", Title: "Ch", Number: &n, Date: &ts, Scanlators: []string{}, Locked: true}},
	}
	var e abi.Enc
	m.encode(&e)
	d := abi.Dec{B: e.B}
	got := decodeManga(&d)
	if d.Err != nil || len(d.B) != 0 {
		t.Fatalf("decode err %v, %d bytes left", d.Err, len(d.B))
	}
	if !reflect.DeepEqual(got, m) {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, m)
	}
}

func TestFilterEncoding(t *testing.T) {
	var e abi.Enc
	f := FilterValue{Type: "sort", ID: "s", Index: 2, Ascending: true}
	if err := f.encode(&e); err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 1, 's', 4, 1}; !reflect.DeepEqual(e.B, want) {
		t.Fatalf("sort filter = % x, want % x", e.B, want)
	}
	if (&FilterValue{Type: "bogus"}).encode(&e) == nil {
		t.Fatal("unknown filter type must fail")
	}
}
