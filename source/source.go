// Package source loads an unpacked Aidoku source and calls its exports.
package source

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/whooslizi/kinkoku/abi"
)

type Info struct {
	Info struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		AltNames      []string `json:"altNames,omitempty"`
		Version       int      `json:"version"`
		URL           string   `json:"url,omitempty"`
		URLs          []string `json:"urls,omitempty"`
		ContentRating int      `json:"contentRating"`
		Languages     []string `json:"languages"`
		MinAppVersion string   `json:"minAppVersion,omitempty"`
	} `json:"info"`
	Listings []Listing `json:"listings,omitempty"`
	Config   struct {
		LanguageSelectType  string `json:"languageSelectType,omitempty"`
		AllowsBaseURLSelect bool   `json:"allowsBaseUrlSelect,omitempty"`
	} `json:"config"`
}

func ReadInfo(dir string) (*Info, error) {
	b, err := os.ReadFile(filepath.Join(dir, "source.json"))
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, fmt.Errorf("source.json: %w", err)
	}
	if info.Info.ID == "" {
		return nil, errors.New("source.json: missing info.id")
	}
	return &info, nil
}

// Source errors, by the negative codes aidoku-rs returns.
var (
	ErrMissingResult = errors.New("source returned no result")
	ErrUnimplemented = errors.New("source does not implement this")
	sourceErrors     = map[int32]error{
		-2: ErrUnimplemented,
		-3: errors.New("source network error"),
		-4: errors.New("source html error"),
		-5: errors.New("source javascript error"),
		-6: errors.New("source canvas error"),
		-7: errors.New("source utf-8 error"),
		-8: errors.New("source json parse error"),
		-9: errors.New("source deserialize error"),
	}
)

type Source struct {
	Info *Info
	Host *abi.Host
	rt   wazero.Runtime
	mod  api.Module
	mu   sync.Mutex // wasm calls are not reentrant
}

// Open compiles main.wasm, links the host imports, registers settings defaults and runs `start`.
func Open(ctx context.Context, dir string, client *http.Client) (*Source, error) {
	info, err := ReadInfo(dir)
	if err != nil {
		return nil, err
	}
	defs, err := LoadDefaults(dir)
	if err != nil {
		return nil, err
	}
	wasm, err := os.ReadFile(filepath.Join(dir, "main.wasm"))
	if err != nil {
		return nil, err
	}
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(2048)) // 128 MiB
	compiled, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(ctx)
		return nil, err
	}
	s := &Source{Info: info, Host: abi.NewHost(info.Info.ID, defs, client), rt: rt}
	if err := s.Host.Link(ctx, rt, compiled); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	s.mod, err = rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig().WithName(info.Info.ID).WithStartFunctions())
	if err != nil {
		rt.Close(ctx)
		return nil, err
	}
	if f := s.mod.ExportedFunction("start"); f != nil {
		if _, err := f.Call(ctx); err != nil {
			rt.Close(ctx)
			return nil, fmt.Errorf("start: %w", err)
		}
	}
	return s, nil
}

// Unsupported returns the JS/WebView imports of the wasm in dir (empty = runnable).
func Unsupported(ctx context.Context, dir string) ([]string, error) {
	wasm, err := os.ReadFile(filepath.Join(dir, "main.wasm"))
	if err != nil {
		return nil, err
	}
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigInterpreter())
	defer rt.Close(ctx)
	m, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		return nil, err
	}
	return abi.Unsupported(m), nil
}

func (s *Source) Close() { s.rt.Close(context.Background()) }

func (s *Source) Has(export string) bool { return s.mod.ExportedFunction(export) != nil }

