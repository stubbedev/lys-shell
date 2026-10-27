// Package outputsize reads the size a surface covering an output has
// in device pixels.
package outputsize

import (
	"image"

	"github.com/stubbedev/lys-ui/app"
)

// Device is the output's current mode in device pixels, turned with a
// 90 or 270 degree transform (flipped or not); false when the
// compositor has not reported a mode yet.
func Device(out *app.Output) (image.Point, bool) {
	w, h := out.ModeW, out.ModeH
	if out.Transform%2 == 1 {
		w, h = h, w
	}
	if w <= 0 || h <= 0 {
		return image.Point{}, false
	}
	return image.Pt(w, h), true
}

// Devices are the device pixel sizes of the outputs that report a
// mode, in their order.
func Devices(outputs []*app.Output) []image.Point {
	sizes := make([]image.Point, 0, len(outputs))
	for _, o := range outputs {
		if size, ok := Device(o); ok {
			sizes = append(sizes, size)
		}
	}
	return sizes
}
