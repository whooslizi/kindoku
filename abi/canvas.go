package abi

import (
	"bytes"
	"context"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"

	"github.com/tetratelabs/wazero/api"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
	_ "golang.org/x/image/webp"
)

// canvas result codes (aidoku-rs CanvasError).
const (
	canvasInvalidContext      = -1
	canvasInvalidImagePointer = -2
	canvasInvalidImage        = -3
	canvasInvalidSrcRect      = -4
	canvasInvalidBounds       = -6
	canvasInvalidData         = -11
)

// Canvas is a drawing context. M maps user space to device pixels (origin top-left).
type Canvas struct {
	Img *image.RGBA
	M   f64.Aff3
}

var identity = f64.Aff3{1, 0, 0, 0, 1, 0}

func mul(a, b f64.Aff3) f64.Aff3 {
	return f64.Aff3{
		a[0]*b[0] + a[1]*b[3], a[0]*b[1] + a[1]*b[4], a[0]*b[2] + a[1]*b[5] + a[2],
		a[3]*b[0] + a[4]*b[3], a[3]*b[1] + a[4]*b[4], a[3]*b[2] + a[4]*b[5] + a[5],
	}
}

func decodeImage(b []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(b))
	return img, err
}

func (h *Host) Image(rid int32) image.Image {
	switch v := h.Get(rid).(type) {
	case image.Image:
		return v
	case []byte:
		if img, err := decodeImage(v); err == nil {
			return img
		}
	}
	return nil
}

// drawInto draws src (whose bounds map to the dst rect in user space) through the context transform.
func (c *Canvas) drawInto(src image.Image, dx, dy, dw, dh float32) {
	b := src.Bounds() // decoded and cropped images are 0-based
	place := f64.Aff3{float64(dw) / float64(b.Dx()), 0, float64(dx), 0, float64(dh) / float64(b.Dy()), float64(dy)}
	xdraw.ApproxBiLinear.Transform(c.Img, mul(c.M, place), src, b, xdraw.Over, nil)
}

func (h *Host) canvas() map[string]any {
	ctxOf := func(rid int32) *Canvas { c, _ := h.Get(rid).(*Canvas); return c }
	return map[string]any{
		"new_context": func(w, ht float32) int32 {
			if w <= 0 || ht <= 0 || w*ht > 64<<20 {
				return canvasInvalidBounds
			}
			return h.Put(&Canvas{Img: image.NewRGBA(image.Rect(0, 0, int(w), int(ht))), M: identity})
		},
		"set_transform": func(rid int32, tx, ty, sx, sy, rot float32) int32 {
			c := ctxOf(rid)
			if c == nil {
				return canvasInvalidContext
			}
			sin, cos := math.Sincos(float64(rot))
			t := f64.Aff3{1, 0, float64(tx), 0, 1, float64(ty)}
			s := f64.Aff3{float64(sx), 0, 0, 0, float64(sy), 0}
			r := f64.Aff3{cos, -sin, 0, sin, cos, 0}
			c.M = mul(mul(t, s), r) // translate, then scale, then rotate (CoreGraphics order)
			return 0
		},
		"draw_image": func(rid, img int32, dx, dy, dw, dh float32) int32 {
			c := ctxOf(rid)
			if c == nil {
				return canvasInvalidContext
			}
			src := h.Image(img)
			if src == nil {
				return canvasInvalidImagePointer
			}
			if src.Bounds().Empty() {
				return canvasInvalidImage
			}
			c.drawInto(src, dx, dy, dw, dh)
			return 0
		},
		"copy_image": func(rid, img int32, sx, sy, sw, sh, dx, dy, dw, dh float32) int32 {
			c := ctxOf(rid)
			if c == nil {
				return canvasInvalidContext
			}
			src := h.Image(img)
			if src == nil {
				return canvasInvalidImagePointer
			}
			r := image.Rect(int(sx), int(sy), int(sx+sw), int(sy+sh)).Intersect(src.Bounds())
			if r.Empty() {
				return canvasInvalidSrcRect
			}
			// crop into a 0-based copy: x/image/draw's Transform draws nothing for a translated
			// source rect whose origin isn't (0,0)
			crop := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
			draw.Draw(crop, crop.Rect, src, r.Min, draw.Src)
			c.drawInto(crop, dx, dy, dw, dh)
			return 0
		},
		"get_image": func(rid int32) int32 {
			c := ctxOf(rid)
			if c == nil {
				return canvasInvalidContext
			}
			out := image.NewRGBA(c.Img.Rect)
			draw.Draw(out, out.Rect, c.Img, image.Point{}, draw.Src)
			return h.Put(image.Image(out))
		},
		"new_image": func(_ context.Context, m api.Module, p, n int32) int32 {
			b, ok := readBytes(m, p, n)
			if !ok {
				return canvasInvalidData
			}
			img, err := decodeImage(b)
			if err != nil {
				return canvasInvalidImage
			}
			return h.Put(img)
		},
		"get_image_data": func(rid int32) int32 {
			switch v := h.Get(rid).(type) {
			case []byte:
				return h.Put(append([]byte(nil), v...))
			case image.Image:
				var buf bytes.Buffer
				if err := png.Encode(&buf, v); err != nil {
					return canvasInvalidImage
				}
				return h.Put(buf.Bytes())
			}
			return canvasInvalidImagePointer
		},
		"get_image_width": func(rid int32) float32 {
			if img := h.Image(rid); img != nil {
				return float32(img.Bounds().Dx())
			}
			return canvasInvalidImagePointer
		},
		"get_image_height": func(rid int32) float32 {
			if img := h.Image(rid); img != nil {
				return float32(img.Bounds().Dy())
			}
			return canvasInvalidImagePointer
		},
		// fill, stroke, draw_text and fonts fall through to the unimplemented stub
	}
}
