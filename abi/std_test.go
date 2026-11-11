package abi

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"
)

func TestStdBuffers(t *testing.T) {
	h, g := newGuest(t)
	std := h.std()
	bufLen := fn[fnRid](t, std, "buffer_len")
	read := fn[fnMemRid](t, std, "read_buffer")

	s := h.Put("héllo")
	b := h.Put([]byte{1, 2, 3})
	if n := bufLen(s); n != 6 {
		t.Fatalf("buffer_len(string) = %d, want 6", n)
	}
	if n := bufLen(b); n != 3 {
		t.Fatalf("buffer_len(bytes) = %d, want 3", n)
	}
	if n := bufLen(999); n != stdInvalidDescriptor {
		t.Fatalf("buffer_len(bad) = %d", n)
	}
	h.Put(3.5)
	if n := bufLen(h.Put(3.5)); n != stdInvalidDescriptor {
		t.Fatalf("buffer_len(non-buffer) = %d", n)
	}

	p := g.bytes(make([]byte, 16))
	if r := read(bg, g.m, s, p, 6); r != 0 || string(g.read(p, 6)) != "héllo" {
		t.Fatalf("read_buffer = %d %q", r, g.read(p, 6))
	}
	if r := read(bg, g.m, b, p, 2); r != 0 || g.read(p, 2)[1] != 2 {
		t.Fatalf("partial read_buffer = %d", r)
	}
	if r := read(bg, g.m, b, p, 4); r != stdInvalidBufferSize {
		t.Fatalf("oversized read_buffer = %d", r)
	}
	if r := read(bg, g.m, b, 1<<20, 3); r != stdFailedMemoryWrite {
		t.Fatalf("out-of-bounds read_buffer = %d", r)
	}
}

func TestStdDestroyResetsDescriptors(t *testing.T) {
	h, _ := newGuest(t)
	destroy := fn[func(int32)](t, h.std(), "destroy")
	a, b := h.Put("a"), h.Put("b")
	if a != 1 || b != 2 {
		t.Fatalf("descriptors start at 1: got %d, %d", a, b)
	}
	destroy(a)
	destroy(b)
	if c := h.Put("c"); c != 1 {
		t.Fatalf("descriptor after emptying store = %d, want 1", c)
	}
}

func TestStdDates(t *testing.T) {
	h, g := newGuest(t)
	std := h.std()
	now := fn[func() float64](t, std, "current_date")()
	if d := math.Abs(now - float64(time.Now().Unix())); d > 5 {
		t.Fatalf("current_date off by %vs", d)
	}
	_, off := time.Now().Zone()
	if got := fn[func() int64](t, std, "utc_offset")(); got != -int64(off) {
		t.Fatalf("utc_offset = %d, want %d", got, -off)
	}

	parse := fn[func(context.Context, api.Module, int32, int32, int32, int32, int32, int32, int32, int32) float64](t, std, "parse_date")
	call := func(s, f, loc, tz string) float64 {
		sp, sl := g.str(s)
		fp, fl := g.str(f)
		lp, ll := g.str(loc)
		tp, tl := g.str(tz)
		return parse(bg, g.m, sp, sl, fp, fl, lp, ll, tp, tl)
	}
	utc := func(y int, mo time.Month, d, hh, mm, ss int) float64 {
		return float64(time.Date(y, mo, d, hh, mm, ss, 0, time.UTC).Unix())
	}
	cases := []struct {
		s, f, loc, tz string
		want          float64
	}{
		{"2024-03-05", "yyyy-MM-dd", "", "UTC", utc(2024, 3, 5, 0, 0, 0)},
		{"2024-03-05T10:20:30Z", "yyyy-MM-dd'T'HH:mm:ss'Z'", "en_US_POSIX", "UTC", utc(2024, 3, 5, 10, 20, 30)},
		{"2024-03-05T10:20:30.123Z", "yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", "", "UTC", utc(2024, 3, 5, 10, 20, 30) + 0.123},
		{"2024-03-05T10:20:30", "yyyy-MM-ddTHH:mm:ss", "", "UTC", utc(2024, 3, 5, 10, 20, 30)},
		{"2024-03-05 10:20:30+0700", "yyyy-MM-dd HH:mm:ssZZZ", "", "", utc(2024, 3, 5, 3, 20, 30)},
		{"2024-03-05T10:20:30.123456+02:00", "yyyy-MM-dd'T'HH:mm:ss.SSSSSSXXX", "", "", utc(2024, 3, 5, 8, 20, 30) + 0.123456},
		{"July 17, 2025", "MMM d, yyyy", "en_US", "UTC", utc(2025, 7, 17, 0, 0, 0)},
		{"Jul 7, 2025", "MMM d, yyyy", "en_US", "UTC", utc(2025, 7, 7, 0, 0, 0)},
		{"July 17, 2025 12:00 pm", "MMMM d, yyyy h:mm a", "en_US", "UTC", utc(2025, 7, 17, 12, 0, 0)},
		{"5 de marzo de 2024", "d 'de' MMMM 'de' yyyy", "es_ES", "UTC", utc(2024, 3, 5, 0, 0, 0)},
		{"05 marzo 2024", "dd MMMM yyyy", "it", "UTC", utc(2024, 3, 5, 0, 0, 0)},
		{"24.03.05", "yy.MM.dd", "", "UTC", utc(2024, 3, 5, 0, 0, 0)},
		{"05.03.2024", "dd.MM.yyyy", "", "Asia/Taipei", utc(2024, 3, 4, 16, 0, 0)},
	}
	for _, c := range cases {
		if got := call(c.s, c.f, c.loc, c.tz); math.Abs(got-c.want) > 1e-3 {
			t.Errorf("parse_date(%q, %q) = %f, want %f", c.s, c.f, got, c.want)
		}
	}
	if got := call("5 minutes ago", "yyyy-MM-dd", "", ""); got != stdInvalidDateString {
		t.Errorf("unparseable date = %f, want %d", got, stdInvalidDateString)
	}
	if got := parse(bg, g.m, -1, 3, 0, 1, 0, 0, 0, 0); got != stdInvalidString {
		t.Errorf("bad pointer = %f, want %d", got, stdInvalidString)
	}
}

func TestEnvPrint(t *testing.T) {
	h, g := newGuest(t)
	var got string
	h.Log = func(s string) { got = s }
	p, n := g.str("hi there")
	fn[func(context.Context, api.Module, int32, int32)](t, h.env(), "print")(bg, g.m, p, n)
	if got != "hi there" {
		t.Fatalf("print logged %q", got)
	}
}

func TestStubLogsAndFails(t *testing.T) {
	h, _ := newGuest(t)
	var got string
	h.Log = func(s string) { got = s }
	stack := []uint64{0, 0}
	h.stub("js.context_create", []api.ValueType{api.ValueTypeI32})(bg, nil, stack)
	if got != "unimplemented: js.context_create" || api.DecodeI32(stack[0]) != -1 {
		t.Fatalf("stub logged %q, returned %d", got, api.DecodeI32(stack[0]))
	}
	h.stub("canvas.get_image_width", []api.ValueType{api.ValueTypeF32})(bg, nil, stack)
	if api.DecodeF32(stack[0]) != -1 {
		t.Fatalf("f32 stub returned %v", api.DecodeF32(stack[0]))
	}
}
