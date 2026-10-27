// Package loupe is the frozen frame the color picker and the region
// overlay paint behind everything, and the magnified pixel grid they
// show near the pointer over it: the color picker's card and the region
// overlay's magnifier.
package loupe

import (
	"image"
	"image/color"
	"math"

	"github.com/stubbedev/lys-lib/render"
)

// Grid geometry of color_picker/mod.rs.
const (
	// Zoom is the size of one magnified source pixel.
	Zoom = 11
	// Radius is the grid's radius in source pixels: the grid is
	// 2*Radius+1 square.
	Radius = 8
	// Size is the grid's edge in logical pixels.
	Size = (2*Radius + 1) * Zoom
	// Offset is how far a card sits from the cursor, on each axis.
	Offset = 24.0
)

// The centre pixel's box: white, ringed in black so it reads on any
// color.
var (
	crossWhite = render.RGBA(255, 255, 255, 242)
	crossBlack = render.RGBA(0, 0, 0, 230)
)

// DrawFrame paints the frozen frame over the logical box: one frame
// pixel per device pixel when the frame has the box's device size, the
// output's usual case, else scaled into it as it is drawn. The frame is
// drawn from its own pixels, never from a resampled copy, so the
// surface holds no raster beyond the capture.
func DrawFrame(cv *render.Canvas, frame render.StillImage, box render.Rect) {
	dev := cv.MapRect(box)
	if b := frame.Bounds(); b.Dx() == dev.W && b.Dy() == dev.H {
		cv.DrawImageDevice(frame, dev.X, dev.Y)
		return
	}
	cv.DrawImageCover(frame, box, render.Corners{})
}

// SampleAt is the frame pixel under a surface-local logical point: the
// point scaled to the frame's physical resolution, truncated and
// clamped.
func SampleAt(img *image.RGBA, logicalW, logicalH int, lx, ly float64) (int, int) {
	b := img.Bounds()
	sx := float64(b.Dx()) / float64(max(logicalW, 1))
	sy := float64(b.Dy()) / float64(max(logicalH, 1))
	return clampIndex(int(lx*sx), b.Dx()-1), clampIndex(int(ly*sy), b.Dy()-1)
}

// Pixel reads img at frame coordinates.
func Pixel(img *image.RGBA, x, y int) color.RGBA {
	return img.RGBAAt(img.Bounds().Min.X+x, img.Bounds().Min.Y+y)
}

// clampIndex clamps v into [0, hi].
func clampIndex(v, hi int) int { return min(max(v, 0), hi) }

// Place is the top-left of a w x h card near the cursor (cx, cy) in
// box: Offset below and right of it, flipped to the other side on an
// axis where it would leave the box.
func Place(box render.Rect, cx, cy float64, w, h int) (int, int) {
	px, py := cx+Offset, cy+Offset
	if px+float64(w) > float64(box.W) {
		px = cx - Offset - float64(w)
	}
	if py+float64(h) > float64(box.H) {
		py = cy - Offset - float64(h)
	}
	return box.X + int(math.Max(px, 0)), box.Y + int(math.Max(py, 0))
}

// Draw paints the grid of img's pixels around the frame pixel
// (centerX, centerY) with its top-left at (x0, y0), the centre pixel
// boxed.
func Draw(cv *render.Canvas, img *image.RGBA, centerX, centerY, x0, y0 int) {
	b := img.Bounds()
	for gy := -Radius; gy <= Radius; gy++ {
		for gx := -Radius; gx <= Radius; gx++ {
			c := Pixel(img, clampIndex(centerX+gx, b.Dx()-1), clampIndex(centerY+gy, b.Dy()-1))
			cv.FillRect(render.Rect{X: x0 + (gx+Radius)*Zoom, Y: y0 + (gy+Radius)*Zoom, W: Zoom, H: Zoom}, render.RGB(c.R, c.G, c.B))
		}
	}
	cx0, cy0 := x0+Radius*Zoom, y0+Radius*Zoom
	cv.BorderRect(render.Rect{X: cx0 - 1, Y: cy0 - 1, W: Zoom + 2, H: Zoom + 2}, 2, crossWhite)
	cv.BorderRect(render.Rect{X: cx0 - 2, Y: cy0 - 2, W: Zoom + 4, H: Zoom + 4}, 1, crossBlack)
}
