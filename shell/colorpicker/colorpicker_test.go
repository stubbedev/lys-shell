package colorpicker

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xkb"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/loupe"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestParseHex(t *testing.T) {
	if c, ok := parseHex("#FF8000"); !ok || c != (RGB{255, 128, 0}) {
		t.Errorf("#FF8000 = %v %v", c, ok)
	}
	if c, ok := parseHex("  #000000 "); !ok || c != (RGB{}) {
		t.Errorf("padded = %v %v", c, ok)
	}
	for _, bad := range []string{"FF8000", "#FFF", "#GGGGGG", "", "#FF80001"} {
		if _, ok := parseHex(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	if got := (RGB{255, 128, 0}).Hex(); got != "#FF8000" {
		t.Errorf("Hex = %q", got)
	}
}

func TestPushHistory(t *testing.T) {
	var h []RGB
	for i := range 10 {
		h = pushHistory(h, RGB{R: uint8(i)})
	}
	if len(h) != historyMax || h[0].R != 9 || h[historyMax-1].R != 2 {
		t.Fatalf("history = %v", h)
	}
	h = pushHistory(h, RGB{R: 5})
	if h[0].R != 5 || len(h) != historyMax {
		t.Fatalf("re-pick = %v", h)
	}
	for _, c := range h[1:] {
		if c.R == 5 {
			t.Fatal("the re-picked color kept its old slot")
		}
	}
}

func TestHistoryPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lys-shell", "color-history")
	saveHistory(path, []RGB{{1, 2, 3}, {255, 255, 255}})
	if got := loadHistory(path); len(got) != 2 || got[0] != (RGB{1, 2, 3}) {
		t.Fatalf("round trip = %v", got)
	}
	if err := os.WriteFile(path, []byte("#010203\ngarbage\n#FFFFFF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadHistory(path); len(got) != 2 {
		t.Fatalf("garbage lines survived: %v", got)
	}
	if got := loadHistory(filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Fatalf("missing file = %v", got)
	}
	saveHistory("", []RGB{{1, 1, 1}}) // no path: a no-op, not a crash
}

func TestHistoryPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if got := HistoryPath(); got != "/data/lys-shell/color-history" {
		t.Errorf("xdg = %q", got)
	}
	t.Setenv("HOME", "/home/u")
	for _, ignored := range []string{"", "relative/data"} {
		t.Setenv("XDG_DATA_HOME", ignored)
		if got := HistoryPath(); got != "/home/u/.local/share/lys-shell/color-history" {
			t.Errorf("XDG_DATA_HOME %q: home = %q", ignored, got)
		}
	}
	t.Setenv("HOME", "")
	if got := HistoryPath(); got != "" {
		t.Errorf("no home = %q", got)
	}
}

// gradient is a 2x physical frame over a 60x40 logical output.
func gradient() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 120, 80))
	for y := range 80 {
		for x := range 120 {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 2), uint8(y * 3), 7, 255})
		}
	}
	return img
}

func TestClickPicksAndRemembers(t *testing.T) {
	dir := t.TempDir()
	p := &Picker{historyPath: filepath.Join(dir, "h")}
	ch := make(chan result, 1)
	p.reply = ch
	l := &surface{p: p, frame: render.NewStillImage(gradient()), logicalW: 60, logicalH: 40}
	l.Arrange(render.Rect{W: 60, H: 40})
	l.ClickAt(widget.Point{X: 10, Y: 5})
	r := <-ch
	if !r.ok || r.pick.Color != (RGB{40, 30, 7}) {
		t.Fatalf("pick = %+v", r)
	}
	if len(p.history) != 1 || p.history[0] != r.pick.Color {
		t.Fatalf("history = %v", p.history)
	}
	if got := loadHistory(p.historyPath); len(got) != 1 {
		t.Fatalf("the pick was not persisted: %v", got)
	}
	red, green, blue := r.pick.Color.Float()
	if red != 40.0/255 || green != 30.0/255 || blue != 7.0/255 {
		t.Fatalf("Float = %v %v %v", red, green, blue)
	}
}

