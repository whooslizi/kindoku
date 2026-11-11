package abi

import (
	"bytes"
	"testing"
)

func TestPostcardVectors(t *testing.T) {
	var e Enc
	e.Uvarint(300) // ac 02
	e.Varint(-1)   // 01 (zigzag)
	e.Varint(63)   // 7e
	e.Str("hi")    // 02 68 69
	e.OptStr(nil)  // 00
	e.F32(1)       // 00 00 80 3f
	e.Bool(true)   // 01
	want := []byte{0xac, 0x02, 0x01, 0x7e, 0x02, 'h', 'i', 0x00, 0x00, 0x00, 0x80, 0x3f, 0x01}
	if !bytes.Equal(e.B, want) {
		t.Fatalf("encoded % x, want % x", e.B, want)
	}
	d := Dec{B: e.B}
	if d.Uvarint() != 300 || d.Varint() != -1 || d.Varint() != 63 || d.Str() != "hi" || d.OptStr() != nil || d.F32() != 1 || !d.Bool() || d.Err != nil {
		t.Fatal("round trip failed")
	}
	d.U8()
	if d.Err == nil {
		t.Fatal("reading past the end must set Err")
	}
	bad := Dec{B: []byte{0x05, 'a'}} // claims 5 bytes, has 1
	if bad.Str(); bad.Err == nil {
		t.Fatal("short string must set Err")
	}
}
