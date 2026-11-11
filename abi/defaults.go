package abi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero/api"
	"github.com/whooslizi/kinkoku/store"
)

// defaults result codes and value kinds (aidoku-rs DefaultValue).
const (
	defInvalidKey     = -1
	defInvalidValue   = -2
	defFailedEncoding = -3
	defFailedDecoding = -4

	kindData        = 0
	kindBool        = 1
	kindInt         = 2
	kindFloat       = 3
	kindString      = 4
	kindStringArray = 5
	kindNull        = 6
)

// Defaults is a source's key/value settings, persisted as defaults.json. Values are bool, int32,
// float32, string, []string or []byte. Registered values (from settings.json) are fallbacks
// that are never written to disk.
type Defaults struct {
	path string
	vals map[string]any
	regs map[string]any
}

func LoadDefaults(path string) (*Defaults, error) {
	d := &Defaults{path: path, vals: map[string]any{}, regs: map[string]any{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for k, v := range raw {
		if val, ok := fromJSON(v); ok {
			d.vals[k] = val
		}
	}
	return d, nil
}

func fromJSON(v json.RawMessage) (any, bool) {
	var x any
	if json.Unmarshal(v, &x) != nil {
		return nil, false
	}
	switch t := x.(type) {
	case bool, string:
		return t, true
	case float64:
		if strings.ContainsAny(string(v), ".eE") {
			return float32(t), true
		}
		return int32(t), true
	case []any:
		out := make([]string, 0, len(t))
		for _, s := range t {
			str, _ := s.(string)
			out = append(out, str)
		}
		return out, true
	case map[string]any:
		if s, ok := t["data"].(string); ok {
			b, err := base64.StdEncoding.DecodeString(s)
			return b, err == nil
		}
	}
	return nil, false
}

func toJSON(v any) json.RawMessage {
	switch t := v.(type) {
	case float32:
		s := strconv.FormatFloat(float64(t), 'f', -1, 32)
		if !strings.Contains(s, ".") {
			s += ".0" // keep the float kind across a reload
		}
		return json.RawMessage(s)
	case []byte:
		b, _ := json.Marshal(map[string]string{"data": base64.StdEncoding.EncodeToString(t)})
		return b
	}
	b, _ := json.Marshal(v)
	return b
}

func (d *Defaults) Register(key string, v any) { d.regs[key] = v }

func (d *Defaults) Get(key string) any {
	if v, ok := d.vals[key]; ok {
		return v
	}
	return d.regs[key]
}

func (d *Defaults) Set(key string, v any) error {
	if v == nil {
		delete(d.vals, key)
	} else {
		d.vals[key] = v
	}
	if d.path == "" {
		return nil
	}
	out := make(map[string]json.RawMessage, len(d.vals))
	for k, v := range d.vals {
		out[k] = toJSON(v)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return store.WriteAtomic(d.path, b)
}

// SetString parses a CLI value, coercing it to the type of the existing or registered value.
func (d *Defaults) SetString(key, s string) error {
	var v any = s
	switch d.Get(key).(type) {
	case bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		v = b
	case int32:
		n, err := strconv.ParseInt(s, 10, 32)
		if err != nil {
			return err
		}
		v = int32(n)
	case float32:
		f, err := strconv.ParseFloat(s, 32)
		if err != nil {
			return err
		}
		v = float32(f)
	case []string:
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err != nil {
			return fmt.Errorf("expected a JSON string array: %w", err)
		}
		v = arr
	case string:
	default: // unknown key: take JSON scalars/arrays as typed, anything else as a string
		if val, ok := fromJSON(json.RawMessage(s)); ok {
			v = val
		}
	}
	return d.Set(key, v)
}

// JSON returns registered values overlaid with stored ones, for `settings get`.
func (d *Defaults) JSON() map[string]any {
	out := map[string]any{}
	for k, v := range d.regs {
		out[k] = v
	}
	for k, v := range d.vals {
		out[k] = v
	}
	return out
}

func (h *Host) defaults() map[string]any {
	return map[string]any{
		"get": func(_ context.Context, m api.Module, kp, kl int32) int32 {
			key, ok := readStr(m, kp, kl)
			if !ok {
				return defInvalidKey
			}
			var e Enc
			switch v := h.Defaults.Get(key).(type) {
			case bool:
				e.Bool(v)
			case int32:
				e.Varint(int64(v))
			case float32:
				e.F32(v)
			case string:
				e.Str(v)
			case []string:
				e.Strs(v)
			case []byte:
				return h.Put(append([]byte(nil), v...))
			default:
				return defInvalidValue
			}
			return h.Put(e.B)
		},
		"set": func(_ context.Context, m api.Module, kp, kl, kind, vp int32) int32 {
			key, ok := readStr(m, kp, kl)
			if !ok {
				return defInvalidKey
			}
			var v any
			if kind != kindNull {
				data, ok := ReadResult(m, vp)
				if !ok {
					return defFailedDecoding
				}
				d := Dec{B: data}
				switch kind {
				case kindData:
					v = data
				case kindBool:
					v = d.Bool()
				case kindInt:
					v = int32(d.Varint())
				case kindFloat:
					v = d.F32()
				case kindString:
					v = d.Str()
				case kindStringArray:
					v = d.Strs()
				default:
					return defInvalidValue
				}
				if d.Err != nil {
					return defFailedDecoding
				}
			}
			if err := h.Defaults.Set(key, v); err != nil {
				h.Log("defaults: " + err.Error())
			}
			return 0
		},
	}
}