func TestCancelRemembersNothing(t *testing.T) {
	p := &Picker{historyPath: filepath.Join(t.TempDir(), "h")}
	ch := make(chan result, 1)
	p.reply = ch
	p.finish(RGB{1, 2, 3}, false)
	if r := <-ch; r.ok {
		t.Fatal("cancel reported a pick")
	}
	if len(p.history) != 0 {
		t.Fatal("cancel entered the history")
	}
	if _, err := os.Stat(p.historyPath); err == nil {
		t.Fatal("cancel wrote the history file")
	}
}

func TestLoupePaints(t *testing.T) {
	const w, h = 400, 400
	data := make([]byte, render.Stride(w)*h)
	cv := render.New(data, render.Stride(w), w, h)
	img := gradient()
	box := render.Rect{W: w, H: h}
	at := func(x, y int) render.Color { return render.ColorFromBytes(data[y*render.Stride(w)+x*4:]) }

	drawLoupe(cv, nil, img, 60, 40, box, 50, 50, config.ColorFormatHex)
	// The grid's top-left cell sits just right-below the cursor offset
	// and shows the pixel radius cells up-left of the center.
	cx, cy := loupe.SampleAt(img, 60, 40, 50, 50)
	want := img.RGBAAt(max(cx-loupe.Radius, 0), max(cy-loupe.Radius, 0))
	if got := at(50+24+1, 50+24+1); got != render.RGB(want.R, want.G, want.B) {
		t.Fatalf("grid cell = %#x, want %v", uint32(got), want)
	}
	// The recent swatches sit along the bottom, the first red.
	rects := stripRects(box, 2)
	drawStrip(cv, []RGB{{255, 0, 0}, {0, 0, 255}}, rects, -1)
	if got := at(rects[0].X+5, rects[0].Y+5); got != render.RGB(255, 0, 0) {
		t.Fatalf("swatch = %#x", uint32(got))
	}
	if rects[0].Y+rects[0].H != h-stripMargin || rects[1].X-rects[0].X != stripSwatch+swatchGap {
		t.Errorf("strip = %+v", rects)
	}

	// Near the bottom-right corner the card flips to the cursor's
	// other side and stays on screen.
	for i := range data {
		data[i] = 0
	}
	drawLoupe(cv, nil, img, 60, 40, box, w-5, h-5, config.ColorFormatHex)
	if got := at(w-5-24-1, h-5-24-1); got.A() == 0 {
		t.Fatal("the flipped card did not paint up-left of the cursor")
	}
	if got := at(w-1, h-1); got.A() != 0 {
		t.Fatal("the card painted past the cursor corner")
	}
}

// Frames that match no monitor leave nothing to pick from: the request
// fails naming both, instead of passing for the user's cancel, and is
// answered.
func TestNoMatchingFrameFails(t *testing.T) {
	p := &Picker{outputs: func() []*app.Output { return []*app.Output{{Name: "DP-1"}} }}
	ch := make(chan result, 1)
	p.show(map[string]render.StillImage{"HDMI-A-1": render.NewStillImage(gradient())}, ch, Options{Format: config.ColorFormatHex})
	r := <-ch
	if r.ok || r.err == nil || !strings.Contains(r.err.Error(), "HDMI-A-1") || !strings.Contains(r.err.Error(), "DP-1") {
		t.Fatalf("unmatched frames answered %+v, want an error naming the frame and the output", r)
	}
	if p.reply != nil {
		t.Error("the failed request stayed open")
	}
}

// A cancel for a request already over leaves the one open now alone.
func TestCancelIsForItsOwnRequest(t *testing.T) {
	p := &Picker{}
	open, over := make(chan result, 1), make(chan result, 1)
	p.reply = open
	p.cancel(over)
	if p.reply != open || len(open) != 0 || len(over) != 0 {
		t.Fatal("a stale cancel answered the open request")
	}
	p.cancel(open)
	if r := <-open; r.ok || r.err != nil {
		t.Fatalf("the cancel answered %+v", r)
	}
}

