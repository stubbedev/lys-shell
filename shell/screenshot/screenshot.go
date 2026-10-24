// Package screenshot is the screenshot host
// (crates/lys-shell/src/shell/screenshot) behind the
// dev.stubbe.lys.Screenshot1 D-Bus service: it resolves the capture target
// (opening the freeze-frame region overlay for region mode), captures
// a full-resolution image, saves a PNG, optionally copies it to the
// clipboard and fires a desktop notification, and replies with the
// path. It also hosts the color pick the portal's
// Screenshot.PickColor reuses: freeze every output, then the loupe.
package screenshot

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/colorpicker"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

// The capture modes of Capture: a drag-selected region, one output
// (the focused one without a target), the whole multi-monitor space
// composited, or the active window.
const (
	ModeRegion = "region"
	ModeOutput = "output"
	ModeScreen = "screen"
	ModeWindow = "window"
)

// Host performs captures. The capture half runs on the caller's
// goroutine (a D-Bus handler); the overlays marshal onto the lys-ui loop
// themselves.
type Host struct {
	// Config is the [modules.screenshot] section in effect, read at
	// each save so a reload applies to the next capture.
	Config func() config.ScreenshotConfig
	// Monitors lists the outputs' logical geometry, for scaling
	// selections and compositing the whole screen.
	Monitors func() []regionoverlay.Monitor
	// Region and Picker are the freeze-frame overlays.
	Region interface {
		Request(frames map[string]*image.RGBA) (regionoverlay.Selection, bool)
		Cancel()
	}
	Picker interface {
		Request(frames map[string]*image.RGBA) (colorpicker.RGB, bool)
		Cancel()
	}
	// Focus resolves the focused output and the active window.
	Focus Focus
	// Copy puts an image on the clipboard; nil disables the copy.
	Copy func(image.Image) error
	// Notify reports a saved path; nil sends the desktop notification.
	Notify func(path string)

	// now and frozen stand in for the clock and the all-outputs
	// capture in tests.
	now    func() time.Time
	frozen func() ([]frozenOutput, error)

	// interactive is the request handle and the cancel of the overlay
	// open for a portal request, so Dismiss can take it down.
	mu          sync.Mutex
	interactive struct {
		handle string
		cancel func()
	}
}

// Capture takes a screenshot and returns the saved PNG path, or "" when
// the user cancelled a region selection.
func (h *Host) Capture(c portalreq.Caller, mode, target string) (string, error) {
	img, ok, err := h.grab(c, mode, target)
	if err != nil || !ok {
		return "", err
	}
	return h.save(img)
}

// Dismiss cancels the overlay open for the request at handle; another
// request's stays.
func (h *Host) Dismiss(handle string) {
	h.mu.Lock()
	cancel := h.interactive.cancel
	match := handle != "" && h.interactive.handle == handle
	h.mu.Unlock()
	if match && cancel != nil {
		cancel()
	}
}

// overlay runs one overlay request for c, dismissable meanwhile.
func overlay[T any](h *Host, c portalreq.Caller, cancel func(), request func() (T, bool)) (T, bool) {
	h.mu.Lock()
	h.interactive.handle, h.interactive.cancel = c.Handle, cancel
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.interactive.handle == c.Handle {
			h.interactive.handle, h.interactive.cancel = "", nil
		}
		h.mu.Unlock()
	}()
	return request()
}

// grab captures the image for a mode; ok is false for a cancelled
// region.
func (h *Host) grab(c portalreq.Caller, mode, target string) (*image.RGBA, bool, error) {
	switch mode {
	case ModeRegion:
		return h.captureRegion(c)
	case ModeOutput:
		name := target
		if name == "" && h.Focus != nil {
			name, _ = h.Focus.FocusedOutput()
		}
		img, err := captureOutput(name)
		return img, err == nil, err
	case ModeScreen:
		img, err := h.captureFullScreen()
		return img, err == nil, err
	case ModeWindow:
		var t windowTarget
		if h.Focus != nil {
			t = h.Focus.ActiveWindow()
		}
		img, err := captureWindow(t)
		return img, err == nil, err
	}
	return nil, false, fmt.Errorf("unknown screenshot mode: %s", mode)
}

