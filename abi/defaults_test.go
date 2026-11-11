package abi

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tetratelabs/wazero/api"
)

func TestDefaultsGetSet(t *testing.T) {
	h, g := newGuest(t)
	path := filepath.Join(t.TempDir(), "defaults.json")
	d, err := LoadDefaults(path)
	if err != nil {
		t.Fatal(err)
	}
	h.Defaults = d
	d.Register("lang", "en")
	get := fn[fnMemStr](t, h.defaults(), "get")
	set := fn[func(ctx context.Context, m api.Module, kp, kl, kind, vp int32) int32](t, h.defaults(), "set")
	getKey := func(k string) int32 { p, n := g.str(k); return get(bg, g.m, p, n) }

	if r := getKey("missing"); r != defInvalidValue {
		t.Fatalf("get(missing) = %d", r)
	}
	if r := getKey("lang"); r < 0 || string(h.Get(r).([]byte)) != "\x02en" {
		t.Fatalf("get(registered) = %d %v", r, h.Get(r))
	}

	enc := func(f func(e *Enc)) []byte { var e Enc; f(&e); return e.B }
	cases := []struct {
		kind int32
		val  any
		data []byte
	}{
		{kindBool, true, enc(func(e *Enc) { e.Bool(true) })},
		{kindInt, int32(-7), enc(func(e *Enc) { e.Varint(-7) })},
		{kindFloat, float32(2), enc(func(e *Enc) { e.F32(2) })},
		{kindString, "vi", enc(func(e *Enc) { e.Str("vi") })},
		{kindStringArray, []string{"a", "b"}, enc(func(e *Enc) { e.Strs([]string{"a", "b"}) })},
		{kindData, []byte{9, 8}, []byte{9, 8}},
	}
	for i, c := range cases {
		key := string(rune('a' + i))
		kp, kl := g.str(key)
		if r := set(bg, g.m, kp, kl, c.kind, g.result(c.data)); r != 0 {
			t.Fatalf("set kind %d = %d", c.kind, r)
		}
		r := getKey(key)
		if r < 0 || !reflect.DeepEqual(h.Get(r), c.data) {
			t.Fatalf("get kind %d returned %v, want %v", c.kind, h.Get(r), c.data)
		}
	}

	// values survive a reload with their kinds (2.0 stays a float)
	d2, err := LoadDefaults(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range cases {
		if got := d2.Get(string(rune('a' + i))); !reflect.DeepEqual(got, c.val) {
			t.Errorf("reloaded %T %v, want %T %v", got, got, c.val, c.val)
		}
	}

	kp, kl := g.str("a")
	if r := set(bg, g.m, kp, kl, kindNull, 0); r != 0 || getKey("a") != defInvalidValue {
		t.Fatalf("null did not delete the key")
	}
	if r := set(bg, g.m, kp, kl, 42, g.result([]byte{1})); r != defInvalidValue {
		t.Fatalf("bad kind = %d", r)
	}
	if r := get(bg, g.m, -1, 1); r != defInvalidKey {
		t.Fatalf("bad key pointer = %d", r)
	}
}

func TestDefaultsSetString(t *testing.T) {
	d, _ := LoadDefaults("")
	d.Register("on", false)
	d.Register("n", int32(1))
	d.Register("langs", []string{"en"})
	d.Register("url", "x")
	for k, v := range map[string]string{"on": "true", "n": "5", "langs": `["vi"]`, "url": "123", "new": "2.5"} {
		if err := d.SetString(k, v); err != nil {
			t.Fatal(k, err)
		}
	}
	want := map[string]any{"on": true, "n": int32(5), "langs": []string{"vi"}, "url": "123", "new": float32(2.5)}
	for k, v := range want {
		if got := d.Get(k); !reflect.DeepEqual(got, v) {
			t.Errorf("%s = %T %v, want %T %v", k, got, got, v, v)
		}
	}
	if d.SetString("on", "maybe") == nil {
		t.Error("expected a bool parse error")
	}
}
