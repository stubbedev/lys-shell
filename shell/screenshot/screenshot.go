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
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/internal/xdg"
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
		Request(frames map[string]render.StillImage) (regionoverlay.Selection, bool)
		Cancel()
	}
	Picker interface {
		Request(frames map[string]render.StillImage) (colorpicker.RGB, bool, error)
		Cancel()
	}
	// Focus resolves the focused output and the active window.
	Focus Focus
	// Copy puts an image on the clipboard; nil disables the copy.
	Copy func(image.Image) error
	// Notify reports a saved path; nil sends the desktop notification.
	Notify func(path string)
	// Forget drops the GPU textures of the frozen frames with these
	// identities, once the overlay that drew them has closed.
	Forget func(ids []render.ImageID)

	// mem holds the frozen frames' pixels off the Go heap.
	mem memory.Shared

	// now and frozen stand in for the clock and the all-outputs
	// capture (with the pointer or not) in tests.
	now    func() time.Time
	frozen func(pointer bool) ([]frozenOutput, error)

	// interactive is the request handle and the cancel of the overlay
	// open for a portal request, so Dismiss can take it down.
	mu          sync.Mutex
	interactive struct {
		handle string
		cancel func()
	}
}

// Capture takes a screenshot for the user and returns the saved PNG
// path, or "" when the user cancelled a region selection. It is saved
// where [modules.screenshot] says, then copied and announced as it
// says.
func (h *Host) Capture(c portalreq.Caller, mode, target string) (string, error) {
	img, ok, err := h.grab(c, mode, target, false)
	if err != nil || !ok {
		return "", err
	}
	return h.save(img)
}

