// Package sharepreview is the capture half of wayle-share-preview on
// top of lys-lib's capture package: turning captured frames into images
// (the output-transform correction, the preview downscale) and the XDPH
// window list the share picker reads.
package sharepreview

import (
	"image"

	"github.com/stubbedev/lys-lib/capture"
	xdraw "golang.org/x/image/draw"
)

// FrameImage converts a captured frame to an image and applies the
// output transform, the Image::new → into_rgb → transform chain of
// image.rs. Frames without alpha come out opaque.
func FrameImage(f *capture.Frame, t capture.Transform) (*image.RGBA, error) {
	img, err := f.Image()
	if err != nil {
		return nil, err
	}
	return Transform(img, t), nil
}

// Transform applies an output transform to a captured frame the way
// image.rs's Image::transform does: the 90/270 variants rotate
// clockwise (image's rotate90/rotate270), and the flipped variants
// flip vertically first. TransformNormal returns img itself.
func Transform(img *image.RGBA, t capture.Transform) *image.RGBA {
	if t == capture.TransformNormal {
		return img
	}
	w, h := TransformedSize(img.Bounds().Dx(), img.Bounds().Dy(), t)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	TransformInto(dst, img, t)
	return dst
}

// TransformedSize is the size of a w x h frame once t is applied.
func TransformedSize(w, h int, t capture.Transform) (int, int) {
	if t.SwapsAxes() {
		return h, w
	}
	return w, h
}

// TransformInto writes src with t applied into dst, for a caller that
// owns dst's memory; dst has src's TransformedSize.
func TransformInto(dst, src *image.RGBA, t capture.Transform) {
	b := src.Bounds()
	at := sourceOf(t, b.Dx(), b.Dy())
	d := dst.Bounds()
	for y := range d.Dy() {
		for x := range d.Dx() {
			sx, sy := at(x, y)
			si := src.PixOffset(b.Min.X+sx, b.Min.Y+sy)
			di := dst.PixOffset(d.Min.X+x, d.Min.Y+y)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
}

// sourceOf maps a pixel of a w x h source with t applied back to the
// source pixel it shows. The flipped variants flip vertically before
// they rotate, so their source is the rotation's source flipped.
func sourceOf(t capture.Transform, w, h int) func(x, y int) (int, int) {
	switch t {
	case capture.Transform90:
		return func(x, y int) (int, int) { return y, h - 1 - x }
	case capture.Transform180:
		return func(x, y int) (int, int) { return w - 1 - x, h - 1 - y }
	case capture.Transform270:
		return func(x, y int) (int, int) { return w - 1 - y, x }
	case capture.TransformFlipped:
		return func(x, y int) (int, int) { return x, h - 1 - y }
	case capture.TransformFlipped90:
		return func(x, y int) (int, int) { return y, x }
	case capture.TransformFlipped180:
		return func(x, y int) (int, int) { return w - 1 - x, y }
	case capture.TransformFlipped270:
		return func(x, y int) (int, int) { return w - 1 - y, h - 1 - x }
	}
	return func(x, y int) (int, int) { return x, y }
}

// Resize scales the image to w x h with a triangle (bilinear) filter,
// image's FilterType::Triangle.
func Resize(src *image.RGBA, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
	return dst
}

// ResizeToFit downscales the image so its smaller dimension is at most
// size, keeping the aspect ratio - image.rs's resize_to_fit, including
// its quirks: a portrait image scales when its width exceeds size, a
// landscape one when its height does, and a square image never scales.
func ResizeToFit(src *image.RGBA, size int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	aspect := float64(w) / float64(h)
	switch {
	case h > w && w > size:
		return Resize(src, size, int(float64(size)/aspect))
	case w > h && h > size:
		return Resize(src, int(float64(size)*aspect), size)
	}
	return src
}
