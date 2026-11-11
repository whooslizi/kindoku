// Package eink turns manga pages into small 16-gray PNGs sized for an e-ink screen.
package eink

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"

	_ "golang.org/x/image/webp"
)

type Options struct {
	MaxW, MaxH int
	Dither     bool
}

// palette16 is the 16-level gray ramp Kindle panels display natively.
var palette16 = func() color.Palette {
	p := make(color.Palette, 16)
	for i := range p {
		p[i] = color.Gray{Y: uint8(i * 17)}
	}
	return p
}()

// targetSize fits w×h into the box without upscaling; strips taller than 2:1 (webtoons) are
// fitted to the width only so their text stays legible.
func targetSize(w, h int, o Options) (int, int) {
	scale := 1.0
	if sw := float64(o.MaxW) / float64(w); sw < scale {
		scale = sw
	}
	if h <= 2*w {
		if sh := float64(o.MaxH) / float64(h); sh < scale {
			scale = sh
		}
	}
	if scale >= 1 {
		return w, h
	}
	return max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
}

// Process converts img to a 16-gray paletted image that fits the screen.
func Process(img image.Image, o Options) *image.Paletted {
	b := img.Bounds()
	var gray *image.Gray
	switch v := img.(type) {
	case *image.Gray:
		gray = v
	case *image.YCbCr: // JPEG: the luma plane already is the grayscale image
		gray = &image.Gray{Pix: v.Y, Stride: v.YStride, Rect: v.Rect}
	case *image.Paletted: // most PNG pages: map each palette entry once instead of every pixel
		var lut [256]uint8
		for i, c := range v.Palette {
			lut[i] = color.GrayModel.Convert(c).(color.Gray).Y
		}
		gray = image.NewGray(b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			src := v.Pix[v.PixOffset(b.Min.X, y):][:b.Dx()]
			dst := gray.Pix[gray.PixOffset(b.Min.X, y):]
			for x, i := range src {
				dst[x] = lut[i]
			}
		}
	default:
		gray = image.NewGray(b)
		draw.Draw(gray, b, img, b.Min, draw.Src)
	}
	gray = gray.SubImage(b).(*image.Gray)
	if w, h := targetSize(b.Dx(), b.Dy(), o); w != b.Dx() || h != b.Dy() {
		gray = resize(gray, w, h)
	}
	out := image.NewPaletted(image.Rect(0, 0, gray.Rect.Dx(), gray.Rect.Dy()), palette16)
	if o.Dither {
		floydSteinberg(gray, out)
		return out
	}
	for y := 0; y < out.Rect.Dy(); y++ {
		row := gray.Pix[gray.PixOffset(gray.Rect.Min.X, gray.Rect.Min.Y+y):]
		for x := 0; x < out.Rect.Dx(); x++ {
			out.Pix[y*out.Stride+x] = uint8((int(row[x]) + 8) / 17)
		}
	}
	return out
}

func floydSteinberg(g *image.Gray, out *image.Paletted) {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	cur, next := make([]int, w+2), make([]int, w+2) // error terms, offset by 1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := int(g.Pix[g.PixOffset(g.Rect.Min.X+x, g.Rect.Min.Y+y)]) + cur[x+1]/16
			v = min(255, max(0, v))
			q := (v + 8) / 17
			out.Pix[y*out.Stride+x] = uint8(q)
			e := v - q*17
			cur[x+2] += e * 7
			next[x] += e * 3
			next[x+1] += e * 5
			next[x+2] += e
		}
		cur, next = next, cur
		clear(next)
	}
}

func Encode(p *image.Paletted) []byte {
	var buf bytes.Buffer
	// BestSpeed: BestCompression took 2/3 of a page's CPU time for 6% smaller files.
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	enc.Encode(&buf, p) // writing to a bytes.Buffer cannot fail
	return buf.Bytes()
}

// Optimize decodes an image file and returns the e-ink PNG. If the data can't be decoded, the
// original bytes come back unchanged with ok=false.
func Optimize(data []byte, o Options) (out []byte, ok bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, false
	}
	return Encode(Process(img, o)), true
}

// resize downsamples with a separable Catmull-Rom filter. x/image/draw's CatmullRom does the same
// but keeps a [4]float64 per pixel (~50 MB for one page); one float32 channel is 8x smaller.
func resize(src *image.Gray, w, h int) *image.Gray {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	tmp := make([]float32, w*sh) // horizontal pass: w × sh
	xw := weights(sw, w)
	for y := 0; y < sh; y++ {
		row := src.Pix[src.PixOffset(src.Rect.Min.X, src.Rect.Min.Y+y):]
		for x, c := range xw {
			var v float32
			for i, wt := range c.w {
				v += wt * float32(row[c.start+i])
			}
			tmp[y*w+x] = v
		}
	}
	dst := image.NewGray(image.Rect(0, 0, w, h))
	for y, c := range weights(sh, h) {
		for x := 0; x < w; x++ {
			var v float32
			for i, wt := range c.w {
				v += wt * tmp[(c.start+i)*w+x]
			}
			dst.Pix[y*dst.Stride+x] = uint8(min(255, max(0, v+0.5)))
		}
	}
	return dst
}

type contrib struct {
	start int
	w     []float32
}

// weights returns, for each destination pixel, the normalized Catmull-Rom taps over the source.
func weights(srcN, dstN int) []contrib {
	scale := float64(srcN) / float64(dstN) // ≥ 1: downscale only
	support := 2 * scale
	out := make([]contrib, dstN)
	for i := range out {
		center := (float64(i)+0.5)*scale - 0.5
		lo := max(0, int(math.Ceil(center-support)))
		hi := min(srcN-1, int(math.Floor(center+support)))
		c := contrib{start: lo, w: make([]float32, hi-lo+1)}
		var sum float64
		for j := lo; j <= hi; j++ {
			wt := catmullRom((float64(j) - center) / scale)
			c.w[j-lo] = float32(wt)
			sum += wt
		}
		for k := range c.w {
			c.w[k] /= float32(sum)
		}
		out[i] = c
	}
	return out
}

func catmullRom(t float64) float64 {
	t = math.Abs(t)
	switch {
	case t < 1:
		return (1.5*t-2.5)*t*t + 1
	case t < 2:
		return ((-0.5*t+2.5)*t-4)*t + 2
	}
	return 0
}