// save writes the PNG, then applies the clipboard and notify options.
func (h *Host) save(img *image.RGBA) (string, error) {
	cfg := h.Config()
	dir := resolveDir(cfg.OutputDirectory)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the user's pictures directory
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	path := filepath.Join(dir, cfg.FilenameFormat.Layout().Format(h.clock()))
	if err := savePNG(img, path); err != nil {
		return "", err
	}
	if cfg.CopyToClipboard && h.Copy != nil {
		if err := h.Copy(img); err != nil {
			log.Printf("screenshot: clipboard copy: %v", err)
		}
	}
	if cfg.Notify {
		if h.Notify != nil {
			h.Notify(path)
		} else {
			// The saved PNG doubles as the notification icon.
			desktopnotify.Notify("lys-shell", "Screenshot saved", path, path)
		}
	}
	return path, nil
}

func (h *Host) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

func (h *Host) captureAll() ([]frozenOutput, error) {
	if h.frozen != nil {
		return h.frozen()
	}
	return captureAllOutputs()
}

// logical maps each connector to its logical geometry.
func (h *Host) logical() map[string]regionoverlay.Monitor {
	out := map[string]regionoverlay.Monitor{}
	if h.Monitors == nil {
		return out
	}
	for _, m := range h.Monitors() {
		out[m.Connector] = m
	}
	return out
}

// captureRegion is the freeze-frame region flow: capture every output
// before the overlay maps (so transient popups survive), show the
// overlay over those frames, and crop the selection out of the frame
// of its output. Not ok means the user cancelled.
func (h *Host) captureRegion(c portalreq.Caller) (*image.RGBA, bool, error) {
	frozen, err := h.captureAll()
	if err != nil {
		return nil, false, err
	}
	logical := h.logical()
	frames := make(map[string]*image.RGBA, len(frozen))
	for _, f := range frozen {
		frames[f.connector] = f.image
	}
	sel, ok := overlay(h, c, h.Region.Cancel, func() (regionoverlay.Selection, bool) { return h.Region.Request(frames) })
	if !ok {
		return nil, false, nil
	}
	frame, ok := frames[sel.Output]
	if !ok {
		return nil, false, errors.New("captured output for selection no longer present")
	}
	mon, ok := logical[sel.Output]
	if !ok {
		return nil, false, errors.New("no logical geometry for the selected output")
	}
	return cropFrozen(frame, mon.Width, mon.Height, sel), true, nil
}

// captureFullScreen composites every output into one image spanning
// the layout's bounding box, grim's whole-screen grab.
func (h *Host) captureFullScreen() (*image.RGBA, error) {
	frozen, err := h.captureAll()
	if err != nil {
		return nil, err
	}
	logical := h.logical()
	var placed []placedFrame
	for _, f := range frozen {
		m, ok := logical[f.connector]
		if !ok {
			continue
		}
		placed = append(placed, placedFrame{
			logical: logicalGeometry{x: m.X, y: m.Y, width: m.Width, height: m.Height},
			image:   f.image,
		})
	}
	if len(placed) == 0 {
		return nil, errors.New("no outputs available")
	}
	return compositeOutputs(placed), nil
}

// PickColor freezes every output and lets the user pick one pixel with
// the loupe, returning sRGB channels in [0, 1]. Cancelling is an error.
func (h *Host) PickColor(caller portalreq.Caller) (r, g, b float64, err error) {
	frozen, err := h.captureAll()
	if err != nil {
		return 0, 0, 0, err
	}
	frames := make(map[string]*image.RGBA, len(frozen))
	for _, f := range frozen {
		frames[f.connector] = f.image
	}
	if len(frames) == 0 {
		return 0, 0, 0, errors.New("no outputs available")
	}
	c, ok := overlay(h, caller, h.Picker.Cancel, func() (colorpicker.RGB, bool) { return h.Picker.Request(frames) })
	if !ok {
		return 0, 0, 0, errors.New("color pick cancelled")
	}
	r, g, b = c.Float()
	return r, g, b, nil
}

// savePNG encodes with the fastest compression: a snappy save beats the
// smallest file. Opaque images encode as RGB, like the Rust RgbImage.
func savePNG(img image.Image, path string) error {
	f, err := os.Create(path) //nolint:gosec // the configured screenshot path
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", path, err)
	}
	w := bufio.NewWriter(f)
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err = enc.Encode(w, img)
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("cannot save %s: %w", path, err)
	}
	return nil
}

// resolveDir is the save directory: the configured one, else
// $XDG_PICTURES_DIR, else $HOME/Pictures, else the working directory.
func resolveDir(configured string) string {
	if configured != "" {
		return configured
	}
	if dir := os.Getenv("XDG_PICTURES_DIR"); dir != "" {
		return dir
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, "Pictures")
	}
	return "."
}
