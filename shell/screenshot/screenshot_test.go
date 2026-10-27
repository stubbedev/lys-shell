package screenshot

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/render"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/offheap"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/colorpicker"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
)

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

var (
	red   = color.RGBA{255, 0, 0, 255}
	green = color.RGBA{0, 255, 0, 255}
)

func geom(x, y, w, h int) logicalGeometry { return logicalGeometry{x: x, y: y, width: w, height: h} }

func TestLayoutBounds(t *testing.T) {
	cases := []struct {
		name       string
		geoms      []logicalGeometry
		x, y, w, h int
		wantOK     bool
	}{
		{"none", nil, 0, 0, 0, 0, false},
		{"single", []logicalGeometry{geom(0, 0, 1920, 1080)}, 0, 0, 1920, 1080, true},
		{"side by side", []logicalGeometry{geom(0, 0, 1920, 1080), geom(1920, 0, 2560, 1440)}, 0, 0, 4480, 1440, true},
		{"negative origin", []logicalGeometry{geom(0, 0, 1920, 1080), geom(-1280, -200, 1280, 1024)}, -1280, -200, 3200, 1280, true},
		{"stacked with a gap", []logicalGeometry{geom(0, 0, 1000, 500), geom(0, 600, 800, 400)}, 0, 0, 1000, 1000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x, y, w, h, ok := layoutBounds(tc.geoms)
			if ok != tc.wantOK || x != tc.x || y != tc.y || w != tc.w || h != tc.h {
				t.Fatalf("layoutBounds = %d %d %d %d %v", x, y, w, h, ok)
			}
		})
	}
}

func TestCompositeOutputs(t *testing.T) {
	t.Run("no frames is zero sized", func(t *testing.T) {
		if b := compositeOutputs(nil).Bounds(); !b.Empty() {
			t.Fatalf("bounds %v", b)
		}
	})
	t.Run("HiDPI frames scale to their logical size", func(t *testing.T) {
		img := compositeOutputs([]placedFrame{{logical: geom(0, 0, 100, 100), image: solid(200, 200, red)}})
		if img.Bounds().Dx() != 100 || img.RGBAAt(50, 50) != red {
			t.Fatalf("bounds %v pixel %v", img.Bounds(), img.RGBAAt(50, 50))
		}
	})
	t.Run("side by side outputs land at their offsets", func(t *testing.T) {
		img := compositeOutputs([]placedFrame{
			{logical: geom(0, 0, 192, 108), image: solid(192, 108, red)},
			{logical: geom(192, 0, 256, 144), image: solid(256, 144, green)},
		})
		if img.Bounds().Dx() != 448 || img.Bounds().Dy() != 144 {
			t.Fatalf("bounds %v", img.Bounds())
		}
		if img.RGBAAt(191, 107) != red || img.RGBAAt(192, 0) != green || img.RGBAAt(447, 143) != green {
			t.Fatal("outputs misplaced")
		}
		if img.RGBAAt(0, 120) != (color.RGBA{}) {
			t.Fatal("the gap under the shorter output is not empty")
		}
	})
	t.Run("negative origins map to the canvas top left", func(t *testing.T) {
		img := compositeOutputs([]placedFrame{
			{logical: geom(0, 0, 192, 108), image: solid(192, 108, red)},
			{logical: geom(-128, -20, 128, 102), image: solid(128, 102, green)},
		})
		if img.RGBAAt(0, 0) != green || img.RGBAAt(128, 20) != red {
			t.Fatal("negative origin misplaced")
		}
	})
	t.Run("a zero logical size paints nothing", func(t *testing.T) {
		if b := compositeOutputs([]placedFrame{{logical: geom(0, 0, 0, 0), image: solid(10, 10, red)}}).Bounds(); !b.Empty() {
			t.Fatalf("bounds %v", b)
		}
	})
}

