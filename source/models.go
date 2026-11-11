package source

import (
	"encoding/json"
	"fmt"

	"github.com/whooslizi/kinkoku/abi"
)

// Models mirror aidoku-rs structs; field order is the postcard wire order. Empty strings and nil
// slices encode as None.

type Manga struct {
	Key            string    `json:"key"`
	Title          string    `json:"title"`
	Cover          string    `json:"cover,omitempty"`
	Artists        []string  `json:"artists,omitempty"`
	Authors        []string  `json:"authors,omitempty"`
	Description    string    `json:"description,omitempty"`
	URL            string    `json:"url,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	Status         uint8     `json:"status"`        // 0 unknown, 1 ongoing, 2 completed, 3 cancelled, 4 hiatus
	ContentRating  uint8     `json:"contentRating"` // 0 unknown, 1 safe, 2 suggestive, 3 nsfw
	Viewer         uint8     `json:"viewer"`        // 0 unknown, 1 ltr, 2 rtl, 3 vertical, 4 webtoon
	UpdateStrategy uint8     `json:"-"`
	NextUpdateTime *int64    `json:"-"`
	Chapters       []Chapter `json:"chapters,omitempty"`
}

type Chapter struct {
	Key        string   `json:"key"`
	Title      string   `json:"title,omitempty"`
	Number     *float32 `json:"number,omitempty"`
	Volume     *float32 `json:"volume,omitempty"`
	Date       *int64   `json:"date,omitempty"`
	Scanlators []string `json:"scanlators,omitempty"`
	URL        string   `json:"url,omitempty"`
	Language   string   `json:"language,omitempty"`
	Thumbnail  string   `json:"-"`
	Locked     bool     `json:"locked,omitempty"`
}

type MangaPageResult struct {
	Entries     []Manga `json:"entries"`
	HasNextPage bool    `json:"hasNextPage"`
}

type Listing struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind uint8  `json:"kind,omitempty"`
}

// UnmarshalJSON accepts source.json's short form, a bare listing name.
func (l *Listing) UnmarshalJSON(b []byte) error {
	var name string
	if json.Unmarshal(b, &name) == nil {
		*l = Listing{ID: name, Name: name}
		return nil
	}
	type plain Listing
	var p struct {
		plain
		Kind any `json:"kind"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*l = Listing(p.plain)
	if l.Name == "" {
		l.Name = l.ID
	}
	if k, ok := p.Kind.(float64); ok {
		l.Kind = uint8(k)
	}
	return nil
}

const (
	PageURL = iota
	PageText
	PageImage
	PageZip
)

type Page struct {
	Kind    int
	URL     string            // PageURL, or zip URL
	Context map[string]string // PageURL only, nil = None
	Text    string            // PageText, or zip file path
	Image   int32             // PageImage descriptor
}

// FilterValue is what a search sends; JSON form: {"type":"text|sort|check|select|multiselect|range","id":...}.
type FilterValue struct {
	Type      string   `json:"type"`
	ID        string   `json:"id"`
	Value     any      `json:"value,omitempty"` // text/select: string, check: number
	Index     int32    `json:"index,omitempty"`
	Ascending bool     `json:"ascending,omitempty"`
	Included  []string `json:"included,omitempty"`
	Excluded  []string `json:"excluded,omitempty"`
	From      *float32 `json:"from,omitempty"`
	To        *float32 `json:"to,omitempty"`
}

func optStr(e *abi.Enc, s string) {
	e.Bool(s != "")
	if s != "" {
		e.Str(s)
	}
}

func optF32(e *abi.Enc, f *float32) {
	e.Bool(f != nil)
	if f != nil {
		e.F32(*f)
	}
}

