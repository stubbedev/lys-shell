package markup

import (
	"image"
	"math"
	"strconv"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/render"
)

const (
	cellPx     = 12
	ellipseK   = 0.5522847498
	headRatio  = 4
	headMinPx  = 10
	markerBase = 14
	textBase   = 10
)

var markerInk = render.RGB(255, 255, 255)

// View places image pixels on a canvas: a pixel p lands at
// (X + p.X*Scale, Y + p.Y*Scale) in the canvas's logical pixels.
type View struct {
	X, Y, Scale float64
}

func (v View) at(p image.Point) (float64, float64) {
	return v.X + float64(float64(p.X)*v.Scale), v.Y + float64(float64(p.Y)*v.Scale)
}

func (v View) point(p image.Point) (int, int) {
	x, y := v.at(p)
	return int(math.Round(x)), int(math.Round(y))
}

func (v View) rect(r image.Rectangle) render.Rect {
	x0, y0 := v.point(r.Min)
	x1, y1 := v.point(r.Max)
	return render.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func (v View) length(n float64) float64 { return n * v.Scale }

func (v View) stroke(n int) int { return max(1, int(math.Round(v.length(float64(n))))) }

// Image maps a canvas point back to the image pixel under it.
func (v View) Image(x, y float64) image.Point {
	return image.Pt(int(math.Floor((x-v.X)/v.Scale)), int(math.Floor((y-v.Y)/v.Scale)))
}

// Paint draws marks over the image placed by v.
func Paint(cv *render.Canvas, marks []Mark, v View, font render.Font) {
	for _, m := range marks {
		PaintMark(cv, m, v, font)
	}
}

// PaintMark draws one mark over the image placed by v. A crop draws
// nothing: it decides what is kept, not what is drawn.
func PaintMark(cv *render.Canvas, m Mark, v View, font render.Font) {
	switch m.Tool {
	case ToolRect:
		cv.BorderRect(v.rect(m.Rect()), v.stroke(m.Size), m.Color)
	case ToolHighlight:
		cv.FillRect(v.rect(m.Rect()), highlight(m.Color))
	case ToolEllipse:
		cv.FillPath(ellipseRing(v, m.Rect(), v.length(float64(m.Size))), m.Color)
	case ToolArrow:
		paintArrow(cv, m, v)
	case ToolFreehand:
		for i := 1; i < len(m.Points); i++ {
			x0, y0 := v.point(m.Points[i-1])
			x1, y1 := v.point(m.Points[i])
			cv.Line(x0, y0, x1, y1, v.stroke(m.Size), m.Color)
		}
	case ToolText:
		if font == nil {
			return
		}
		x, y := v.point(m.Points[0])
		st := font.Shape(m.Text, v.length(TextPx(m.Size)))
		st.Draw(cv, x, y+int(math.Round(st.Ascent())), m.Color)
	case ToolMarker:
		paintMarker(cv, m, v, font)
	case ToolPixelate:
		paintCells(cv, m, v)
	case ToolCrop:
	}
}

// TextPx is the font size a text mark of stroke size draws at, in image pixels.
func TextPx(size int) float64 { return float64(textBase + 4*size) }

func highlight(c render.Color) render.Color {
	s := c.Straight()
	return render.RGBA(s[0], s[1], s[2], 96)
}

func paintArrow(cv *render.Canvas, m Mark, v View) {
	from, to := m.Points[0], m.Points[len(m.Points)-1]
	fx, fy := v.at(from)
	tx, ty := v.at(to)
	dx, dy := tx-fx, ty-fy
	n := math.Sqrt(float64(dx*dx) + float64(dy*dy))
	if n == 0 {
		return
	}
	ux, uy := dx/n, dy/n
	head := math.Min(v.length(float64(max(headMinPx, headRatio*m.Size))), n)
	bx, by := tx-float64(ux*head), ty-float64(uy*head)
	cv.Line(int(math.Round(fx)), int(math.Round(fy)), int(math.Round(bx)), int(math.Round(by)), v.stroke(m.Size), m.Color)
	half := head * 0.6
	var p render.Path
	p.MoveTo(tx, ty)
	p.LineTo(bx-float64(uy*half), by+float64(ux*half))
	p.LineTo(bx+float64(uy*half), by-float64(ux*half))
	p.Close()
	cv.FillPath(&p, m.Color)
}

// MarkerPx is a marker's diameter at stroke size, in image pixels.
func MarkerPx(size int) int { return markerBase + 4*size }

func paintMarker(cv *render.Canvas, m Mark, v View, font render.Font) {
	d := MarkerPx(m.Size)
	c := m.Points[0]
	r := v.rect(image.Rect(c.X-d/2, c.Y-d/2, c.X-d/2+d, c.Y-d/2+d))
	cv.RoundedRect(r, r.W/2, m.Color)
	if font == nil {
		return
	}
	font.DrawAligned(cv, strconv.Itoa(m.Number), r, v.length(float64(d)*0.6), markerInk, render.AlignCenter)
}

func ellipseRing(v View, r image.Rectangle, width float64) *render.Path {
	x0, y0 := v.at(r.Min)
	x1, y1 := v.at(r.Max)
	cx, cy := float64((x0+x1)/2), float64((y0+y1)/2)
	rx, ry := float64((x1-x0)/2), float64((y1-y0)/2)
	var p render.Path
	half := float64(width / 2)
	ellipse(&p, cx, cy, rx+half, ry+half, false)
	if irx, iry := rx-half, ry-half; irx > 0 && iry > 0 {
		ellipse(&p, cx, cy, irx, iry, true)
	}
	return &p
}

func ellipse(p *render.Path, cx, cy, rx, ry float64, reverse bool) {
	kx, ky := float64(rx*ellipseK), float64(ry*ellipseK)
	if reverse {
		ky = -ky
		ry = -ry
	}
	p.MoveTo(cx+rx, cy)
	p.CubeTo(cx+rx, cy+ky, cx+kx, cy+ry, cx, cy+ry)
	p.CubeTo(cx-kx, cy+ry, cx-rx, cy+ky, cx-rx, cy)
	p.CubeTo(cx-rx, cy-ky, cx-kx, cy-ry, cx, cy-ry)
	p.CubeTo(cx+kx, cy-ry, cx+rx, cy-ky, cx+rx, cy)
	p.Close()
}

func cellGrid(r image.Rectangle) (cols, rows int) {
	return (r.Dx() + cellPx - 1) / cellPx, (r.Dy() + cellPx - 1) / cellPx
}

func cellRect(r image.Rectangle, col, row int) image.Rectangle {
	at := r.Min.Add(image.Pt(col*cellPx, row*cellPx))
	return image.Rectangle{Min: at, Max: at.Add(image.Pt(cellPx, cellPx))}.Intersect(r)
}

func pixelCells(img *image.RGBA, r image.Rectangle) []render.Color {
	cols, rows := cellGrid(r)
	cells := make([]render.Color, 0, cols*rows)
	for row := range rows {
		for col := range cols {
			cells = append(cells, average(img, cellRect(r, col, row)))
		}
	}
	return cells
}

func average(img *image.RGBA, r image.Rectangle) render.Color {
	var sr, sg, sb, sa, n uint64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[img.PixOffset(r.Min.X, y):img.PixOffset(r.Max.X, y)]
		for i := 0; i < len(row); i += 4 {
			sr += uint64(row[i])
			sg += uint64(row[i+1])
			sb += uint64(row[i+2])
			sa += uint64(row[i+3])
			n++
		}
	}
	if n == 0 {
		return 0
	}
	mean := func(s uint64) uint32 { return intconv.Must[uint32]((s + n/2) / n) }
	return render.Color(mean(sa)<<24 | mean(sr)<<16 | mean(sg)<<8 | mean(sb))
}

