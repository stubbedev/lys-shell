package credential

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"log"
	"math"
	"math/bits"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/imagedecode"
	"github.com/stubbedev/lys-shell/internal/intconv"
	"github.com/stubbedev/lys-shell/internal/offheap"
)

// HexFill converts a config color to a premultiplied fill.
func HexFill(c config.HexColor) render.Color {
	r, g, b, a := c.RGBA()
	return render.RGBA(r, g, b, a)
}

// Background builds the surface background (build_background): the
// backdrop scaled to cover under the .lock-bg-scrim legibility layer,
// or the configured color painted by a .lock-bg area when there is no
// backdrop.
func Background(bg *Backdrop, color render.Color) widget.Widget {
	if bg == nil {
		fill := widget.NewDrawingArea(0, 0)
		fill.AddClass("lock-bg")
		fill.OnDraw = func(cv *render.Canvas, r render.Rect) { cv.FillRect(r, color) }
		return fill
	}
	pic := widget.NewImage(bg.Image)
	pic.SetScale(widget.ImageCover)
	scrim := widget.NewBox(widget.Column, 0, 0)
	scrim.AddClass("lock-bg-scrim")
	return widget.NewOverlay().Append(pic).Append(scrim)
}

// Backdrop is a background image at the size it is shown at, blurred,
// with its pixels off the Go heap. Image keeps its identity for as long
// as the pixels live, so a GPU renderer's texture of it can be dropped
// by ID before the pixels are freed.
type Backdrop struct {
	Image render.StillImage
	pix   memory.Slice[byte]
	mem   *memory.Shared
}

// LoadBackdrop decodes the background at path into mem and makes it a
// backdrop for outputs, as NewBackdrop does; the decoded image is
// freed before it returns.
func LoadBackdrop(mem *memory.Shared, decode func(mem *memory.Shared, path string) (imagedecode.Decoded, error), path string, sigma float64, outputs []image.Point) (*Backdrop, error) {
	src, err := decode(mem, path)
	if err != nil {
		return nil, fmt.Errorf("background %s unreadable: %w", path, err)
	}
	bg, err := NewBackdrop(mem, src.Image, sigma, outputs)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("background %s unusable: %w", path, err), src.Free())
	}
	if err := src.Free(); err != nil {
		return nil, errors.Join(fmt.Errorf("freeing the decoded background %s: %w", path, err), bg.Free())
	}
	return &bg, nil
}

// NewBackdrop resamples src to the smallest size that still covers
// every one of outputs (device pixel sizes) and blurs it with a
// gaussian of standard deviation sigma in src's pixels, scaled with
// the image so the result looks as blurring src itself would. A src
// already no larger than that keeps its size: it is blurred as it is
// and scaled up where it is drawn. The pixels and the blur's scratch
// raster live in mem; the scratch is freed before it returns.
func NewBackdrop(mem *memory.Shared, src image.Image, sigma float64, outputs []image.Point) (Backdrop, error) {
	b := src.Bounds()
	if b.Empty() {
		return Backdrop{}, fmt.Errorf("cannot show an empty %v background", b)
	}
	scale := coverScale(b.Size(), outputs)
	size := scaledSize(b.Size(), scale)
	img, pix, err := offheap.NewRGBA(mem, size.X, size.Y)
	if err != nil {
		return Backdrop{}, err
	}
	areaResample(img, src)
	if err := blurOffHeap(mem, img, sigma*scale); err != nil {
		return Backdrop{}, errors.Join(err, offheap.Free(mem, pix))
	}
	return Backdrop{Image: render.NewStillImage(img), pix: pix, mem: mem}, nil
}

// Free frees the backdrop's pixels; nothing may draw them any more.
func (b Backdrop) Free() error {
	return offheap.Free(b.mem, b.pix)
}

// Retire lets go of the backdrop once no surface draws it: forget drops
// the renderer's texture now, and the pixels are freed once every List
// recorded until now has ended, so no frame still being played reads
// freed memory. A failed free is logged under who.
func (b Backdrop) Retire(forget func(render.ImageID), who string) {
	forget(b.Image.ID)
	render.Retire(func() {
		if err := b.Free(); err != nil {
			log.Printf("%s: freeing the background: %v", who, err)
		}
	})
}

