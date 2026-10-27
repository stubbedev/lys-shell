package resample

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"testing"

	"github.com/stubbedev/lys-lib/render"
)

func sources(w, h int) map[string]image.Image {
	r := image.Rect(3, 2, 3+w, 2+h)
	rgba := image.NewRGBA(r)
	nrgba := image.NewNRGBA(r)
	ycc := image.NewYCbCr(r, image.YCbCrSubsampleRatio420)
	gray := image.NewGray(r)
	pal := image.NewPaletted(r, palette.Plan9)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := uint8(x*37 + y*91 + x*y)
			a := uint8(128 + (x*y)%128)
			rgba.SetRGBA(x, y, color.RGBA{R: v / 2, G: (v * 3) / 4 / 2, B: a / 3, A: a})
			nrgba.SetNRGBA(x, y, color.NRGBA{R: v, G: v ^ 0x5a, B: uint8(y * 7), A: a})
			gray.SetGray(x, y, color.Gray{Y: v})
			pal.Set(x, y, color.RGBA{R: v, G: uint8(x), B: uint8(y), A: 255})
			ycc.Y[ycc.YOffset(x, y)] = v
			ci := ycc.COffset(x, y)
			ycc.Cb[ci], ycc.Cr[ci] = uint8(x*5), uint8(y*11)
		}
	}
	return map[string]image.Image{"rgba": rgba, "nrgba": nrgba, "ycbcr": ycc, "gray": gray, "paletted": pal}
}

// Into writes what render.Resample returns at the same size, for every
// source model, down- and upscales, crops, and box-reduced downscales.
func TestIntoMatchesRenderResample(t *testing.T) {
	for name, src := range sources(61, 43) {
		b := src.Bounds()
		for _, tc := range []struct {
			rect   image.Rectangle
			dw, dh int
		}{
			{b, 61, 43},
			{b, 20, 14},
			{b, 7, 5},
			{b, 150, 97},
			{image.Rect(b.Min.X+9, b.Min.Y+4, b.Min.X+40, b.Min.Y+30), 64, 36},
			{image.Rect(b.Min.X+9, b.Min.Y+4, b.Min.X+40, b.Min.Y+30), 13, 9},
			{b, 61, 1},
		} {
			want := render.Resample(src, tc.rect, tc.dw, tc.dh)
			got := image.NewRGBA(image.Rect(0, 0, tc.dw, tc.dh))
			Into(got, src, tc.rect)
			if !bytes.Equal(got.Pix, want.Pix) {
				t.Errorf("%s %v -> %dx%d differs from render.Resample", name, tc.rect, tc.dw, tc.dh)
			}
		}
	}
}

// A source rect outside the image leaves dst untouched rather than
// reading out of bounds.
func TestIntoEmptySourceLeavesDst(t *testing.T) {
	src := sources(8, 8)["rgba"]
	dst := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range dst.Pix {
		dst.Pix[i] = 7
	}
	Into(dst, src, image.Rect(100, 100, 120, 120))
	for _, v := range dst.Pix {
		if v != 7 {
			t.Fatal("an empty source rect wrote dst")
		}
	}
}
