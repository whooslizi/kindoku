package abi

import (
	"encoding/binary"
	"errors"
	"math"
)

// Postcard wire format (https://postcard.jamesmunns.com/wire-format), as used by aidoku-rs:
// unsigned ints are LEB128 varints, signed ints are zigzag varints, f32/f64 are little-endian,
// strings/seqs/maps are varint length + items, Option is 0/1 + value, enums are varint tag + fields.

var ErrDecode = errors.New("postcard: unexpected end of data")

type Enc struct{ B []byte }

func (e *Enc) Uvarint(v uint64) { e.B = binary.AppendUvarint(e.B, v) }
func (e *Enc) Varint(v int64)   { e.Uvarint(uint64((v << 1) ^ (v >> 63))) }
func (e *Enc) Bool(v bool) {
	if v {
		e.B = append(e.B, 1)
	} else {
		e.B = append(e.B, 0)
	}
}
func (e *Enc) U8(v uint8)    { e.B = append(e.B, v) }
func (e *Enc) F32(v float32) { e.B = binary.LittleEndian.AppendUint32(e.B, math.Float32bits(v)) }
func (e *Enc) F64(v float64) { e.B = binary.LittleEndian.AppendUint64(e.B, math.Float64bits(v)) }
func (e *Enc) Bytes(v []byte) {
	e.Uvarint(uint64(len(v)))
	e.B = append(e.B, v...)
}
func (e *Enc) Str(v string) { e.Uvarint(uint64(len(v))); e.B = append(e.B, v...) }
func (e *Enc) Strs(v []string) {
	e.Uvarint(uint64(len(v)))
	for _, s := range v {
		e.Str(s)
	}
}
func (e *Enc) OptStr(v *string) {
	e.Bool(v != nil)
	if v != nil {
		e.Str(*v)
	}
}
func (e *Enc) OptStrs(v []string) {
	e.Bool(v != nil)
	if v != nil {
		e.Strs(v)
	}
}
func (e *Enc) Map(m map[string]string) {
	e.Uvarint(uint64(len(m)))
	for k, v := range m {
		e.Str(k)
		e.Str(v)
	}
}

// Dec records the first error and returns zero values after it, so callers check Err once.
type Dec struct {
	B   []byte
	Err error
}

func (d *Dec) fail() { d.Err = ErrDecode; d.B = nil }

func (d *Dec) Uvarint() uint64 {
	if d.Err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.B)
	if n <= 0 {
		d.fail()
		return 0
	}
	d.B = d.B[n:]
	return v
}
func (d *Dec) Varint() int64 { u := d.Uvarint(); return int64(u>>1) ^ -int64(u&1) }
func (d *Dec) take(n int) []byte {
	if d.Err != nil || n < 0 || len(d.B) < n {
		d.fail()
		return make([]byte, max(n, 0)) // zeros; callers only pass small fixed sizes or a checked Len
	}
	b := d.B[:n]
	d.B = d.B[n:]
	return b
}
func (d *Dec) U8() uint8    { return d.take(1)[0] }
func (d *Dec) Bool() bool   { return d.U8() != 0 }
func (d *Dec) F32() float32 { return math.Float32frombits(binary.LittleEndian.Uint32(d.take(4))) }
func (d *Dec) F64() float64 { return math.Float64frombits(binary.LittleEndian.Uint64(d.take(8))) }
func (d *Dec) Len() int {
	n := d.Uvarint()
	if n > uint64(len(d.B)) { // every item takes ≥1 byte, so a longer count is corrupt
		d.fail()
		return 0
	}
	return int(n)
}
func (d *Dec) Bytes() []byte { return append([]byte(nil), d.take(d.Len())...) }
func (d *Dec) Str() string   { return string(d.take(d.Len())) }
func (d *Dec) Strs() []string {
	n := d.Len()
	out := make([]string, 0, n)
	for i := 0; i < n && d.Err == nil; i++ {
		out = append(out, d.Str())
	}
	return out
}
func (d *Dec) Opt() bool { return d.Bool() }
func (d *Dec) OptStr() *string {
	if !d.Opt() {
		return nil
	}
	s := d.Str()
	return &s
}
func (d *Dec) OptStrs() []string {
	if !d.Opt() {
		return nil
	}
	return d.Strs()
}
func (d *Dec) Map() map[string]string {
	n := d.Len()
	m := make(map[string]string, n)
	for i := 0; i < n && d.Err == nil; i++ {
		k := d.Str()
		m[k] = d.Str()
	}
	return m
}