func paintCells(cv *render.Canvas, m Mark, v View) {
	r := m.Rect()
	cols, rows := cellGrid(r)
	if len(m.cells) != cols*rows {
		return
	}
	for row := range rows {
		for col := range cols {
			cv.FillRect(v.rect(cellRect(r, col, row)), m.cells[row*cols+col])
		}
	}
}

// Flatten renders the document as it is saved: the marks burned into a
// copy of the captured pixels, cropped. Pixelated cells replace the
// pixels under them, so nothing of the original survives there.
func Flatten(d *Doc, font render.Font) *image.RGBA {
	b := d.base.Bounds()
	w, h := b.Dx(), b.Dy()
	stride := render.Stride(w)
	data := make([]byte, stride*h)
	cv := render.New(data, stride, w, h)
	cv.DrawImage(d.base, 0, 0)
	Paint(cv, d.Marks(), View{X: float64(-b.Min.X), Y: float64(-b.Min.Y), Scale: 1}, font)
	crop := d.Crop().Sub(b.Min)
	out := image.NewRGBA(image.Rect(0, 0, crop.Dx(), crop.Dy()))
	for y := range crop.Dy() {
		src := data[(crop.Min.Y+y)*stride+crop.Min.X*4:]
		dst := out.Pix[y*out.Stride : y*out.Stride+crop.Dx()*4]
		for i := 0; i < len(dst); i += 4 {
			dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], src[i+3]
		}
	}
	return out
}
