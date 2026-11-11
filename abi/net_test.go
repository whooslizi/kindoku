package abi

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tetratelabs/wazero/api"
)

func TestNet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/page":
			w.Header().Set("X-Test", "yes")
			w.Write([]byte(`<html><body><a href="/x">X</a></body></html>`))
		case "/echo":
			b, _ := io.ReadAll(r.Body)
			w.Write([]byte(r.Method + " " + r.Header.Get("X-Key") + " " + string(b)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	h, g := newGuest(t)
	net := h.net()
	initReq := fn[fnRid](t, net, "init")
	setURL := fn[fnMemRid](t, net, "set_url")
	setHeader := fn[fnMemRid2](t, net, "set_header")
	setBody := fn[fnMemRid](t, net, "set_body")
	send := fn[fnRid](t, net, "send")
	dataLen := fn[fnRid](t, net, "data_len")
	readData := fn[fnMemRid](t, net, "read_data")
	status := fn[fnRid](t, net, "get_status_code")
	getURL := fn[fnRid](t, net, "get_url")
	getHeader := fn[fnMemRid](t, net, "get_header")
	toHTML := fn[fnRid](t, net, "html")
	newReq := func(method int32, url string) int32 {
		r := initReq(method)
		p, n := g.str(url)
		if c := setURL(bg, g.m, r, p, n); c != 0 {
			t.Fatalf("set_url = %d", c)
		}
		return r
	}

	if r := initReq(99); r != netInvalidMethod {
		t.Fatalf("init(99) = %d", r)
	}
	if r := send(initReq(0)); r != netMissingURL {
		t.Fatalf("send without url = %d", r)
	}
	if r := status(initReq(0)); r != netMissingResponse {
		t.Fatalf("status before send = %d", r)
	}

	// GET with redirect: status, final URL, headers, body, html
	r := newReq(0, srv.URL+"/redirect")
	if c := send(r); c != 0 {
		t.Fatalf("send = %d", c)
	}
	if s := status(r); s != 200 {
		t.Fatalf("status = %d", s)
	}
	if u := strOf(t, h, getURL(r)); u != srv.URL+"/page" {
		t.Fatalf("get_url = %q", u)
	}
	kp, kn := g.str("x-test")
	if v := strOf(t, h, getHeader(bg, g.m, r, kp, kn)); v != "yes" {
		t.Fatalf("get_header = %q", v)
	}
	kp, kn = g.str("X-Missing")
	if c := getHeader(bg, g.m, r, kp, kn); c != netMissingData {
		t.Fatalf("missing header = %d", c)
	}
	n := dataLen(r)
	buf := g.bytes(make([]byte, n))
	if c := readData(bg, g.m, r, buf, n); c != 0 || string(g.read(buf, 5)) != "<html" {
		t.Fatalf("read_data = %d %q", c, g.read(buf, 5))
	}
	if c := readData(bg, g.m, r, buf, n+1); c != netInvalidBufferSize {
		t.Fatalf("oversized read_data = %d", c)
	}
	doc, ok := h.Get(toHTML(r)).(*Node)
	if !ok || *doc.Base != srv.URL+"/page" {
		t.Fatalf("html() = %v", h.Get(toHTML(r)))
	}

	// POST with header and body
	r = newReq(1, srv.URL+"/echo")
	kp, kn = g.str("X-Key")
	vp, vn := g.str("k1")
	if c := setHeader(bg, g.m, r, kp, kn, vp, vn); c != 0 {
		t.Fatalf("set_header = %d", c)
	}
	bp, bn := g.str("body!")
	if c := setBody(bg, g.m, r, bp, bn); c != 0 {
		t.Fatalf("set_body = %d", c)
	}
	fn[func(int32, float64) int32](t, net, "set_timeout")(r, 5)
	send(r)
	if got := string(h.request(r).Data); got != "POST k1 body!" {
		t.Fatalf("echo = %q", got)
	}

	// send_all writes a per-request error array back into guest memory
	a, b := newReq(0, srv.URL+"/page"), newReq(0, "http://127.0.0.1:1/unreachable")
	arr := binary.LittleEndian.AppendUint32(nil, uint32(a))
	arr = binary.LittleEndian.AppendUint32(arr, uint32(b))
	arr = binary.LittleEndian.AppendUint32(arr, 999)
	ptr := g.bytes(arr)
	sendAll := fn[func(context.Context, api.Module, int32, int32) int32](t, net, "send_all")
	if c := sendAll(bg, g.m, ptr, 3); c != netRequestError {
		t.Fatalf("send_all = %d", c)
	}
	codes := g.read(ptr, 12)
	got := []int32{int32(binary.LittleEndian.Uint32(codes)), int32(binary.LittleEndian.Uint32(codes[4:])), int32(binary.LittleEndian.Uint32(codes[8:]))}
	if got[0] != 0 || got[1] != netRequestError || got[2] != netInvalidDescriptor {
		t.Fatalf("send_all codes = %v", got)
	}
	if status(a) != 200 {
		t.Fatal("send_all did not store the response")
	}
}

func TestRateLimit(t *testing.T) {
	var r rateLimit
	r.set(2, 200*time.Millisecond)
	start := time.Now()
	for i := 0; i < 3; i++ {
		r.wait()
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Fatalf("third request was not delayed (%v)", d)
	}
}