func TestCropFrozen(t *testing.T) {
	// A 2x physical frame: left half red, right half green.
	img := solid(200, 100, red)
	for y := range 100 {
		for x := 100; x < 200; x++ {
			img.SetRGBA(x, y, green)
		}
	}
	t.Run("scales the logical selection to physical pixels", func(t *testing.T) {
		out := cropFrozen(img, 100, 50, regionoverlay.Selection{X: 40, Y: 10, Width: 20, Height: 10})
		if out.Bounds().Dx() != 40 || out.Bounds().Dy() != 20 {
			t.Fatalf("crop %v, want 40x20", out.Bounds())
		}
		if out.RGBAAt(0, 0) != red || out.RGBAAt(39, 19) != green {
			t.Fatal("crop took the wrong pixels")
		}
	})
	t.Run("clamps a selection running off the frame", func(t *testing.T) {
		out := cropFrozen(img, 100, 50, regionoverlay.Selection{X: 90, Y: 40, Width: 50, Height: 50})
		if out.Bounds().Dx() != 20 || out.Bounds().Dy() != 20 {
			t.Fatalf("crop %v, want the 20x20 left inside", out.Bounds())
		}
		out = cropFrozen(img, 100, 50, regionoverlay.Selection{X: 500, Y: 500, Width: 5, Height: 5})
		if !out.Bounds().Empty() {
			t.Fatalf("a selection past the frame cropped %v", out.Bounds())
		}
	})
}

func TestMatchesTarget(t *testing.T) {
	both := windowTarget{appID: "foot", hasAppID: true, title: "shell", hasTitle: true}
	if !matchesTarget("foot", "shell", both) {
		t.Error("exact app id and title did not match")
	}
	if matchesTarget("foot", "other", both) || matchesTarget("kitty", "shell", both) {
		t.Error("a differing key matched")
	}
	appOnly := windowTarget{appID: "foot", hasAppID: true}
	if !matchesTarget("foot", "anything", appOnly) {
		t.Error("an app-id-only target ignored nothing")
	}
	if matchesTarget("foot", "shell", windowTarget{}) {
		t.Error("a target with no identity matched a window")
	}
}

func TestParseHyprlandActiveWindow(t *testing.T) {
	tgt, ok := parseHyprlandActiveWindow(`{"address":"0x55d0a1","class":"foot","title":"~"}`)
	if !ok || !tgt.hasHandle || tgt.hyprlandHandle != 0x55d0a1 || tgt.appID != "foot" || tgt.title != "~" {
		t.Fatalf("parsed %+v %v", tgt, ok)
	}
	if _, ok := parseHyprlandActiveWindow(`{}`); ok {
		t.Error("an empty object parsed as an active window")
	}
	if _, ok := parseHyprlandActiveWindow(`not json`); ok {
		t.Error("garbage parsed as an active window")
	}
	tgt, ok = parseHyprlandActiveWindow(`{"address":"zz","class":"a","title":"b"}`)
	if !ok || tgt.hasHandle {
		t.Errorf("a bad address kept a handle: %+v", tgt)
	}
}

// fakeRegion answers the region overlay; block, when set, holds the
// request until Cancel.
type fakeRegion struct {
	sel     regionoverlay.Selection
	ok      bool
	frames  map[string]render.StillImage
	block   chan struct{}
	cancels int
}

func (f *fakeRegion) Request(frames map[string]render.StillImage) (regionoverlay.Selection, bool) {
	f.frames = frames
	if f.block != nil {
		<-f.block
		return regionoverlay.Selection{}, false
	}
	return f.sel, f.ok
}

func (f *fakeRegion) Cancel() {
	f.cancels++
	if f.block != nil {
		close(f.block)
	}
}

type fakePicker struct {
	c   colorpicker.RGB
	ok  bool
	err error
}

func (f fakePicker) Request(map[string]render.StillImage) (colorpicker.RGB, bool, error) {
	return f.c, f.ok, f.err
}
func (fakePicker) Cancel() {}

// fixed is a Config that never reloads.
func fixed(cfg config.ScreenshotConfig) func() config.ScreenshotConfig {
	return func() config.ScreenshotConfig { return cfg }
}