// coverScale is the factor src scales by so it covers the largest of
// outputs, at most 1: the background is never enlarged before the
// blur. No outputs keeps the source size.
func coverScale(src image.Point, outputs []image.Point) float64 {
	scale := 0.0
	for _, o := range outputs {
		scale = max(scale, float64(o.X)/float64(src.X), float64(o.Y)/float64(src.Y))
	}
	if scale == 0 {
		return 1
	}
	return min(scale, 1)
}

// scaledSize is src scaled by scale, rounded up so the binding axis
// lands on the output's size exactly and the other still covers it.
func scaledSize(src image.Point, scale float64) image.Point {
	axis := func(n int) int { return max(1, int(math.Ceil(float64(n)*scale-1e-6))) }
	return image.Pt(axis(src.X), axis(src.Y))
}

// areaResample averages src into dst, each destination pixel the mean
// of the source area it covers, partial source pixels weighted by how
// much of them it covers: the downscale that cannot alias. dst is no
// larger than src on either axis. Memory stays bounded by rows: src is
// read one row at a time into a premultiplied RGBA row.
func areaResample(dst *image.RGBA, src image.Image) {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dw, dh := dst.Rect.Dx(), dst.Rect.Dy()
	row := image.NewRGBA(image.Rect(0, 0, sw, 1))
	cols := areaSpans(sw, dw)
	lines := areaSpans(sh, dh)
	across := make([]float32, 4*dw)
	cur, next := make([]float32, 4*dw), make([]float32, 4*dw)
	norm := float32(float64(dw) * float64(dh) / (float64(sw) * float64(sh)))
	at := 0
	flush := func() {
		out := dst.Pix[at*dst.Stride : at*dst.Stride+4*dw]
		for i, v := range cur {
			out[i] = uint8(min(v*norm+0.5, 255))
		}
	}
	for y, line := range lines {
		draw.Draw(row, row.Rect, src, image.Pt(sb.Min.X, sb.Min.Y+y), draw.Src)
		clear(across)
		for x, col := range cols {
			p := row.Pix[4*x : 4*x+4]
			spread(across, col, p)
		}
		if line.to > at {
			flush()
			cur, next = next, cur
			clear(next)
			at = line.to
		}
		addWeighted(cur, across, line.first)
		if line.second > 0 {
			addWeighted(next, across, line.second)
		}
	}
	flush()
}

// areaSpan is where one source pixel of an axis lands in the
// destination: weight first in pixel to, the rest, second, in to+1.
type areaSpan struct {
	to            int
	first, second float32
}

// areaSpans maps each of n source pixels onto m destination pixels
// (m <= n) by the fraction of each destination pixel it covers, in
// units of a source pixel.
func areaSpans(n, m int) []areaSpan {
	spans := make([]areaSpan, n)
	for i := range spans {
		start := i * m
		to := start / n
		edge := (to + 1) * n
		if start+m <= edge {
			spans[i] = areaSpan{to: to, first: 1}
			continue
		}
		first := float32(edge-start) / float32(m)
		spans[i] = areaSpan{to: to, first: first, second: 1 - first}
	}
	return spans
}

// spread adds the pixel p to row by its span.
func spread(row []float32, s areaSpan, p []uint8) {
	o := 4 * s.to
	for c := range 4 {
		row[o+c] += s.first * float32(p[c])
	}
	if s.second > 0 {
		for c := range 4 {
			row[o+4+c] += s.second * float32(p[c])
		}
	}
}

// addWeighted adds w times src to dst.
func addWeighted(dst, src []float32, w float32) {
	for i, v := range src {
		dst[i] += w * v
	}
}

// blurOffHeap blurs img in place through a scratch raster mem holds
// while it runs.
func blurOffHeap(mem *memory.Shared, img *image.RGBA, sigma float64) error {
	if sigma <= 0 {
		return nil
	}
	tmp, err := memory.MakeSlice[byte](mem, len(img.Pix))
	if err != nil {
		return fmt.Errorf("cannot allocate the blur's %v scratch: %w", img.Rect, err)
	}
	blur(img, tmp.Elems(), sigma)
	return offheap.Free(mem, tmp)
}

