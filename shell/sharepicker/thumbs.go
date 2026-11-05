package sharepicker

import (
	"fmt"
	"image"

	"github.com/stubbedev/lys-lib/capture"

	"github.com/stubbedev/lys-shell/service/sharepreview"
)

// fallbackToplevels enumerates windows through
// ext-foreign-toplevel-list when XDPH sent no list (the picker run
// standalone, or a non-XDPH portal). The ids are display-only indexes:
// they never round-trip to a portal. Capture re-matches the window by
// class and title, and the payload carries the stable identifier.
func fallbackToplevels() []sharepreview.Toplevel {
	c, err := capture.Connect()
	if err != nil {
		return nil
	}
	defer func() { _ = c.Close() }()
	tls, err := c.Toplevels()
	if err != nil {
		return nil
	}
	out := make([]sharepreview.Toplevel, len(tls))
	for i, tl := range tls {
		out[i] = sharepreview.Toplevel{ID: uint64(i), Class: tl.AppID, Title: tl.Title, Identifier: tl.Identifier}
	}
	return out
}

// outputInfo is one output as the outputs page lays it out: the
// wl_output geometry position and the mode with the transform's axis
// swap applied (util.rs's OutputInfo).
type outputInfo struct {
	name          string
	x, y          int
	width, height int
	scale         int
	transform     capture.Transform
}

// outputInfos lists the named outputs with a mode.
func outputInfos(outs []capture.Output) []outputInfo {
	var infos []outputInfo
	for _, o := range outs {
		if o.Name == "" || o.Width <= 0 || o.Height <= 0 {
			continue
		}
		w, h := int(o.Width), int(o.Height)
		if o.Transform.SwapsAxes() {
			w, h = h, w
		}
		infos = append(infos, outputInfo{name: o.Name, x: int(o.X), y: int(o.Y), width: w, height: h, scale: int(max(o.Scale, 1)), transform: o.Transform})
	}
	return infos
}

// applyScaling divides each scaled output's extent by its scale, so
// mixed-DPI layouts read the way the compositor lays them out.
func applyScaling(infos []outputInfo) {
	for i := range infos {
		if infos[i].scale > 1 {
			infos[i].width /= infos[i].scale
			infos[i].height /= infos[i].scale
		}
	}
}

// outputThumb captures an output preview: downscaled first, then the
// output transform applied (request_output_frame's order).
func outputThumb(name string, size int) (*image.RGBA, error) {
	c, err := capture.Connect()
	if err != nil {
		return nil, fmt.Errorf("cannot connect to wayland: %w", err)
	}
	defer func() { _ = c.Close() }()
	o, ok := c.OutputByName(name)
	if !ok {
		return nil, fmt.Errorf("output %s not found", name)
	}
	f, err := c.CaptureOutput(o, capture.Options{})
	if err != nil {
		return nil, err
	}
	img, err := f.Image()
	if err != nil {
		return nil, err
	}
	return sharepreview.Transform(sharepreview.ResizeToFit(img, size), o.Transform), nil
}
