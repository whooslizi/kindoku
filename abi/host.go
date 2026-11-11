// Package abi implements the host side of the Aidoku source ABI (the imports declared by aidoku-rs),
// modeled on AidokuRunner's behavior. See docs/abi.md.
package abi

import (
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"os"
	"sort"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

type Host struct {
	SourceID string
	Defaults *Defaults
	HTTP     *http.Client
	Log      func(string)
	Ctx      context.Context // cancels in-flight requests (download cancel)

	objs map[int32]any
	next int32
	rate rateLimit
}

func NewHost(sourceID string, defaults *Defaults, client *http.Client) *Host {
	return &Host{
		SourceID: sourceID,
		Defaults: defaults,
		HTTP:     client,
		Ctx:      context.Background(),
		Log:      func(s string) { fmt.Fprintln(os.Stderr, "["+sourceID+"]", s) },
		objs:     map[int32]any{},
		next:     1,
	}
}

func (h *Host) Put(v any) int32 {
	d := h.next
	h.objs[d] = v
	h.next++
	return d
}

func (h *Host) Get(d int32) any    { return h.objs[d] }
func (h *Host) Set(d int32, v any) { h.objs[d] = v }
func (h *Host) Remove(d int32) {
	delete(h.objs, d)
	if len(h.objs) == 0 {
		h.next = 1
	}
}

// Len reports live descriptors (tests use it to catch leaks).
func (h *Host) Len() int { return len(h.objs) }

// readStr reads a guest string; ok is false for a negative pointer/length or out-of-range read.
func readStr(m api.Module, ptr, n int32) (string, bool) {
	b, ok := readBytes(m, ptr, n)
	return string(b), ok
}

func readBytes(m api.Module, ptr, n int32) ([]byte, bool) {
	if ptr < 0 || n < 0 {
		return nil, false
	}
	b, ok := m.Memory().Read(uint32(ptr), uint32(n))
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// ReadResult reads an aidoku-rs encoded buffer: [len u32][cap u32][payload], len includes the header.
func ReadResult(m api.Module, ptr int32) ([]byte, bool) {
	hdr, ok := m.Memory().Read(uint32(ptr), 8)
	if !ok {
		return nil, false
	}
	n := binary.LittleEndian.Uint32(hdr)
	if n < 8 {
		return nil, false
	}
	return readBytes(m, ptr+8, int32(n-8))
}

func (h *Host) modules() map[string]map[string]any {
	return map[string]map[string]any{
		"env":      h.env(),
		"std":      h.std(),
		"net":      h.net(),
		"html":     h.html(),
		"defaults": h.defaults(),
		"canvas":   h.canvas(),
	}
}

// Link instantiates every host module the guest imports. Imports we don't provide become stubs
// that log "unimplemented: module.name" and return -1, so the source fails loudly instead of
// failing to instantiate.
func (h *Host) Link(ctx context.Context, r wazero.Runtime, guest wazero.CompiledModule) error {
	known := h.modules()
	byMod := map[string][]api.FunctionDefinition{}
	for _, f := range guest.ImportedFunctions() {
		mod, _, _ := f.Import()
		byMod[mod] = append(byMod[mod], f)
	}
	mods := make([]string, 0, len(byMod))
	for m := range byMod {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, mod := range mods {
		b := r.NewHostModuleBuilder(mod)
		for _, f := range byMod[mod] {
			_, name, _ := f.Import()
			if fn, ok := known[mod][name]; ok {
				b.NewFunctionBuilder().WithFunc(fn).Export(name)
				continue
			}
			b.NewFunctionBuilder().
				WithGoModuleFunction(h.stub(mod+"."+name, f.ResultTypes()), f.ParamTypes(), f.ResultTypes()).
				Export(name)
		}
		if _, err := b.Instantiate(ctx); err != nil {
			return fmt.Errorf("link %s: %w", mod, err)
		}
	}
	return nil
}

func (h *Host) stub(name string, results []api.ValueType) api.GoModuleFunc {
	return func(_ context.Context, _ api.Module, stack []uint64) {
		h.Log("unimplemented: " + name)
		for i, t := range results {
			switch t {
			case api.ValueTypeF32:
				stack[i] = api.EncodeF32(-1)
			case api.ValueTypeF64:
				stack[i] = api.EncodeF64(-1)
			default:
				stack[i] = api.EncodeI64(-1) // also valid for i32: the low 32 bits are -1
			}
		}
	}
}

// Unsupported lists imports from modules we deliberately don't run (JavaScript / WebView).
func Unsupported(guest wazero.CompiledModule) []string {
	var out []string
	for _, f := range guest.ImportedFunctions() {
		if mod, name, _ := f.Import(); mod == "js" {
			out = append(out, mod+"."+name)
		}
	}
	return out
}
