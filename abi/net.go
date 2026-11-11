package abi

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// net result codes (aidoku-rs RequestError).
const (
	netInvalidDescriptor = -1
	netInvalidString     = -2
	netInvalidMethod     = -3
	netInvalidURL        = -4
	netInvalidHTML       = -5
	netInvalidBufferSize = -6
	netMissingData       = -7
	netMissingResponse   = -8
	netMissingURL        = -9
	netRequestError      = -10
	netFailedMemoryWrite = -11
	netNotAnImage        = -12
)

var methods = []string{"GET", "POST", "PUT", "HEAD", "DELETE", "PATCH", "OPTIONS", "CONNECT", "TRACE"}

// Request is a net descriptor: the request a source builds, then its response.
type Request struct {
	Method  string
	URL     string
	Header  http.Header
	Body    []byte
	Timeout time.Duration

	Status   int
	FinalURL string
	RespHdr  http.Header
	Data     []byte
	Err      error
}

func (h *Host) request(rid int32) *Request {
	r, _ := h.Get(rid).(*Request)
	return r
}

func (h *Host) Do(r *Request) {
	h.rate.wait()
	ctx := h.Ctx
	if r.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.Timeout)
		defer cancel()
	}
	var body io.Reader
	if r.Body != nil {
		body = bytes.NewReader(r.Body)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, r.URL, body)
	if err != nil {
		r.Err = err
		return
	}
	for k, v := range r.Header {
		req.Header[k] = v
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		r.Err = err
		return
	}
	defer resp.Body.Close()
	r.Data, r.Err = io.ReadAll(resp.Body)
	r.Status, r.RespHdr, r.FinalURL = resp.StatusCode, resp.Header, resp.Request.URL.String()
}

