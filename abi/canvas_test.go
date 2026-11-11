package abi

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func pngOf(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestCanvasDescramble(t *testing.T) {
	h, g := newGuest(t)
	c := h.canvas()
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	src := image.NewRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			if x < 2 {
				src.Set(x, y, red)
			} else {
				src.Set(x, y, blue)
			}
		}
	}
	data := pngOf(src)
	newImage := fn[fnMemStr](t, c, "new_image")
	img := newImage(bg, g.m, g.bytes(data), int32(len(data)))
	if img < 0 {
		t.Fatalf("new_image = %d", img)
	}
	if w := fn[func(int32) float32](t, c, "get_image_width")(img); w != 4 {
		t.Fatalf("width = %v", w)
	}
	if hh := fn[func(int32) float32](t, c, "get_image_height")(img); hh != 2 {
		t.Fatalf("height = %v", hh)
	}

	newCtx := fn[func(float32, float32) int32](t, c, "new_context")
	if r := newCtx(0, 5); r != canvasInvalidBounds {
		t.Fatalf("new_context(0,5) = %d", r)
	}
	ctx := newCtx(4, 2)
	cp := fn[func(int32, int32, float32, float32, float32, float32, float32, float32, float32, float32) int32](t, c, "copy_image")
	// swap the halves, as a descrambler would
	if r := cp(ctx, img, 0, 0, 2, 2, 2, 0, 2, 2); r != 0 {
		t.Fatalf("copy_image = %d", r)
	}
	cp(ctx, img, 2, 0, 2, 2, 0, 0, 2, 2)
	if r := cp(ctx, img, 10, 10, 2, 2, 0, 0, 2, 2); r != canvasInvalidSrcRect {
		t.Fatalf("copy_image outside = %d", r)
	}
	out := h.Image(fn[fnRid](t, c, "get_image")(ctx))
	if got := color.RGBAModel.Convert(out.At(0, 0)); got != blue {
		t.Fatalf("pixel (0,0) = %v, want blue", got)
	}
	if got := color.RGBAModel.Convert(out.At(3, 1)); got != red {
		t.Fatalf("pixel (3,1) = %v, want red", got)
	}

	// draw_image through a translate+scale transform
	one := image.NewRGBA(image.Rect(0, 0, 1, 1))
	one.Set(0, 0, red)
	ctx2 := newCtx(4, 4)
	fn[func(int32, float32, float32, float32, float32, float32) int32](t, c, "set_transform")(ctx2, 2, 2, 2, 2, 0)
	if r := fn[func(int32, int32, float32, float32, float32, float32) int32](t, c, "draw_image")(ctx2, h.Put(image.Image(one)), 0, 0, 1, 1); r != 0 {
		t.Fatalf("draw_image = %d", r)
	}
	got := h.Image(fn[fnRid](t, c, "get_image")(ctx2))
	if a := color.RGBAModel.Convert(got.At(1, 1)).(color.RGBA).A; a != 0 {
		t.Fatalf("(1,1) should be empty, alpha %d", a)
	}
	if p := color.RGBAModel.Convert(got.At(3, 3)); p != red {
		t.Fatalf("(3,3) = %v, want red", p)
	}

	// get_image_data encodes PNG; raw bytes are copied
	enc := fn[fnRid](t, c, "get_image_data")
	if _, err := png.Decode(bytes.NewReader(h.Get(enc(img)).([]byte))); err != nil {
		t.Fatalf("get_image_data: %v", err)
	}
	raw := h.Put([]byte{1, 2})
	if b := h.Get(enc(raw)).([]byte); len(b) != 2 {
		t.Fatalf("raw copy = %v", b)
	}
	if r := newImage(bg, g.m, g.bytes([]byte("nope")), 4); r != canvasInvalidImage {
		t.Fatalf("new_image(garbage) = %d", r)
	}
}
