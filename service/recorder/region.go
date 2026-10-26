package recorder

import (
	"fmt"
	"image"
	"math"

	"github.com/stubbedev/lys-lib/capture"
)

// scaleDenominator is the granularity of fractional output scales
// (wp-fractional-scale-v1 counts in 120ths).
const scaleDenominator = 120

// outputScale is the output's scale: its upright pixel width over its
// logical width, snapped to 120ths, the steps fractional scales come in.
func outputScale(upright, logical image.Point) float64 {
	return math.Round(float64(upright.X)/float64(logical.X)*scaleDenominator) / scaleDenominator
}

// uprightSize is a buffer's size once the output's transform turns it.
func uprightSize(buffer image.Point, t capture.Transform) image.Point {
	if t.SwapsAxes() {
		return image.Pt(buffer.Y, buffer.X)
	}
	return buffer
}

// regionSource maps a region of an output, in its upright logical
// coordinates as the picker gives it, onto the output's buffer: turned
// back by the output's transform within its logical size, then scaled,
// each coordinate truncated, which is how wlroots maps a screencopy
// region. A region not inside the output is an error naming both.
func regionSource(region image.Rectangle, logical, buffer image.Point, t capture.Transform) (image.Rectangle, error) {
	if region.Empty() || !region.In(image.Rectangle{Max: logical}) {
		return image.Rectangle{}, fmt.Errorf("recorder: region %v is not inside the %dx%d output", region, logical.X, logical.Y)
	}
	box := untransformBox(region, t, logical)
	scale := outputScale(uprightSize(buffer, t), logical)
	scaled := func(v int) int { return int(float64(v) * scale) }
	x, y := scaled(box.Min.X), scaled(box.Min.Y)
	src := image.Rect(x, y, x+scaled(box.Dx()), y+scaled(box.Dy()))
	if !src.In(image.Rectangle{Max: buffer}) {
		return image.Rectangle{}, fmt.Errorf("recorder: region %v maps to %v, outside the %dx%d buffer", region, src, buffer.X, buffer.Y)
	}
	return src, nil
}

// untransformBox turns box, in the upright space of size upright, back
// by t into the buffer's orientation: wlr_box_transform with t
// inverted.
func untransformBox(box image.Rectangle, t capture.Transform, upright image.Point) image.Rectangle {
	x, y, w, h := box.Min.X, box.Min.Y, box.Dx(), box.Dy()
	width, height := upright.X, upright.Y
	var dx, dy int
	// The inverse of a rotation by 90 is one by 270; flips are their
	// own inverses.
	switch t {
	case capture.Transform90:
		dx, dy = y, width-x-w
	case capture.Transform180:
		dx, dy = width-x-w, height-y-h
	case capture.Transform270:
		dx, dy = height-y-h, x
	case capture.TransformFlipped:
		dx, dy = width-x-w, y
	case capture.TransformFlipped90:
		dx, dy = y, x
	case capture.TransformFlipped180:
		dx, dy = x, height-y-h
	case capture.TransformFlipped270:
		dx, dy = height-y-h, width-x-w
	default:
		dx, dy = x, y
	}
	if t.SwapsAxes() {
		w, h = h, w
	}
	return image.Rect(dx, dy, dx+w, dy+h)
}