func (m *Manga) encode(e *abi.Enc) {
	e.Str(m.Key)
	e.Str(m.Title)
	optStr(e, m.Cover)
	e.OptStrs(m.Artists)
	e.OptStrs(m.Authors)
	optStr(e, m.Description)
	optStr(e, m.URL)
	e.OptStrs(m.Tags)
	e.Uvarint(uint64(m.Status))
	e.Uvarint(uint64(m.ContentRating))
	e.Uvarint(uint64(m.Viewer))
	e.Uvarint(uint64(m.UpdateStrategy))
	e.Bool(m.NextUpdateTime != nil)
	if m.NextUpdateTime != nil {
		e.Varint(*m.NextUpdateTime)
	}
	e.Bool(m.Chapters != nil)
	if m.Chapters != nil {
		e.Uvarint(uint64(len(m.Chapters)))
		for i := range m.Chapters {
			m.Chapters[i].encode(e)
		}
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func decodeManga(d *abi.Dec) Manga {
	m := Manga{Key: d.Str(), Title: d.Str()}
	m.Cover = derefStr(d.OptStr())
	m.Artists = d.OptStrs()
	m.Authors = d.OptStrs()
	m.Description = derefStr(d.OptStr())
	m.URL = derefStr(d.OptStr())
	m.Tags = d.OptStrs()
	m.Status = uint8(d.Uvarint())
	m.ContentRating = uint8(d.Uvarint())
	m.Viewer = uint8(d.Uvarint())
	m.UpdateStrategy = uint8(d.Uvarint())
	if d.Opt() {
		t := d.Varint()
		m.NextUpdateTime = &t
	}
	if d.Opt() {
		n := d.Len()
		m.Chapters = make([]Chapter, 0, n)
		for i := 0; i < n && d.Err == nil; i++ {
			m.Chapters = append(m.Chapters, decodeChapter(d))
		}
	}
	return m
}

func (c *Chapter) encode(e *abi.Enc) {
	e.Str(c.Key)
	optStr(e, c.Title)
	optF32(e, c.Number)
	optF32(e, c.Volume)
	e.Bool(c.Date != nil)
	if c.Date != nil {
		e.Varint(*c.Date)
	}
	e.OptStrs(c.Scanlators)
	optStr(e, c.URL)
	optStr(e, c.Language)
	optStr(e, c.Thumbnail)
	e.Bool(c.Locked)
}

func decodeChapter(d *abi.Dec) Chapter {
	c := Chapter{Key: d.Str(), Title: derefStr(d.OptStr())}
	if d.Opt() {
		f := d.F32()
		c.Number = &f
	}
	if d.Opt() {
		f := d.F32()
		c.Volume = &f
	}
	if d.Opt() {
		t := d.Varint()
		c.Date = &t
	}
	c.Scanlators = d.OptStrs()
	c.URL = derefStr(d.OptStr())
	c.Language = derefStr(d.OptStr())
	c.Thumbnail = derefStr(d.OptStr())
	c.Locked = d.Bool()
	return c
}

func decodeMangaPage(d *abi.Dec) MangaPageResult {
	n := d.Len()
	r := MangaPageResult{Entries: make([]Manga, 0, n)}
	for i := 0; i < n && d.Err == nil; i++ {
		r.Entries = append(r.Entries, decodeManga(d))
	}
	r.HasNextPage = d.Bool()
	return r
}

func (l *Listing) encode(e *abi.Enc) {
	e.Str(l.ID)
	e.Str(l.Name)
	e.Uvarint(uint64(l.Kind))
}

func decodeListing(d *abi.Dec) Listing {
	return Listing{ID: d.Str(), Name: d.Str(), Kind: uint8(d.Uvarint())}
}

func decodePage(d *abi.Dec) Page {
	var p Page
	p.Kind = int(d.Uvarint())
	switch p.Kind {
	case PageURL:
		p.URL = d.Str()
		if d.Opt() {
			p.Context = d.Map()
		}
	case PageText:
		p.Text = d.Str()
	case PageImage:
		p.Image = int32(d.Varint())
	case PageZip:
		p.URL, p.Text = d.Str(), d.Str()
	default:
		d.Err = fmt.Errorf("unknown page content %d", p.Kind)
	}
	d.OptStr() // thumbnail
	d.Bool()   // has_description
	d.OptStr() // description
	return p
}

func (f *FilterValue) encode(e *abi.Enc) error {
	str := func(v any) string { s, _ := v.(string); return s }
	switch f.Type {
	case "text":
		e.Uvarint(0)
		e.Str(f.ID)
		e.Str(str(f.Value))
	case "sort":
		e.Uvarint(1)
		e.Str(f.ID)
		e.Varint(int64(f.Index))
		e.Bool(f.Ascending)
	case "check":
		n, _ := f.Value.(float64)
		e.Uvarint(2)
		e.Str(f.ID)
		e.Varint(int64(n))
	case "select":
		e.Uvarint(3)
		e.Str(f.ID)
		e.Str(str(f.Value))
	case "multiselect":
		e.Uvarint(4)
		e.Str(f.ID)
		e.Strs(f.Included)
		e.Strs(f.Excluded)
	case "range":
		e.Uvarint(5)
		e.Str(f.ID)
		optF32(e, f.From)
		optF32(e, f.To)
	default:
		return fmt.Errorf("unknown filter type %q", f.Type)
	}
	return nil
}