func testHost(t *testing.T, region *fakeRegion) (*Host, *[]Notice, *int) {
	t.Helper()
	cfg := config.DefaultsScreenshot()
	cfg.OutputDirectory = filepath.Join(t.TempDir(), "shots")
	var notified []Notice
	copies := 0
	h := &Host{
		Config: fixed(cfg),
		Monitors: func() []regionoverlay.Monitor {
			return []regionoverlay.Monitor{{Connector: "DP-1", Width: 100, Height: 50}, {Connector: "DP-2", X: 100, Width: 100, Height: 50}}
		},
		Region: region,
		Picker: fakePicker{c: colorpicker.RGB{R: 255, G: 51, B: 0}, ok: true},
		Copy:   func(image.Image) error { copies++; return nil },
		Notify: func(n Notice) { notified = append(notified, n) },
		Forget: func([]render.ImageID) {},
		now:    func() time.Time { return time.Date(2026, 9, 30, 12, 34, 56, 0, time.Local) },
	}
	h.frozen = func(bool) ([]frozenOutput, error) {
		return []frozenOutput{offHeap(t, h, "DP-1", solid(200, 100, red)), offHeap(t, h, "DP-2", solid(100, 50, green))}, nil
	}
	return h, &notified, &copies
}

// offHeap is a frozen frame of img's pixels that h's allocator holds,
// as the capture makes them.
func offHeap(t *testing.T, h *Host, connector string, img *image.RGBA) frozenOutput {
	t.Helper()
	b := img.Bounds()
	dst, pix, err := offheap.NewRGBA(&h.mem, b.Dx(), b.Dy())
	if err != nil {
		t.Fatal(err)
	}
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return frozenOutput{connector: connector, image: render.NewStillImage(dst), pix: pix}
}

func TestHostRegionCapture(t *testing.T) {
	region := &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", X: 10, Y: 10, Width: 20, Height: 10}, ok: true}
	h, notified, copies := testHost(t, region)
	path, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(h.Config().OutputDirectory, "Screenshot_2026-09-30_12-34-56.png")
	if path != want {
		t.Fatalf("path %q, want %q", path, want)
	}
	if len(region.frames) != 2 || region.frames["DP-1"].RGBA == nil {
		t.Fatal("the overlay did not get the frozen frames")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 40 || b.Dy() != 20 {
		t.Fatalf("saved %v, want the 2x-scaled 40x20 crop", b)
	}
	if *copies != 1 || len(*notified) != 1 || (*notified)[0] != (Notice{Summary: "Screenshot saved", Body: path, Icon: path}) {
		t.Fatalf("copies %d notified %v", *copies, *notified)
	}
}

func TestHostReportsAFailedCopyInTheNotification(t *testing.T) {
	h, notified, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", Width: 5, Height: 5}, ok: true})
	h.Copy = func(image.Image) error { return errors.New("no data-control and no input serial") }
	path, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatalf("a failed copy failed the capture: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a failed copy lost the saved file: %v", err)
	}
	want := Notice{
		Summary: "Screenshot saved",
		Body:    path + "\nNot copied to the clipboard: no data-control and no input serial",
		Icon:    path,
	}
	if len(*notified) != 1 || (*notified)[0] != want {
		t.Fatalf("notified %+v, want %+v", *notified, want)
	}
}

// Each capture saves under the section in effect then: a reload between
// two captures moves the second, not the first.
func TestHostReadsTheReloadedConfig(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", Width: 5, Height: 5}, ok: true})
	cfg := h.Config()
	h.Config = func() config.ScreenshotConfig { return cfg }
	first, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.OutputDirectory = filepath.Join(t.TempDir(), "reloaded")
	second, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) == cfg.OutputDirectory || filepath.Dir(second) != cfg.OutputDirectory {
		t.Errorf("first %q, second %q; want only the second in %q", first, second, cfg.OutputDirectory)
	}
}

func TestHostRegionCancel(t *testing.T) {
	h, notified, copies := testHost(t, &fakeRegion{ok: false})
	path, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
	if err != nil || path != "" {
		t.Fatalf("cancel = %q, %v; want an empty path and no error", path, err)
	}
	if _, err := os.Stat(h.Config().OutputDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Error("a cancelled capture created the output directory")
	}
	if *copies != 0 || len(*notified) != 0 {
		t.Error("a cancelled capture copied or notified")
	}
}

