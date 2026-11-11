package abi

import (
	"context"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// std result codes (aidoku-rs StdError).
const (
	stdInvalidDescriptor = -1
	stdInvalidBufferSize = -2
	stdFailedMemoryWrite = -3
	stdInvalidString     = -4
	stdInvalidDateString = -5
)

func (h *Host) env() map[string]any {
	return map[string]any{
		"abort": func() {},
		"print": func(_ context.Context, m api.Module, ptr, n int32) {
			s, _ := readStr(m, ptr, n)
			h.Log(s)
		},
		"sleep": func(secs int32) { time.Sleep(time.Duration(secs) * time.Second) },
		// Partial results only speed up Aidoku's UI; the final result carries the same data.
		"send_partial_result": func(int32) {},
	}
}

func (h *Host) bytesOf(rid int32) ([]byte, bool) {
	switch v := h.Get(rid).(type) {
	case []byte:
		return v, true
	case string:
		return []byte(v), true
	}
	return nil, false
}

func (h *Host) std() map[string]any {
	return map[string]any{
		"destroy": func(rid int32) { h.Remove(rid) },
		"buffer_len": func(rid int32) int32 {
			b, ok := h.bytesOf(rid)
			if !ok {
				return stdInvalidDescriptor
			}
			return int32(len(b))
		},
		"read_buffer": func(_ context.Context, m api.Module, rid, buf, size int32) int32 {
			b, ok := h.bytesOf(rid)
			if !ok {
				return stdInvalidDescriptor
			}
			if size < 0 || int(size) > len(b) {
				return stdInvalidBufferSize
			}
			if !m.Memory().Write(uint32(buf), b[:size]) {
				return stdFailedMemoryWrite
			}
			return 0
		},
		"current_date": func() float64 { return float64(time.Now().UnixNano()) / 1e9 },
		"utc_offset": func() int64 {
			_, off := time.Now().Zone()
			return -int64(off)
		},
		"parse_date": func(_ context.Context, m api.Module, sp, sl, fp, fl, lp, ll, tp, tl int32) float64 {
			s, ok1 := readStr(m, sp, sl)
			f, ok2 := readStr(m, fp, fl)
			if !ok1 || !ok2 {
				return stdInvalidString
			}
			var locale, tz string
			if ll > 0 {
				locale, _ = readStr(m, lp, ll)
			}
			if tl > 0 {
				tz, _ = readStr(m, tp, tl)
			}
			t, err := ParseDate(s, f, locale, tz)
			if err != nil {
				return stdInvalidDateString
			}
			return float64(t.UnixNano()) / 1e9
		},
	}
}
