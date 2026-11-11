package eink

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"testing"
)

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

func encodeJPEG(img image.Image) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

var screen = Options{MaxW: 1072, MaxH: 1448}

func TestOptimizeSizeAndFormat(t *testing.T) {
	for _, dither := range []bool{false, true} {
		o := screen
		o.Dither = dither
		out, ok := Optimize(encodeJPEG(gradient(2144, 2896)), o)
		if !ok {
			t.Fatal("optimize failed")
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Width != 1072 || cfg.Height != 1448 {
			t.Errorf("size %dx%d, want 1072x1448", cfg.Width, cfg.Height)
		}
		pal, ok := cfg.ColorModel.(color.Palette)
		if !ok || len(pal) > 16 {
			t.Fatalf("want ≤16-color palette, got %T len %d", cfg.ColorModel, len(pal))
		}
		for _, c := range pal {
			r, g, b, _ := c.RGBA()
			if r != g || g != b {
				t.Errorf("palette color %v is not gray", c)
			}
		}
		if out[24] != 4 { // IHDR bit depth
			t.Errorf("bit depth %d, want 4", out[24])
		}
	}
}

func TestNoUpscale(t *testing.T) {
	p := Process(gradient(300, 400), screen)
	if p.Rect.Dx() != 300 || p.Rect.Dy() != 400 {
		t.Fatalf("small image resized to %v", p.Rect)
	}
}

func TestWebtoonStripFitsWidthOnly(t *testing.T) {
	if w, h := targetSize(1600, 6000, screen); w != 1072 || h != 4020 {
		t.Fatalf("strip -> %dx%d, want 1072x4020", w, h)
	}
	if w, h := targetSize(1000, 2000, screen); w != 724 || h != 1448 { // exactly 2:1 is not a strip
		t.Fatalf("2:1 page -> %dx%d, want 724x1448", w, h)
	}
	if w, h := targetSize(2000, 1000, screen); w != 1072 || h != 536 {
		t.Fatalf("spread -> %dx%d", w, h)
	}
}

func TestPassthroughOnBadInput(t *testing.T) {
	in := []byte("not an image")
	out, ok := Optimize(in, screen)
	if ok || !bytes.Equal(out, in) {
		t.Fatal("bad input must come back unchanged")
	}
}

func TestQuantizeLevels(t *testing.T) {
	g := image.NewGray(image.Rect(0, 0, 3, 1))
	g.Pix = []uint8{0, 128, 255}
	p := Process(g, screen)
	if p.Pix[0] != 0 || p.Pix[1] != 8 || p.Pix[2] != 15 {
		t.Fatalf("levels = %v", p.Pix)
	}
}

func TestPalettedMatchesDraw(t *testing.T) {
	pal := color.Palette{color.Black, color.White, color.RGBA{200, 30, 90, 255}, color.RGBA{10, 180, 240, 255}}
	p := image.NewPaletted(image.Rect(3, 5, 40, 60), pal)
	for i := range p.Pix {
		p.Pix[i] = uint8(i * 7 % len(pal))
	}
	rgba := image.NewRGBA(p.Rect)
	draw.Draw(rgba, p.Rect, p, p.Rect.Min, draw.Src)
	o := Options{MaxW: 100, MaxH: 100}
	if a, b := Process(p, o), Process(rgba, o); !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("paletted fast path differs from the generic conversion")
	}
}