// call runs an export and returns its decoded result payload.
func (s *Source) call(name string, args ...int32) ([]byte, error) {
	f := s.mod.ExportedFunction(name)
	if f == nil {
		return nil, fmt.Errorf("%s: %w", name, ErrUnimplemented)
	}
	params := make([]uint64, len(args))
	for i, a := range args {
		params[i] = api.EncodeI32(a)
	}
	res, err := f.Call(context.Background(), params...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	ptr := api.DecodeI32(res[0])
	if ptr < 0 {
		if e, ok := sourceErrors[ptr]; ok {
			return nil, e
		}
		return nil, ErrMissingResult
	}
	mem := s.mod.Memory()
	hdr, ok := mem.Read(uint32(ptr), 12)
	if !ok {
		return nil, ErrMissingResult
	}
	defer s.free(ptr)
	if binary.LittleEndian.Uint32(hdr) == 0xFFFFFFFF { // error message: [-1][cap][len][utf8...]
		n := binary.LittleEndian.Uint32(hdr[8:])
		msg, _ := mem.Read(uint32(ptr)+12, n-12)
		return nil, errors.New(string(msg))
	}
	data, ok := abi.ReadResult(s.mod, ptr)
	if !ok {
		return nil, ErrMissingResult
	}
	return data, nil
}

func (s *Source) free(ptr int32) {
	if f := s.mod.ExportedFunction("free_result"); f != nil {
		f.Call(context.Background(), api.EncodeI32(ptr))
	}
}

func (s *Source) putEncoded(f func(e *abi.Enc)) int32 {
	var e abi.Enc
	f(&e)
	return s.Host.Put(e.B)
}

func decode[T any](data []byte, err error, f func(d *abi.Dec) T) (T, error) {
	var zero T
	if err != nil {
		return zero, err
	}
	d := abi.Dec{B: data}
	v := f(&d)
	if d.Err != nil {
		return zero, d.Err
	}
	return v, nil
}

func (s *Source) Search(query string, page int, filters []FilterValue) (MangaPageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.Host.Put(query)
	defer s.Host.Remove(q)
	var e abi.Enc
	e.Uvarint(uint64(len(filters)))
	for i := range filters {
		if err := filters[i].encode(&e); err != nil {
			return MangaPageResult{}, err
		}
	}
	f := s.Host.Put(e.B)
	defer s.Host.Remove(f)
	data, err := s.call("get_search_manga_list", q, int32(page), f)
	return decode(data, err, decodeMangaPage)
}

func (s *Source) MangaList(l Listing, page int) (MangaPageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.putEncoded(l.encode)
	defer s.Host.Remove(p)
	data, err := s.call("get_manga_list", p, int32(page))
	return decode(data, err, decodeMangaPage)
}

func (s *Source) Listings() ([]Listing, error) {
	if !s.Has("get_listings") {
		return s.Info.Listings, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.call("get_listings")
	dyn, err := decode(data, err, func(d *abi.Dec) []Listing {
		n := d.Len()
		out := make([]Listing, 0, n)
		for i := 0; i < n && d.Err == nil; i++ {
			out = append(out, decodeListing(d))
		}
		return out
	})
	return append(append([]Listing{}, s.Info.Listings...), dyn...), err
}

func (s *Source) MangaUpdate(m Manga, details, chapters bool) (Manga, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.putEncoded(m.encode)
	defer s.Host.Remove(p)
	b := func(v bool) int32 {
		if v {
			return 1
		}
		return 0
	}
	data, err := s.call("get_manga_update", p, b(details), b(chapters))
	return decode(data, err, decodeManga)
}

func (s *Source) PageList(m Manga, c Chapter) ([]Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m.Chapters = nil
	mp := s.putEncoded(m.encode)
	defer s.Host.Remove(mp)
	cp := s.putEncoded(c.encode)
	defer s.Host.Remove(cp)
	data, err := s.call("get_page_list", mp, cp)
	return decode(data, err, func(d *abi.Dec) []Page {
		n := d.Len()
		out := make([]Page, 0, n)
		for i := 0; i < n && d.Err == nil; i++ {
			out = append(out, decodePage(d))
		}
		return out
	})
}

func (s *Source) putContext(ctx map[string]string) int32 {
	if ctx == nil {
		return -1
	}
	return s.putEncoded(func(e *abi.Enc) { e.Map(ctx) })
}

// ImageRequest returns the request for a page image: the source's own if it provides one.
func (s *Source) ImageRequest(url string, ctx map[string]string) (*abi.Request, error) {
	if !s.Has("get_image_request") {
		return &abi.Request{Method: "GET", URL: url, Header: http.Header{}}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.putEncoded(func(e *abi.Enc) { e.Str(url) })
	defer s.Host.Remove(u)
	c := s.putContext(ctx)
	if c >= 0 {
		defer s.Host.Remove(c)
	}
	data, err := s.call("get_image_request", u, c)
	rid, err := decode(data, err, func(d *abi.Dec) int32 { return int32(d.Varint()) })
	if err != nil {
		return nil, err
	}
	defer s.Host.Remove(rid)
	r, ok := s.Host.Get(rid).(*abi.Request)
	if !ok || r.URL == "" {
		return nil, ErrMissingResult
	}
	return r, nil
}

// ProcessPage runs process_page_image on a downloaded image. It returns either the original
// bytes (source passed them through) or a new image.
func (s *Source) ProcessPage(req *abi.Request, status int, hdr http.Header, data []byte, ctx map[string]string) (image.Image, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	img := s.Host.Put(data)
	defer s.Host.Remove(img)
	flat := func(h http.Header) map[string]string {
		m := make(map[string]string, len(h))
		for k, v := range h {
			m[k] = strings.Join(v, ", ")
		}
		return m
	}
	resp := s.putEncoded(func(e *abi.Enc) {
		e.Uvarint(uint64(status))
		e.Map(flat(hdr))
		optStr(e, req.URL)
		e.Map(flat(req.Header))
		e.Varint(int64(img))
	})
	defer s.Host.Remove(resp)
	c := s.putContext(ctx)
	if c >= 0 {
		defer s.Host.Remove(c)
	}
	out, err := s.call("process_page_image", resp, c)
	ref, err := decode(out, err, func(d *abi.Dec) int32 { return int32(d.Varint()) })
	if err != nil {
		return nil, nil, err
	}
	if ref != img {
		defer s.Host.Remove(ref)
	}
	switch v := s.Host.Get(ref).(type) {
	case image.Image:
		return v, nil, nil
	case []byte:
		return nil, v, nil
	}
	return nil, nil, ErrMissingResult
}
