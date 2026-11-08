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
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-lib/render"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/memory-go"

	"github.com/stubbedev/lys-shell/internal/desktopnotify"
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

// ErrBusy answers a capture or a color pick refused because another capture
// is in progress: nothing opened, nothing was captured, and what was running
// goes on untouched. It is neither a capture nor the user's cancel.
var ErrBusy = regionoverlay.ErrBusy

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
	// Gate is the capture gate Region and Picker share: every capture of
	// this host is admitted by it, so none runs while an overlay is open.
	Gate *regionoverlay.Gate
	// Region and Picker are the freeze-frame overlays.
	Region interface {
		Request(ctx context.Context, frames regionoverlay.Frames) (regionoverlay.Selection, bool, error)
	}
	Picker interface {
		Request(ctx context.Context, frames regionoverlay.Frames, opts colorpicker.Options) (colorpicker.Pick, bool, error)
	}
	// Focus resolves the focused output and the active window.
	Focus Focus
	// Copy puts an image on the clipboard and returns what takes that
	// copy back, nil when nothing can; nil disables the copy.
	Copy func(image.Image) (retract func(), err error)
	// CopyText puts text on the clipboard: a picked color, or text Read read.
	CopyText func(string) error
	// Notify announces a saved screenshot, and a failed copy with it;
	// nil sends the desktop notification.
	Notify func(Notice)
	// Present shows the floating thumbnail of a saved capture; nil
	// shows none.
	Present func(Saved)
	// Edit opens the markup editor on a saved capture, and OpenFolder
	// the folder it was saved in: the notification's buttons.
	Edit       func(Saved)
	OpenFolder func(dir string)
	// Toast and OpenURL deliver what Read read: a line in a toast, a
	// decoded link in the browser.
	Toast   func(text, icon string)
	OpenURL func(string)
	// Working shows that tesseract is reading, with a way for the user
	// to cancel it; finished takes that down.
	Working func(cancel func()) (finished func())
	// Forget drops the GPU textures of the frozen frames with these
	// identities, once the overlay that drew them has closed.
	Forget func(ids []render.ImageID)

	// mem holds the frozen frames' pixels off the Go heap.
	mem memory.Shared

	// now and frozen stand in for the clock and the all-outputs
	// capture (with the pointer or not) in tests.
	now    func() time.Time
	frozen func(pointer bool) ([]frozenOutput, error)

	// interactive is every overlay request running, so Dismiss can take
	// down the one at a handle; a refused request is one for as long as
	// it takes to be refused, and touches no other.
	mu          sync.Mutex
	interactive []*overlayRequest
}

// overlayRequest is one overlay request's handle and what ends it.
type overlayRequest struct {
	handle string
	cancel context.CancelFunc
}

// Capture takes a screenshot for the user and returns the saved PNG
// path, or "" when the user cancelled a region selection. It is saved
// where [modules.screenshot] says, then copied and announced as it
// says. Every mode answers ErrBusy while a capture overlay is open.
func (h *Host) Capture(c portaldialogs.Caller, mode, target string) (string, error) {
	img, ok, err := h.grab(c, mode, target, false)
	if err != nil || !ok {
		return "", err
	}
	return h.save(img)
}

// CapturePrivate takes a screenshot for an app: saved to a file of its
// own in lys-shell's cache, with none of the user's save, clipboard or
// notification settings applied, the pointer composited in when
// pointer is set. "" is a cancelled selection; every mode answers ErrBusy
// while a capture overlay is open.
func (h *Host) CapturePrivate(c portaldialogs.Caller, mode, target string, pointer bool) (string, error) {
	img, ok, err := h.grab(c, mode, target, pointer)
	if err != nil || !ok {
		return "", err
	}
	return h.savePrivate(img)
}

// Dismiss cancels the overlay open for the request at handle; another
// request's stays.
func (h *Host) Dismiss(handle string) {
	if handle == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.interactive {
		if r.handle == handle {
			r.cancel()
		}
	}
}

// dismissable is the context of one overlay request for handle, ended by
// Dismiss; done ends the request's entry.
func (h *Host) dismissable(handle string) (ctx context.Context, done func()) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &overlayRequest{handle: handle, cancel: cancel}
	h.mu.Lock()
	h.interactive = append(h.interactive, r)
	h.mu.Unlock()
	return ctx, func() {
		cancel()
		h.mu.Lock()
		h.interactive = slices.DeleteFunc(h.interactive, func(o *overlayRequest) bool { return o == r })
		h.mu.Unlock()
	}
}