func TestHostRegionOnAVanishedOutput(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "HDMI-9", Width: 5, Height: 5}, ok: true})
	if _, err := h.Capture(portalreq.Caller{}, ModeRegion, ""); err == nil || !strings.Contains(err.Error(), "no longer present") {
		t.Fatalf("err = %v", err)
	}
}

func TestHostOptionsOff(t *testing.T) {
	h, notified, copies := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-2", Width: 5, Height: 5}, ok: true})
	cfg := h.Config()
	cfg.CopyToClipboard, cfg.Notify = false, false
	h.Config = fixed(cfg)
	if _, err := h.Capture(portalreq.Caller{}, ModeRegion, ""); err != nil {
		t.Fatal(err)
	}
	if *copies != 0 || len(*notified) != 0 {
		t.Fatalf("disabled options ran: copies %d notified %v", *copies, *notified)
	}
}

func TestHostScreenComposites(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{})
	path, err := h.Capture(portalreq.Caller{}, ModeScreen, "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 50 {
		t.Fatalf("screen %v, want the 200x50 layout", b)
	}
	h.Monitors = func() []regionoverlay.Monitor { return nil }
	if _, err := h.Capture(portalreq.Caller{}, ModeScreen, ""); err == nil {
		t.Fatal("a screen capture without monitor geometry succeeded")
	}
}

func TestHostUnknownMode(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{})
	if _, err := h.Capture(portalreq.Caller{}, "everything", ""); err == nil || err.Error() != "unknown screenshot mode: everything" {
		t.Fatalf("err = %v", err)
	}
}

func TestHostSaveFailure(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-2", Width: 5, Height: 5}, ok: true})
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := h.Config()
	cfg.OutputDirectory = filepath.Join(file, "sub")
	h.Config = fixed(cfg)
	if _, err := h.Capture(portalreq.Caller{}, ModeRegion, ""); err == nil || !strings.HasPrefix(err.Error(), "cannot create") {
		t.Fatalf("err = %v", err)
	}
}

// A pick replies its color, a cancel no color and no error, and a
// failure - no outputs, or a picker with nothing to show - an error.
func TestHostPickColor(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{})
	c, picked, err := h.PickColor(portalreq.Caller{})
	if err != nil || !picked || c != [3]float64{1, 0.2, 0} {
		t.Fatalf("pick = %v %v %v", c, picked, err)
	}
	h.Picker = fakePicker{ok: false}
	if _, picked, err := h.PickColor(portalreq.Caller{}); err != nil || picked {
		t.Fatalf("cancel = %v, %v; want no pick and no error", picked, err)
	}
	h.Picker = fakePicker{err: errors.New("color picker: no captured frame matches an output")}
	if _, picked, err := h.PickColor(portalreq.Caller{}); err == nil || picked {
		t.Fatalf("a picker failure = %v, %v; want its error, not a cancel", picked, err)
	}
	h.frozen = func(bool) ([]frozenOutput, error) { return nil, nil }
	if _, _, err := h.PickColor(portalreq.Caller{}); err == nil || err.Error() != "no outputs available" {
		t.Fatalf("no outputs err = %v", err)
	}
}

// An app's capture goes to a private file of its own in the cache, and
// leaves the user's directory, clipboard and notifications alone.
func TestHostCapturePrivate(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	h, notified, copies := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", Width: 5, Height: 5}, ok: true})
	first, err := h.CapturePrivate(portalreq.Caller{Handle: "/r/1"}, ModeRegion, "", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.CapturePrivate(portalreq.Caller{Handle: "/r/2"}, ModeRegion, "", false)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cache, "lys-shell", "portal-screenshots")
	if filepath.Dir(first) != dir || filepath.Dir(second) != dir || first == second {
		t.Errorf("private captures at %q and %q, want two files in %s", first, second, dir)
	}
	if info, err := os.Stat(first); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("a private capture is %v, %v; want 0600", info, err)
	}
	if *copies != 0 || len(*notified) != 0 {
		t.Errorf("an app's capture copied %d times and notified %v", *copies, *notified)
	}
	if _, err := os.Stat(h.Config().OutputDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Error("an app's capture went to the user's screenshot directory")
	}
	h.Region = &fakeRegion{}
	if path, err := h.CapturePrivate(portalreq.Caller{}, ModeRegion, "", false); err != nil || path != "" {
		t.Errorf("a cancelled private capture = %q, %v", path, err)
	}
}

