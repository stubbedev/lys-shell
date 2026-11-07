package material

import "image"

// Sample reads img at a grid of at most side samples along its longer
// edge, keeping its aspect, one pixel at the center of each cell, and
// returns the opaque ones. Its work and its one allocation are bounded
// by the grid, whatever the image's resolution.
func Sample(img *image.RGBA, side int) []ARGB {
	b := img.Rect
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || side <= 0 {
		return nil
	}
	cols, rows := min(side, w), min(side, h)
	if w >= h {
		rows = max(1, min(rows, h*cols/w))
	} else {
		cols = max(1, min(cols, w*rows/h))
	}
	out := make([]ARGB, 0, cols*rows)
	for row := range rows {
		y := (2*row + 1) * h / (2 * rows)
		for col := range cols {
			x := (2*col + 1) * w / (2 * cols)
			o := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			p := img.Pix[o : o+4 : o+4]
			if p[3] != 0xff {
				continue
			}
			out = append(out, RGB(p[0], p[1], p[2]))
		}
	}
	return out
}
