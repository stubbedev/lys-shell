package wallpaper

import (
	"image"
	"image/draw"
	"math"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/internal/offheap"
	"github.com/stubbedev/lys-shell/internal/resample"
	"github.com/stubbedev/lys-shell/service/wallpaper"
)

// target is the device-pixel box a wallpaper is prepared for and the
// output's device scale (device px per logical px).
type target struct {
	w, h  int
	scale float64
}

// plan is where a wallpaper of bounds b lands on its monitor: the
// source rect and the size it is resampled to, so the surface blits it
// 1:1 instead of rescaling a multi-megapixel source on the frame path.
// It is shown with ImageScaleDown, which draws it at its natural size
// once it fits the box. The fits mirror the GTK ContentFit the Rust
// shell maps each FitMode to (content_fit): Fill is Cover, Fit is
// Contain, Stretch is Fill, and Center is ScaleDown with the natural
// size in logical pixels, as GTK measures a texture.
func plan(b image.Rectangle, fit wallpaper.FitMode, box target) (image.Rectangle, image.Point) {
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 || box.w <= 0 || box.h <= 0 {
		return b, b.Size()
	}
	var (
		src image.Rectangle
		dst render.Rect
	)
	switch fit {
	case wallpaper.FitFit:
		src, dst = render.PlaceImage(sw, sh, box.w, box.h, render.ImageFit, render.ImageCentered)
	case wallpaper.FitStretch:
		src, dst = render.PlaceImage(sw, sh, box.w, box.h, render.ImageStretch, render.ImageCentered)
	case wallpaper.FitCenter:
		nw := int(math.Round(float64(sw) * box.scale))
		nh := int(math.Round(float64(sh) * box.scale))
		if nw <= box.w && nh <= box.h {
			src, dst = image.Rect(0, 0, sw, sh), render.Rect{W: nw, H: nh}
		} else {
			src, dst = render.PlaceImage(sw, sh, box.w, box.h, render.ImageFit, render.ImageCentered)
		}
	default: // FitFill
		src, dst = render.PlaceImage(sw, sh, box.w, box.h, render.ImageCover, render.ImageCentered)
	}
	return src.Add(b.Min), image.Pt(dst.W, dst.H)
}

// rasterize makes the raster img is shown as on a monitor of box (the
// whole image, drawn by the fit's widget policy, when known is false
// because the output's size is not reported yet) in mem: resampled from
// img's rows straight into it, or converted as it is when no resample
// is needed, with no image-sized copy on the Go heap.
func rasterize(mem *memory.Shared, img image.Image, fit wallpaper.FitMode, box target, known bool) (*raster, error) {
	b := img.Bounds()
	src, size, scale := b, b.Size(), widgetScale(fit)
	if known {
		src, size = plan(b, fit, box)
		scale = widget.ImageScaleDown
	}
	dst, pix, err := offheap.NewRGBA(mem, size.X, size.Y)
	if err != nil {
		return nil, err
	}
	if src == b && size == b.Size() {
		draw.Draw(dst, dst.Rect, img, b.Min, draw.Src)
	} else {
		resample.Into(dst, img, src)
	}
	return &raster{image: render.NewStillImage(dst), scale: scale, pix: pix}, nil
}

// widgetScale is the lys-ui policy for an image that could not be
// prepared (the output size is unknown): the closest live equivalent.
func widgetScale(fit wallpaper.FitMode) widget.ImageScale {
	switch fit {
	case wallpaper.FitFit:
		return widget.ImageFit
	case wallpaper.FitStretch:
		return widget.ImageStretch
	case wallpaper.FitCenter:
		return widget.ImageScaleDown
	}
	return widget.ImageCover
}