// blur approximates a gaussian of standard deviation sigma (the image
// crate's blur) over img in place with three box passes per axis, the
// classic linear-time approximation; tmp holds img's pixels between
// passes.
func blur(img *image.RGBA, tmp []uint8, sigma float64) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	sums := make([]uint64, 4*w)
	for _, r := range boxRadii(sigma, 3) {
		boxRows(img.Pix, tmp, w, h, img.Stride, r)
		boxColumns(tmp, img.Pix, w, h, img.Stride, r, sums)
	}
}

// boxRadii picks n box radii whose successive application matches a
// gaussian of sigma (Kovesi, "Fast Almost-Gaussian Filtering").
func boxRadii(sigma float64, n int) []int {
	ideal := math.Sqrt(12*sigma*sigma/float64(n) + 1)
	lower := int(math.Floor(ideal))
	if lower%2 == 0 {
		lower--
	}
	upper := lower + 2
	m := int(math.Round((12*sigma*sigma - float64(n*lower*lower) - 4*float64(n*lower) - 3*float64(n)) / (-4*float64(lower) - 4)))
	radii := make([]int, n)
	for i := range radii {
		width := upper
		if i < m {
			width = lower
		}
		radii[i] = (width - 1) / 2
	}
	return radii
}

// spanDivisor divides a box's sum by its span with a multiply instead
// of a division, exactly: the sum of span bytes is below 255 * span,
// and the multiplier's error stays under one span for every such sum
// while 255 * span * span fits 64 bits.
type spanDivisor struct{ m uint64 }

func newSpanDivisor(span int) spanDivisor {
	return spanDivisor{m: ^uint64(0)/intconv.Clamp[uint64](span) + 1}
}

// mean is sum / span.
func (d spanDivisor) mean(sum uint64) uint8 {
	q, _ := bits.Mul64(sum, d.m)
	return uint8(q & 0xff)
}

// boxRows blurs each row of src into dst with a box of the given
// radius, clamping at the edges.
func boxRows(src, dst []uint8, w, h, stride, radius int) {
	div := newSpanDivisor(2*radius + 1)
	for y := range h {
		in := src[y*stride : y*stride+4*w]
		out := dst[y*stride : y*stride+4*w]
		var r, g, b, a uint64
		for i := -radius; i <= radius; i++ {
			p := in[4*min(max(i, 0), w-1):]
			r, g, b, a = r+uint64(p[0]), g+uint64(p[1]), b+uint64(p[2]), a+uint64(p[3])
		}
		for x := range w {
			o := out[4*x : 4*x+4]
			o[0], o[1], o[2], o[3] = div.mean(r), div.mean(g), div.mean(b), div.mean(a)
			enter := in[4*min(x+radius+1, w-1):]
			enter = enter[:4]
			leave := in[4*max(x-radius, 0):]
			leave = leave[:4]
			r += uint64(enter[0]) - uint64(leave[0])
			g += uint64(enter[1]) - uint64(leave[1])
			b += uint64(enter[2]) - uint64(leave[2])
			a += uint64(enter[3]) - uint64(leave[3])
		}
	}
}

// boxColumns blurs each column of src into dst with a box of the
// given radius, clamping at the edges. It walks the rows in order,
// keeping every column's running sum in sums (4*w long).
func boxColumns(src, dst []uint8, w, h, stride, radius int, sums []uint64) {
	div := newSpanDivisor(2*radius + 1)
	rowOf := func(y int) []uint8 {
		y = min(max(y, 0), h-1)
		return src[y*stride : y*stride+4*w]
	}
	clear(sums)
	for i := -radius; i <= radius; i++ {
		for k, v := range rowOf(i) {
			sums[k] += uint64(v)
		}
	}
	for y := range h {
		out := dst[y*stride : y*stride+4*w]
		enter, leave := rowOf(y+radius+1), rowOf(y-radius)
		enter, leave, sums := enter[:len(out)], leave[:len(out)], sums[:len(out)]
		for k := range out {
			out[k] = div.mean(sums[k])
			sums[k] += uint64(enter[k]) - uint64(leave[k])
		}
	}
}