// grab captures the image for a mode, with the pointer when pointer is
// set; ok is false for a cancelled region. A region is admitted by its
// overlay's session, every other mode by the gate here.
func (h *Host) grab(c portaldialogs.Caller, mode, target string, pointer bool) (*image.RGBA, bool, error) {
	if mode == ModeRegion {
		return h.captureRegion(c, pointer)
	}
	end, err := h.Gate.Instant()
	if err != nil {
		return nil, false, err
	}
	defer end()
	switch mode {
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

// save writes the PNG under the configured name, then copies it,
// presents its thumbnail and announces it as the section says.
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
	shot := Saved{Path: path, Image: img}
	var copyErr error
	if cfg.CopyToClipboard && h.Copy != nil {
		if shot.Retract, copyErr = h.Copy(img); copyErr != nil {
			log.Printf("screenshot: clipboard copy: %v", copyErr)
		}
	}
	thumbnail := cfg.ThumbnailMs > 0 && h.Present != nil
	if thumbnail {
		h.Present(shot)
	}
	if cfg.Notify {
		n := savedNotice(path, copyErr)
		if !thumbnail {
			n.Actions = []Action{ActionEdit, ActionOpenFolder, ActionDelete}
			n.Respond = func(a Action) { h.act(a, shot) }
		}
		h.announce(n)
	}
	return path, nil
}

// Saved is a capture on disk: its file, the pixels it was saved from, and
// Retract, which takes its clipboard copy back (nil when there is none).
type Saved struct {
	Path    string
	Image   *image.RGBA
	Retract func()
}

// Discard deletes a saved capture's file and takes its clipboard copy back.
func (s Saved) Discard() error {
	if s.Retract != nil {
		s.Retract()
	}
	if err := os.Remove(s.Path); err != nil {
		return fmt.Errorf("cannot delete the screenshot %s: %w", s.Path, err)
	}
	return nil
}

// Action is a button on the saved-capture notification.
type Action string

// The notification's buttons.
const (
	ActionEdit       Action = "edit"
	ActionOpenFolder Action = "open-folder"
	ActionDelete     Action = "delete"
	ActionCopy       Action = "copy"
	ActionOpen       Action = "open"
)

var actionLabels = map[Action]string{
	ActionEdit:       "screenshot-action-edit",
	ActionOpenFolder: "screenshot-action-open-folder",
	ActionDelete:     "screenshot-action-delete",
	ActionCopy:       "screenshot-action-copy",
	ActionOpen:       "screenshot-action-open",
}

func (h *Host) act(a Action, shot Saved) {
	switch a {
	case ActionEdit:
		if h.Edit != nil {
			h.Edit(shot)
		}
	case ActionOpenFolder:
		if h.OpenFolder != nil {
			h.OpenFolder(filepath.Dir(shot.Path))
		}
	case ActionDelete:
		if err := shot.Discard(); err != nil {
			log.Printf("screenshot: %v", err)
		}
	}
}

// Notice is the desktop notification announcing a saved screenshot. With
// Actions it carries those buttons, and Respond hears the one pressed.
type Notice struct {
	Summary string
	Body    string
	Icon    string
	Actions []Action
	Respond func(Action)
}

func savedNotice(path string, copyErr error) Notice {
	n := Notice{Summary: "Screenshot saved", Body: path, Icon: path}
	if copyErr != nil {
		n.Body += "\nNot copied to the clipboard: " + copyErr.Error()
	}
	return n
}

const actionWait = 30 * time.Second

func (h *Host) announce(n Notice) {
	if h.Notify != nil {
		h.Notify(n)
		return
	}
	source := i18n.T("notification-source-screenshot")
	if len(n.Actions) == 0 {
		desktopnotify.Notify(source, n.Summary, n.Body, n.Icon)
		return
	}
	go func() {
		a, err := ask(source, n)
		if err != nil {
			log.Printf("screenshot: notification: %v", err)
			return
		}
		if a != "" {
			n.Respond(a)
		}
	}()
}

func ask(source string, n Notice) (Action, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return "", fmt.Errorf("session bus: %w", err)
	}
	defer func() { _ = conn.Close() }()
	buttons := make([]desktopnotify.Action, len(n.Actions))
	for i, a := range n.Actions {
		buttons[i] = desktopnotify.Action{Key: string(a), Label: i18n.T(actionLabels[a])}
	}
	ctx, cancel := context.WithTimeout(context.Background(), actionWait+5*time.Second)
	defer cancel()
	key, ok, err := desktopnotify.NewSender(conn).Ask(ctx, source, n.Summary, n.Body, n.Icon, buttons, actionWait)
	if err != nil || !ok {
		return "", err
	}
	return Action(key), nil
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
	if len(frozen) == 0 {
		return
	}
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

// captureRegion is the freeze-frame region flow: once the overlay's
// session admits the request, capture every output before the overlay maps
// (so transient popups survive), show the overlay over those frames, and
// crop the selection out of the frame of its output. Not ok means the user
// cancelled.
func (h *Host) captureRegion(c portaldialogs.Caller, pointer bool) (*image.RGBA, bool, error) {
	ctx, done := h.dismissable(c.Handle)
	defer done()
	var (
		frozen  []frozenOutput
		frames  map[string]render.StillImage
		logical map[string]regionoverlay.Monitor
	)
	defer func() { h.release(frozen) }()
	sel, ok, err := h.Region.Request(ctx, func() (map[string]render.StillImage, error) {
		var err error
		if frozen, err = h.captureAll(pointer); err != nil {
			return nil, err
		}
		frames, logical = framesOf(frozen), h.logical()
		return frames, nil
	})
	if err != nil || !ok {
		return nil, false, err
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
// the user cancelled, ErrBusy answers while a capture overlay is open, and
// any other error is a failure.
func (h *Host) PickColor(caller portaldialogs.Caller) (color [3]float64, picked bool, err error) {
	p, ok, err := h.pick(caller, colorpicker.Options{})
	if err != nil || !ok {
		return color, false, err
	}
	color[0], color[1], color[2] = p.Color.Float()
	return color, true, nil
}

// PickColorText lets the user pick one pixel as PickColor does and
// returns it written in the format the readout showed at the click,
// which opts.Format starts it in; with copyText it is put on the
// clipboard too. picked is false when the user cancelled.
func (h *Host) PickColorText(caller portaldialogs.Caller, opts colorpicker.Options, copyText bool) (text string, picked bool, err error) {
	p, ok, err := h.pick(caller, opts)
	if err != nil || !ok {
		return "", false, err
	}
	text = p.Text()
	if copyText {
		if err := h.CopyText(text); err != nil {
			return "", false, fmt.Errorf("cannot copy the picked color %s: %w", text, err)
		}
	}
	return text, true, nil
}

// pick freezes every output and runs the picker over them.
func (h *Host) pick(caller portaldialogs.Caller, opts colorpicker.Options) (colorpicker.Pick, bool, error) {
	ctx, done := h.dismissable(caller.Handle)
	defer done()
	var frozen []frozenOutput
	defer func() { h.release(frozen) }()
	return h.Picker.Request(ctx, func() (map[string]render.StillImage, error) {
		var err error
		if frozen, err = h.captureAll(false); err != nil {
			return nil, err
		}
		if len(frozen) == 0 {
			return nil, errors.New("no outputs available")
		}
		return framesOf(frozen), nil
	}, opts)
}

// maxNameTries bounds the numbered names saveNew tries.
const maxNameTries = 100

// saveNew saves under path, or "name-2.png", "name-3.png", ... when
// that exists: two captures in the same second (the default name's
// resolution) never overwrite each other. The PNG is written and synced
// to a temporary file beside it first and linked in under the free name,
// so no name ever holds a partial image.
func saveNew(img image.Image, path string) (string, error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".screenshot-*.part")
	if err != nil {
		return "", fmt.Errorf("cannot create a screenshot in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("cannot save %s: %w", path, err)
	}
	if err := writePNG(img, tmp); err != nil {
		return "", err
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	for i := 1; i <= maxNameTries; i++ {
		name := path
		if i > 1 {
			name = fmt.Sprintf("%s-%d%s", base, i, ext)
		}
		err := os.Link(tmp.Name(), name)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("cannot save %s: %w", name, err)
		}
		return name, nil
	}
	return "", fmt.Errorf("cannot save %s: %d numbered names taken", path, maxNameTries)
}

// WritePNG replaces path with img as a PNG, atomically: a reader sees
// the old file or the new one, never a partial write.
func WritePNG(img image.Image, path string) error {
	err := atomicfile.Write(path, 0o644, func(w io.Writer) error { return encodePNG(w, img) })
	if err != nil {
		return fmt.Errorf("cannot save %s: %w", path, err)
	}
	return nil
}

// savePrivate saves an app's screenshot to a new private file under
// $XDG_CACHE_HOME/lys-shell/portal-screenshots.
func (h *Host) savePrivate(img image.Image) (string, error) {
	cache, ok := xdg.OS.CacheDir(identity.Name)
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
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func writePNG(img image.Image, f *os.File) error {
	err := encodePNG(f, img)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("cannot save %s: %w", f.Name(), err)
	}
	return nil
}

func encodePNG(w io.Writer, img image.Image) error {
	bw := bufio.NewWriter(w)
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(bw, img); err != nil {
		return err
	}
	return bw.Flush()
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
