package abi

import (
	"context"
	"encoding/binary"
	"net/http"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// memWasm is a minimal module that only exports one page of memory as "mem".
var memWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic, version
	0x05, 0x03, 0x01, 0x00, 0x01, // memory section: min 1 page
	0x07, 0x07, 0x01, 0x03, 'm', 'e', 'm', 0x02, 0x00, // export "mem"
}

// guest gives tests a memory to call host functions with, plus a bump allocator.
type guest struct {
	t    *testing.T
	m    api.Module
	next uint32
}

func newGuest(t *testing.T) (*Host, *guest) {
	t.Helper()
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	t.Cleanup(func() { r.Close(ctx) })
	m, err := r.Instantiate(ctx, memWasm)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := LoadDefaults("")
	h := NewHost("test", d, http.DefaultClient)
	h.Log = func(s string) { t.Log(s) }
	return h, &guest{t: t, m: m, next: 1024}
}

// str writes s and returns (ptr, len) as the guest would pass them.
func (g *guest) str(s string) (int32, int32) { return g.bytes([]byte(s)), int32(len(s)) }

func (g *guest) bytes(b []byte) int32 {
	p := g.next
	if !g.m.Memory().Write(p, b) {
		g.t.Fatal("memory write failed")
	}
	g.next += uint32(len(b)+7) &^ 7
	return int32(p)
}

// result writes an aidoku-rs encoded buffer ([len][cap][payload]) and returns its pointer.
func (g *guest) result(payload []byte) int32 {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(payload)+8))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(payload)+8))
	return g.bytes(append(b, payload...))
}

func (g *guest) read(p int32, n int) []byte {
	b, _ := g.m.Memory().Read(uint32(p), uint32(n))
	return b
}

var bg = context.Background()

// fn fetches a host function with its concrete signature.
func fn[F any](t *testing.T, mod map[string]any, name string) F {
	t.Helper()
	f, ok := mod[name].(F)
	if !ok {
		t.Fatalf("%s: unexpected signature %T", name, mod[name])
	}
	return f
}

// str reads a descriptor that should hold a string.
func strOf(t *testing.T, h *Host, rid int32) string {
	t.Helper()
	if rid < 0 {
		t.Fatalf("expected a descriptor, got error %d", rid)
	}
	s, ok := h.Get(rid).(string)
	if !ok {
		t.Fatalf("descriptor %d holds %T, not a string", rid, h.Get(rid))
	}
	return s
}

type (
	fnRid      = func(int32) int32
	fnMemStr   = func(context.Context, api.Module, int32, int32) int32
	fnMemRid   = func(context.Context, api.Module, int32, int32, int32) int32
	fnMemRid2  = func(context.Context, api.Module, int32, int32, int32, int32, int32) int32
	fnMemParse = func(context.Context, api.Module, int32, int32, int32, int32) int32
)