// The pointer is composited in only into an app's capture that asks
// for it: the frames are taken with it then, never for the user's own
// capture or a color pick.
func TestHostCapturesThePointerOnlyWhenAsked(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	h, _, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", Width: 5, Height: 5}, ok: true})
	frozen := h.frozen
	var asked []bool
	h.frozen = func(pointer bool) ([]frozenOutput, error) {
		asked = append(asked, pointer)
		return frozen(pointer)
	}
	for _, capture := range []func() error{
		func() error { _, err := h.CapturePrivate(portalreq.Caller{}, ModeScreen, "", true); return err },
		func() error { _, err := h.CapturePrivate(portalreq.Caller{}, ModeRegion, "", true); return err },
		func() error { _, err := h.CapturePrivate(portalreq.Caller{}, ModeScreen, "", false); return err },
		func() error { _, err := h.Capture(portalreq.Caller{}, ModeScreen, ""); return err },
		func() error { _, _, err := h.PickColor(portalreq.Caller{}); return err },
	} {
		if err := capture(); err != nil {
			t.Fatal(err)
		}
	}
	if want := []bool{true, true, false, false, false}; !slices.Equal(asked, want) {
		t.Errorf("frames taken with the pointer %v, want %v", asked, want)
	}
}

// Two captures in the same second keep both files.
func TestHostKeepsCapturesOfTheSameSecond(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", Width: 5, Height: 5}, ok: true})
	var paths []string
	for range 3 {
		path, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, filepath.Base(path))
	}
	want := []string{"Screenshot_2026-09-30_12-34-56.png", "Screenshot_2026-09-30_12-34-56-2.png", "Screenshot_2026-09-30_12-34-56-3.png"}
	if !slices.Equal(paths, want) {
		t.Errorf("saved %q, want %q", paths, want)
	}
}

// A selection past its output's edge keeps the other output's part.
func TestHostRegionAcrossOutputs(t *testing.T) {
	// DP-1 (red, 2x) spans x 0-100, DP-2 (green, 1x) x 100-200.
	h, _, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", X: 90, Y: 0, Width: 20, Height: 10}, ok: true})
	path, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 20 || b.Dy() != 10 {
		t.Fatalf("saved %v, want the 20x10 logical selection", b)
	}
	if left, right := color.RGBAModel.Convert(img.At(2, 5)).(color.RGBA), color.RGBAModel.Convert(img.At(17, 5)).(color.RGBA); left != red || right != green {
		t.Errorf("left half %v, right half %v; want DP-1's red then DP-2's green", left, right)
	}
}

func TestResolveDir(t *testing.T) {
	t.Setenv("XDG_PICTURES_DIR", "/pics")
	t.Setenv("HOME", "/home/u")
	if got := resolveDir("/custom"); got != "/custom" {
		t.Errorf("configured = %q", got)
	}
	if got := resolveDir(""); got != "/pics" {
		t.Errorf("xdg = %q", got)
	}
	t.Setenv("XDG_PICTURES_DIR", "")
	if got := resolveDir(""); got != "/home/u/Pictures" {
		t.Errorf("home = %q", got)
	}
	t.Setenv("HOME", "")
	if got := resolveDir(""); got != "." {
		t.Errorf("fallback = %q", got)
	}
}

// fakeCapturer is the daemon's host in tests.
type fakeCapturer struct{ err error }

func (f fakeCapturer) Capture(_ portalreq.Caller, mode, target string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "/p/" + mode + target, nil
}

func (f fakeCapturer) CapturePrivate(_ portalreq.Caller, mode, target string, pointer bool) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if pointer {
		mode += "+pointer"
	}
	return "/private/" + mode + target, nil
}