// CapturePrivate takes a screenshot for an app: saved to a file of its
// own in lys-shell's cache, with none of the user's save, clipboard or
// notification settings applied, the pointer composited in when
// pointer is set. "" is a cancelled selection.
func (h *Host) CapturePrivate(c portalreq.Caller, mode, target string, pointer bool) (string, error) {
	img, ok, err := h.grab(c, mode, target, pointer)
	if err != nil || !ok {
		return "", err
	}
	return h.savePrivate(img)
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

// grab captures the image for a mode, with the pointer when pointer is
// set; ok is false for a cancelled region.
func (h *Host) grab(c portalreq.Caller, mode, target string, pointer bool) (*image.RGBA, bool, error) {
	switch mode {
	case ModeRegion:
		return h.captureRegion(c, pointer)
	case ModeOutput:
		name := target
		if name == "" && h.Focus != nil {
			name, _ = h.Focus.FocusedOutput()
		}
		img, err := captureOutput(name, pointer)
		return img, err == nil, err
	case ModeScreen:
		img, err := h.captureFullScreen(pointer)
		return img, err == nil, err
	case ModeWindow:
		t := windowTarget{identifier: target}
		if target == "" && h.Focus != nil {
			t = h.Focus.ActiveWindow()
		}
		img, err := captureWindow(t, pointer)
		return img, err == nil, err
	}
	return nil, false, fmt.Errorf("unknown screenshot mode: %s", mode)
}

// save writes the PNG under the configured name, then applies the
// clipboard and notify options.
func (h *Host) save(img *image.RGBA) (string, error) {
	cfg := h.Config()
	dir := resolveDir(cfg.OutputDirectory)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the user's pictures directory
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	path, err := saveNew(img, filepath.Join(dir, cfg.FilenameFormat.Layout().Format(h.clock())))
	if err != nil {
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

func (h *Host) captureAll(pointer bool) ([]frozenOutput, error) {
	if h.frozen != nil {
		return h.frozen(pointer)
	}
	return captureAllOutputs(&h.mem, pointer)
}

// release lets go of frozen frames nothing will draw again: the GPU
// drops their textures, and their pixels are freed once every List
// recorded until now has ended, so no frame still being played reads
// freed memory.
func (h *Host) release(frozen []frozenOutput) {
	ids := make([]render.ImageID, len(frozen))
	for i, f := range frozen {
		ids[i] = f.image.ID
	}
	h.Forget(ids)
	render.Retire(func() { freeFrames(&h.mem, frozen) })
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
func (h *Host) captureRegion(c portalreq.Caller, pointer bool) (*image.RGBA, bool, error) {
	frozen, err := h.captureAll(pointer)
	if err != nil {
		return nil, false, err
	}
	defer h.release(frozen)
	logical := h.logical()
	frames := framesOf(frozen)
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
	if sel.X+sel.Width <= mon.Width && sel.Y+sel.Height <= mon.Height {
		return cropFrozen(frame.RGBA, mon.Width, mon.Height, sel), true, nil
	}
	// A selection past its output's edge takes the other outputs'
	// parts too.
	global := image.Rect(mon.X+sel.X, mon.Y+sel.Y, mon.X+sel.X+sel.Width, mon.Y+sel.Y+sel.Height)
	return cropAcross(frames, h.Monitors(), global), true, nil
}

// captureFullScreen composites every output into one image spanning
// the layout's bounding box, grim's whole-screen grab.
func (h *Host) captureFullScreen(pointer bool) (*image.RGBA, error) {
	frozen, err := h.captureAll(pointer)
	if err != nil {
		return nil, err
	}
	defer h.release(frozen)
	logical := h.logical()
	var placed []placedFrame
	for _, f := range frozen {
		m, ok := logical[f.connector]
		if !ok {
			continue
		}
		placed = append(placed, placedFrame{
			logical: logicalGeometry{x: m.X, y: m.Y, width: m.Width, height: m.Height},
			image:   f.image.RGBA,
		})
	}
	if len(placed) == 0 {
		return nil, errors.New("no outputs available")
	}
	return compositeOutputs(placed), nil
}

// PickColor freezes every output and lets the user pick one pixel with
// the loupe, returning sRGB channels in [0, 1]; picked is false when
// the user cancelled, and an error is a failure.
func (h *Host) PickColor(caller portalreq.Caller) (color [3]float64, picked bool, err error) {
	frozen, err := h.captureAll(false)
	if err != nil {
		return color, false, err
	}
	defer h.release(frozen)
	frames := framesOf(frozen)
	if len(frames) == 0 {
		return color, false, errors.New("no outputs available")
	}
	var pickErr error
	c, ok := overlay(h, caller, h.Picker.Cancel, func() (colorpicker.RGB, bool) {
		c, ok, err := h.Picker.Request(frames)
		pickErr = err
		return c, ok
	})
	if pickErr != nil {
		return color, false, pickErr
	}
	if !ok {
		return color, false, nil
	}
	color[0], color[1], color[2] = c.Float()
	return color, true, nil
}

// maxNameTries bounds the numbered names saveNew tries.
const maxNameTries = 100

// saveNew saves under path, or "name-2.png", "name-3.png", ... when
// that exists: two captures in the same second (the default name's
// resolution) never overwrite each other.
func saveNew(img image.Image, path string) (string, error) {
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i <= maxNameTries; i++ {
		name := path
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // the configured screenshot path, readable as any picture
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot create %s: %w", name, err)
		}
		if err := writePNG(img, f); err != nil {
			return "", err
		}
		return name, nil
	}
	return "", fmt.Errorf("cannot save %s: %d numbered names taken", path, maxNameTries)
}

// savePrivate saves an app's screenshot to a new private file under
// $XDG_CACHE_HOME/lys-shell/portal-screenshots.
func (h *Host) savePrivate(img image.Image) (string, error) {
	cache, ok := xdg.CacheDir()
	if !ok {
		return "", errors.New("no cache directory for the screenshot (XDG_CACHE_HOME and HOME unset)")
	}
	dir := filepath.Join(cache, "portal-screenshots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, h.clock().UTC().Format("screenshot-20060102-150405-*.png"))
	if err != nil {
		return "", fmt.Errorf("cannot create a screenshot in %s: %w", dir, err)
	}
	if err := writePNG(img, f); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// writePNG encodes into f with the fastest compression (a snappy save
// beats the smallest file) and closes it; a failed write removes it.
// Opaque images encode as RGB, like the Rust RgbImage.
func writePNG(img image.Image, f *os.File) error {
	path := f.Name()
	w := bufio.NewWriter(f)
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err := enc.Encode(w, img)
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
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