func (h *Host) net() map[string]any {
	withReq := func(f func(r *Request) int32) func(int32) int32 {
		return func(rid int32) int32 {
			r := h.request(rid)
			if r == nil {
				return netInvalidDescriptor
			}
			return f(r)
		}
	}
	return map[string]any{
		"init": func(method int32) int32 {
			if method < 0 || int(method) >= len(methods) {
				return netInvalidMethod
			}
			return h.Put(&Request{Method: methods[method], Header: http.Header{}})
		},
		"send": withReq(func(r *Request) int32 {
			if r.URL == "" {
				return netMissingURL
			}
			h.Do(r)
			if r.Err != nil {
				h.Log("net: " + r.Err.Error())
				return netRequestError
			}
			return 0
		}),
		"send_all": func(_ context.Context, m api.Module, ptr, n int32) int32 {
			if ptr < 0 || n <= 0 {
				return netInvalidDescriptor
			}
			raw, ok := m.Memory().Read(uint32(ptr), uint32(n)*4)
			if !ok {
				return netInvalidDescriptor
			}
			codes := make([]int32, n)
			var wg sync.WaitGroup
			for i := range codes {
				r := h.request(int32(binary.LittleEndian.Uint32(raw[i*4:])))
				switch {
				case r == nil:
					codes[i] = netInvalidDescriptor
				case r.URL == "":
					codes[i] = netMissingURL
				default:
					wg.Add(1)
					go func() {
						defer wg.Done()
						if h.Do(r); r.Err != nil {
							codes[i] = netRequestError
						}
					}()
				}
			}
			wg.Wait()
			out := make([]byte, 0, n*4)
			failed := false
			for _, c := range codes {
				out = binary.LittleEndian.AppendUint32(out, uint32(c))
				failed = failed || c != 0
			}
			m.Memory().Write(uint32(ptr), out)
			if failed {
				return netRequestError
			}
			return 0
		},
		"set_url": func(_ context.Context, m api.Module, rid, p, n int32) int32 {
			r := h.request(rid)
			if r == nil {
				return netInvalidDescriptor
			}
			s, ok := readStr(m, p, n)
			if !ok || n == 0 {
				return netInvalidString
			}
			if _, err := url.Parse(s); err != nil {
				return netInvalidURL
			}
			r.URL = s
			return 0
		},
		"set_header": func(_ context.Context, m api.Module, rid, kp, kl, vp, vl int32) int32 {
			r := h.request(rid)
			if r == nil {
				return netInvalidDescriptor
			}
			k, ok1 := readStr(m, kp, kl)
			v, ok2 := readStr(m, vp, vl)
			if !ok1 || !ok2 || kl == 0 || vl == 0 {
				return netInvalidString
			}
			r.Header.Set(k, v)
			return 0
		},
		"set_body": func(_ context.Context, m api.Module, rid, p, n int32) int32 {
			r := h.request(rid)
			if r == nil {
				return netInvalidDescriptor
			}
			b, ok := readBytes(m, p, n)
			if !ok || n == 0 {
				return netInvalidString
			}
			r.Body = b
			return 0
		},
		"set_timeout": func(rid int32, secs float64) int32 {
			r := h.request(rid)
			if r == nil {
				return netInvalidDescriptor
			}
			r.Timeout = time.Duration(secs * float64(time.Second))
			return 0
		},
		"data_len": withReq(func(r *Request) int32 {
			if r.Data == nil {
				return netMissingData
			}
			return int32(len(r.Data))
		}),
		"read_data": func(_ context.Context, m api.Module, rid, buf, size int32) int32 {
			r := h.request(rid)
			if r == nil {
				return netInvalidDescriptor
			}
			if r.Data == nil {
				return netMissingData
			}
			if size < 0 || int(size) > len(r.Data) {
				return netInvalidBufferSize
			}
			if !m.Memory().Write(uint32(buf), r.Data[:size]) {
				return netFailedMemoryWrite
			}
			return 0
		},
		"get_image": withReq(func(r *Request) int32 {
			if r.Data == nil {
				return netMissingData
			}
			img, err := decodeImage(r.Data)
			if err != nil {
				return netNotAnImage
			}
			return h.Put(img)
		}),
		"get_status_code": withReq(func(r *Request) int32 {
			if r.RespHdr == nil {
				return netMissingResponse
			}
			return int32(r.Status)
		}),
		"get_url": withReq(func(r *Request) int32 {
			if r.RespHdr == nil {
				return netMissingResponse
			}
			return h.Put(r.FinalURL)
		}),
		"get_header": func(_ context.Context, m api.Module, rid, kp, kl int32) int32 {
			r := h.request(rid)
			if r == nil {
				return netInvalidDescriptor
			}
			if r.RespHdr == nil {
				return netMissingResponse
			}
			k, ok := readStr(m, kp, kl)
			if !ok || kl == 0 {
				return netInvalidString
			}
			if _, has := r.RespHdr[http.CanonicalHeaderKey(k)]; !has {
				return netMissingData
			}
			return h.Put(r.RespHdr.Get(k))
		},
		"html": withReq(func(r *Request) int32 {
			if r.Data == nil {
				return netMissingData
			}
			base := r.FinalURL
			if base == "" {
				base = r.URL
			}
			doc, err := parseDocument(string(r.Data), base, false)
			if err != nil {
				return netInvalidHTML
			}
			return h.Put(doc)
		}),
		"set_rate_limit": func(permits, period, unit int32) {
			mult := []int32{1, 60, 3600}
			if unit < 0 || int(unit) >= len(mult) {
				unit = 0
			}
			h.rate.set(int(permits), time.Duration(period*mult[unit])*time.Second)
		},
	}
}

// rateLimit allows `permits` requests per `period`; zero values disable it.
type rateLimit struct {
	mu      sync.Mutex
	permits int
	period  time.Duration
	start   time.Time
	count   int
}

func (r *rateLimit) set(permits int, period time.Duration) {
	r.mu.Lock()
	r.permits, r.period = permits, period
	r.mu.Unlock()
}

func (r *rateLimit) wait() {
	for {
		r.mu.Lock()
		if r.permits <= 0 || r.period <= 0 {
			r.mu.Unlock()
			return
		}
		now := time.Now()
		if now.Sub(r.start) >= r.period {
			r.start, r.count = now, 0
		}
		if r.count < r.permits {
			r.count++
			r.mu.Unlock()
			return
		}
		d := r.start.Add(r.period).Sub(now)
		r.mu.Unlock()
		time.Sleep(d)
	}
}