func (f fakeCapturer) PickColor(portalreq.Caller) ([3]float64, bool, error) {
	return [3]float64{0.5, 0.25, 1}, f.err == nil, f.err
}

func (fakeCapturer) Dismiss(string) {}

func TestDaemonMethods(t *testing.T) {
	d := &Daemon{host: fakeCapturer{}}
	if p, err := d.Capture(portalreq.Caller{}, "output", "DP-1"); err != nil || p != "/p/outputDP-1" {
		t.Fatalf("Capture = %q %v", p, err)
	}
	if picked, r, g, b, err := d.PickColor(portalreq.Caller{}); err != nil || !picked || r != 0.5 || g != 0.25 || b != 1 {
		t.Fatalf("PickColor = %v %v %v %v %v", picked, r, g, b, err)
	}
	if p, err := d.CapturePrivate(portalreq.Caller{}, "window", "id-1", false); err != nil || p != "/private/windowid-1" {
		t.Fatalf("CapturePrivate = %q %v", p, err)
	}
	d = &Daemon{host: fakeCapturer{err: errors.New("no outputs available")}}
	_, err := d.Capture(portalreq.Caller{}, "output", "")
	if err == nil || err.Name != "org.freedesktop.DBus.Error.Failed" || err.Body[0] != "no outputs available" {
		t.Fatalf("Capture error = %v", err)
	}
	if _, _, _, _, err := d.PickColor(portalreq.Caller{}); err == nil || err.Name != "org.freedesktop.DBus.Error.Failed" {
		t.Fatalf("PickColor error = %v", err)
	}
}

// Dismissing the request whose region selection is open cancels the
// overlay; another request's dismissal leaves it.
func TestDismissCancelsItsOverlayOnly(t *testing.T) {
	region := &fakeRegion{block: make(chan struct{})}
	h, _, _ := testHost(t, region)
	done := make(chan string, 1)
	go func() {
		path, _ := h.Capture(portalreq.Caller{Handle: "/r/mine"}, ModeRegion, "")
		done <- path
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.Lock()
		open := h.interactive.handle == "/r/mine"
		h.mu.Unlock()
		if open {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the region request never opened")
		}
		time.Sleep(time.Millisecond)
	}
	h.Dismiss("/r/other")
	if region.cancels != 0 {
		t.Error("another request's dismissal cancelled the overlay")
	}
	h.Dismiss("/r/mine")
	select {
	case path := <-done:
		if path != "" {
			t.Errorf("a dismissed selection saved %q", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dismiss left the selection open")
	}
}

// A picked window matches by its ext identifier alone; without one the
// focused window's app id and title match.
func TestMatchesToplevel(t *testing.T) {
	tl := capture.Toplevel{AppID: "foot", Title: "~", Identifier: "id-7"}
	if !matchesToplevel(tl, windowTarget{identifier: "id-7"}) {
		t.Error("the picked window's identifier did not match")
	}
	if matchesToplevel(tl, windowTarget{identifier: "id-8", appID: "foot", hasAppID: true}) {
		t.Error("another identifier matched by app id")
	}
	if !matchesToplevel(tl, windowTarget{appID: "foot", hasAppID: true}) {
		t.Error("the focused window's app id did not match")
	}
}

// The overlay gets the captured frames themselves, and the saved region
// is the frame's pixels under the selection, byte for byte.
func TestHostRegionSavesTheFramePixels(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 200, 100))
	for i := range frame.Pix {
		frame.Pix[i] = uint8(i*13 + i/797)
		if i%4 == 3 {
			frame.Pix[i] = 255
		}
	}
	region := &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", X: 13, Y: 7, Width: 31, Height: 17}, ok: true}
	h, _, _ := testHost(t, region)
	var captured *image.RGBA
	h.frozen = func(bool) ([]frozenOutput, error) {
		dp1 := offHeap(t, h, "DP-1", frame)
		captured = dp1.image.RGBA
		return []frozenOutput{dp1, offHeap(t, h, "DP-2", solid(100, 50, green))}, nil
	}
	path, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
	if err != nil {
		t.Fatal(err)
	}
	if region.frames["DP-1"].RGBA != captured {
		t.Error("the overlay got a copy of the captured frame, not the frame")
	}
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 62 || b.Dy() != 34 {
		t.Fatalf("saved %v, want the 2x-scaled 62x34 crop", b)
	}
	for y := range 34 {
		for x := range 62 {
			r, g, b, _ := img.At(x, y).RGBA()
			want := frame.RGBAAt(26+x, 14+y)
			if uint8(r>>8) != want.R || uint8(g>>8) != want.G || uint8(b>>8) != want.B {
				t.Fatalf("saved (%d,%d) = %d,%d,%d, want the frame's %v", x, y, r>>8, g>>8, b>>8, want)
			}
		}
	}
}

