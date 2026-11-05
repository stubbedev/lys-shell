package sharepreview

import (
	"errors"
	"fmt"
	"image"

	"github.com/stubbedev/lys-lib/capture"
)

// WindowThumb captures a window preview: Hyprland's toplevel-export
// when XDPH sent the address, otherwise (or when that fails) the ext
// path matched by class and title. The frame is converted without an
// output transform and downscaled to size.
func WindowThumb(tl Toplevel, size int) (*image.RGBA, error) {
	c, err := capture.Connect()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to wayland: %w", err)
	}
	defer func() { _ = c.Close() }()
	var f *capture.Frame
	if tl.HasAddress && c.HasHyprlandExport() {
		f, err = c.CaptureHyprlandWindow(tl.WindowAddress, capture.Options{})
		if err != nil {
			f = nil
		}
	}
	if f == nil {
		if !c.HasToplevelCapture() {
			return nil, errors.New("window capture not supported on this compositor")
		}
		tls, err := c.Toplevels()
		if err != nil {
			return nil, err
		}
		for _, cand := range tls {
			if tl.matches(cand) {
				f, err = c.CaptureToplevel(cand, capture.Options{})
				if err != nil {
					return nil, fmt.Errorf("window capture failed: %w", err)
				}
				break
			}
		}
		if f == nil {
			return nil, errors.New("could not match the window to capture")
		}
	}
	img, err := f.Image()
	if err != nil {
		return nil, err
	}
	return ResizeToFit(img, size), nil
}

// matches reports whether cand is the window: by its stable identifier
// when tl has one, else by class and title.
func (tl Toplevel) matches(cand capture.Toplevel) bool {
	if tl.Identifier != "" {
		return cand.Identifier == tl.Identifier
	}
	return cand.AppID == tl.Class && cand.Title == tl.Title
}