// The surface paints the frozen frame from its own pixels, at the 2x
// device scale it was captured at, without a resampled copy; the click
// still reads the frame pixel under the pointer.
func TestSurfacePaintsTheFrame(t *testing.T) {
	frame := gradient()
	l := &surface{p: &Picker{}, frame: render.NewStillImage(frame), logicalW: 60, logicalH: 40}
	l.Arrange(render.Rect{W: 60, H: 40})
	data := make([]byte, render.Stride(120)*80)
	cv := render.NewScaled(data, render.Stride(120), 120, 80, 2, 1)
	if got := treetest.HeapBytes(func() { l.Paint(cv) }); got >= uint64(len(frame.Pix))/4 {
		t.Errorf("painting allocated %d bytes, a copy of the %d-byte frame", got, len(frame.Pix))
	}
	for _, p := range []image.Point{{0, 0}, {37, 21}, {119, 79}} {
		want := frame.RGBAAt(p.X, p.Y)
		if got := render.ColorFromBytes(data[p.Y*render.Stride(120)+p.X*4:]); got != render.RGB(want.R, want.G, want.B) {
			t.Errorf("device pixel %v = %#x, want the frame's %v", p, uint32(got), want)
		}
	}
}

func TestTabCyclesTheReadoutAndThePickUsesIt(t *testing.T) {
	p := &Picker{historyPath: filepath.Join(t.TempDir(), "h"), configured: func() config.ColorFormat { return config.ColorFormatRGB }}
	if f := p.startFormat(""); f != config.ColorFormatRGB {
		t.Fatalf("first pick starts in %s, want the configured one", f)
	}
	ch := make(chan result, 1)
	p.reply, p.format = ch, p.startFormat("")
	l := &surface{p: p, frame: render.NewStillImage(gradient()), logicalW: 60, logicalH: 40, hoverSwatch: -1}
	p.views = []*surface{l}
	l.Arrange(render.Rect{W: 60, H: 40})
	p.key(nil, xkb.KeyTab, 0)
	if p.format != config.ColorFormatHSL {
		t.Fatalf("Tab moved the readout to %s", p.format)
	}
	l.ClickAt(widget.Point{X: 10, Y: 5})
	r := <-ch
	if r.pick.Format != config.ColorFormatHSL || r.pick.Text() != r.pick.Color.Format(config.ColorFormatHSL) {
		t.Fatalf("pick = %+v", r.pick)
	}
	if f := p.startFormat(""); f != config.ColorFormatHSL {
		t.Errorf("the next pick starts in %s, want the one used", f)
	}
	if f := p.startFormat(config.ColorFormatCMYK); f != config.ColorFormatCMYK {
		t.Errorf("an asked-for format starts in %s", f)
	}
	cancelled := make(chan result, 1)
	p.reply = cancelled
	p.key(nil, xkb.KeyEscape, 0)
	if r := <-cancelled; r.ok {
		t.Error("Escape picked")
	}
}

func TestFormatsCycleBackToTheFirst(t *testing.T) {
	f := config.ColorFormats[0]
	for range config.ColorFormats {
		f = f.Next()
	}
	if f != config.ColorFormats[0] {
		t.Errorf("cycling every format ends at %s", f)
	}
}

func TestClickingARecentSwatchPicksItAgain(t *testing.T) {
	old, older := RGB{1, 2, 3}, RGB{200, 100, 50}
	p := &Picker{historyPath: filepath.Join(t.TempDir(), "h"), history: []RGB{old, older}, format: config.ColorFormatRGB}
	ch := make(chan result, 1)
	p.reply = ch
	l := &surface{p: p, frame: render.NewStillImage(gradient()), logicalW: 400, logicalH: 300, hoverSwatch: -1}
	l.Arrange(render.Rect{W: 400, H: 300})
	rects := stripRects(l.Bounds(), 2)
	at := widget.Point{X: rects[1].X + 3, Y: rects[1].Y + 3}
	l.HoverMove(at)
	if l.hoverSwatch != 1 || l.CursorName() != "default" {
		t.Fatalf("over the swatch: hover %d, cursor %q", l.hoverSwatch, l.CursorName())
	}
	l.ClickAt(at)
	r := <-ch
	if !r.ok || r.pick.Color != older || r.pick.Text() != "rgb(200, 100, 50)" {
		t.Fatalf("pick = %+v", r)
	}
	if p.history[0] != older || p.history[1] != old {
		t.Errorf("history = %v, want the re-picked swatch first", p.history)
	}
	l.HoverMove(widget.Point{X: 5, Y: 5})
	if l.hoverSwatch != -1 || l.CursorName() != "none" {
		t.Errorf("off the strip: hover %d, cursor %q", l.hoverSwatch, l.CursorName())
	}
	p.noLoupe = true
	if l.CursorName() != "crosshair" {
		t.Errorf("without the loupe the cursor is %q", l.CursorName())
	}
}