// Every flow that freezes the outputs releases the frames once it is
// done with them, whether the user confirmed, cancelled or picked: each
// frame's GPU texture is forgotten once, by its identity, and its
// off-heap pixels are freed.
func TestHostReleasesTheFrozenFrames(t *testing.T) {
	sel := regionoverlay.Selection{Output: "DP-1", X: 10, Y: 10, Width: 20, Height: 10}
	for name, run := range map[string]func(h *Host) error{
		"region confirmed": func(h *Host) error { _, err := h.Capture(portalreq.Caller{}, ModeRegion, ""); return err },
		"region across outputs": func(h *Host) error {
			h.Region = &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", X: 90, Y: 10, Width: 20, Height: 10}, ok: true}
			_, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
			return err
		},
		"region cancelled": func(h *Host) error {
			h.Region = &fakeRegion{}
			_, err := h.Capture(portalreq.Caller{}, ModeRegion, "")
			return err
		},
		"screen":     func(h *Host) error { _, err := h.Capture(portalreq.Caller{}, ModeScreen, ""); return err },
		"color pick": func(h *Host) error { _, _, err := h.PickColor(portalreq.Caller{}); return err },
		"color pick failed": func(h *Host) error {
			h.Picker = fakePicker{err: errors.New("no output")}
			if _, _, err := h.PickColor(portalreq.Caller{}); err == nil {
				return errors.New("the failed pick succeeded")
			}
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, _, _ := testHost(t, &fakeRegion{sel: sel, ok: true})
			var captured, forgotten []render.ImageID
			frozen := h.frozen
			h.frozen = func(pointer bool) ([]frozenOutput, error) {
				out, err := frozen(pointer)
				for _, f := range out {
					captured = append(captured, f.image.ID)
				}
				return out, err
			}
			h.Forget = func(ids []render.ImageID) { forgotten = append(forgotten, ids...) }
			if err := run(h); err != nil {
				t.Fatal(err)
			}
			if live := h.mem.Stats().Live; live != 0 {
				t.Errorf("%d frames still allocated after the flow", live)
			}
			if len(captured) != 2 || !slices.Equal(forgotten, captured) {
				t.Errorf("forgot %v, want each captured frame %v once", forgotten, captured)
			}
			if slices.Contains(forgotten, 0) {
				t.Errorf("forgot a frame without an identity: %v", forgotten)
			}
		})
	}
}

// The frames' pixels are freed only once every List recorded while the
// overlay was up has ended: a frame still being played keeps them.
func TestHostFreesTheFramesAfterTheFence(t *testing.T) {
	h, _, _ := testHost(t, &fakeRegion{sel: regionoverlay.Selection{Output: "DP-1", X: 10, Y: 10, Width: 20, Height: 10}, ok: true})
	drawing := render.NewRecorder(1, 1, 1, 1).Recording()
	if _, err := h.Capture(portalreq.Caller{}, ModeRegion, ""); err != nil {
		t.Fatal(err)
	}
	if live := h.mem.Stats().Live; live != 2 {
		t.Fatalf("%d frames allocated while a List from before the release is open, want both kept", live)
	}
	drawing.End()
	if live := h.mem.Stats().Live; live != 0 {
		t.Fatalf("%d frames still allocated after the last List ended", live)
	}
}
