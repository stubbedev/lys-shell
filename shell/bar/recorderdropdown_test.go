package bar

import (
	"context"
	"sync"
	"testing"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/pulse/native"
	"github.com/stubbedev/lys-shell/service/pulse/pulsetest"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/recorder/recordertest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func TestPreviewGeometryAndPercent(t *testing.T) {
	if pw, ph, cw, ch := previewGeometry(312, 20); pw != 312 || ph != 175 || cw != 62 || ch != 34 {
		t.Errorf("geometry = %d %d %d %d", pw, ph, cw, ch)
	}
	if pw, _, cw, ch := previewGeometry(100, 1); pw != 100 || cw != 24 || ch != 14 {
		t.Errorf("floors = %d %d %d", pw, cw, ch)
	}
	for _, tc := range []struct {
		px, margin, travel int32
		want               uint8
	}{
		{8, 8, 100, 0}, {58, 8, 100, 50}, {108, 8, 100, 100}, {500, 8, 100, 100}, {0, 8, 100, 0}, {50, 8, 0, 0},
	} {
		if got := pctFromPx(tc.px, tc.margin, tc.travel); got != tc.want {
			t.Errorf("pctFromPx(%d, %d, %d) = %d, want %d", tc.px, tc.margin, tc.travel, got, tc.want)
		}
	}
}

// The frame follows the pointer from where it was grabbed, clamped to
// the inset travel, and the release reports the percentages.
func TestWebcamPreviewDrag(t *testing.T) {
	p := newWebcamPreview(20, 0, 0)
	p.Arrange(render.Rect{X: 100, Y: 50, W: 312, H: 175})
	margin, tw, th := p.travel()
	if p.camX != margin || p.camY != margin {
		t.Fatalf("0,0 placed at %d,%d, want the margin %d", p.camX, p.camY, margin)
	}
	var moved [][2]uint8
	p.onMove = func(x, y uint8) { moved = append(moved, [2]uint8{x, y}) }
	// Grab 5px into the frame, drag right by half the travel.
	p.HoverMove(widget.Point{X: 100 + int(margin) + 5, Y: 50 + int(margin) + 5})
	p.SetPressed(true)
	if p.CursorName() != "grabbing" {
		t.Error("no grabbing cursor while dragging")
	}
	p.DragMove(widget.Point{X: 100 + int(margin) + 5 + int(tw/2), Y: 50 + int(margin) + 5})
	if p.camX != margin+tw/2 || p.camY != margin {
		t.Errorf("after drag = %d,%d", p.camX, p.camY)
	}
	// Far past the corner clamps.
	p.DragMove(widget.Point{X: 5000, Y: 5000})
	if p.camX != margin+tw || p.camY != margin+th {
		t.Errorf("clamped = %d,%d", p.camX, p.camY)
	}
	p.SetPressed(false)
	if len(moved) != 1 || moved[0] != [2]uint8{100, 100} {
		t.Errorf("reported = %v", moved)
	}
	// Motion without a press moves nothing.
	p.DragMove(widget.Point{X: 0, Y: 0})
	if p.camX != margin+tw || len(moved) != 1 {
		t.Error("an unpressed move dragged the frame")
	}
	if p.CursorName() != "grab" {
		t.Error("cursor after release")
	}
	p.place(50, 0)
	if x, y := p.percent(); x != 50 || y != 0 {
		t.Errorf("place/percent round trip = %d,%d", x, y)
	}
}

// configLog records ModuleContext.SetConfig writes.
type configLog struct {
	mu     sync.Mutex
	writes map[string]any
}

func (c *configLog) set(path string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.writes == nil {
		c.writes = map[string]any{}
	}
	c.writes[path] = value
}

func (c *configLog) get(path string) any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes[path]
}

func recorderTestCtx(t *testing.T, cams []recorder.DeviceChoice) (ModuleContext, *configLog, *recordertest.Engine) {
	t.Helper()
	prev := recorderCameras
	recorderCameras = func() []recorder.DeviceChoice { return cams }
	t.Cleanup(func() { recorderCameras = prev })
	cfg := config.Defaults()
	cfg.Recorder.OutputDirectory, cfg.Recorder.StartDelayMs = t.TempDir(), 0
	ctx := newTestContext(t, cfg)
	log := &configLog{}
	ctx.SetConfig = log.set
	engine := &recordertest.Engine{}
	ctx.Recorder = recorder.NewState(engine, func() config.RecorderConfig { return cfg.Recorder }, recorder.Hooks{})
	return ctx, log, engine
}

func TestRecorderDropdownStateAndControls(t *testing.T) {
	ctx, _, engine := recorderTestCtx(t, []recorder.DeviceChoice{{ID: "", Label: "Automatic"}})
	v := recorderDropdown(ctx).(*recorderView)
	defer v.dropdownClosed()
	if v.status.Visible() || v.pause.Enabled() || !v.record.HasClass(css.SuggestedAction) || recordText(v) != i18n.T("dropdown-recorder-record") {
		t.Error("idle: want the record button, no status, pause disabled")
	}
	if v.webcam.Visible() {
		t.Error("no camera: the webcam card shows")
	}

	v.record.OnClick()
	waitHeadless(t, "recording", func() bool { return v.status.Visible() })
	if !v.record.HasClass(css.DestructiveAction) || v.record.HasClass(css.SuggestedAction) || recordText(v) != i18n.T("dropdown-recorder-stop") || !v.pause.Enabled() {
		t.Error("recording: want stop, danger, pause enabled")
	}
	v.pause.OnClick()
	waitHeadless(t, "paused", func() bool { return v.statusDot.HasClass("paused") })
	if v.pauseIcon.Name() != "ld-play-symbolic" || v.pause.TooltipText() != i18n.T("dropdown-recorder-resume") {
		t.Error("paused: want the resume button")
	}
	if got := engine.Recordings()[0].Paused(); len(got) != 1 || !got[0] {
		t.Errorf("pause calls = %v", got)
	}
	v.record.OnClick()
	waitHeadless(t, "stopped", func() bool { return !v.status.Visible() })
}

func TestRecorderDropdownWritesConfig(t *testing.T) {
	ctx, log, _ := recorderTestCtx(t, []recorder.DeviceChoice{{ID: "", Label: "Automatic"}, {ID: "/dev/video0", Label: "Cam"}})
	srv := pulsetest.New(t)
	srv.PutSource(pulsetest.Source(10, "mic", "Microphone", native.InvalidIndex, ""))
	svc, err := pulse.Connect(context.Background(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()
	ctx.Pulse = svc
	v := recorderDropdown(ctx).(*recorderView)
	defer v.dropdownClosed()
	if !v.webcam.Visible() {
		t.Fatal("a camera is present: the webcam card is hidden")
	}

	// Switches and pickers write their keys.
	micRow := treetest.WithClass(v, "recorder-row")[0]
	micSwitch := treetest.First[*widget.Switch](t, micRow)
	micSwitch.SetOn(!micSwitch.On())
	if log.get(recorderPathMic) != micSwitch.On() {
		t.Errorf("microphone write = %v", log.get(recorderPathMic))
	}
	if len(v.mics) != 2 {
		t.Fatalf("mics = %v", v.mics)
	}
	v.micPicker.SetSelected(1)
	if log.get(recorderPathMicDevice) != "mic" {
		t.Errorf("mic device write = %v", log.get(recorderPathMicDevice))
	}
	v.preview.onMove(30, 70)
	if log.get(recorderPathWebcamX) != int64(30) || log.get(recorderPathWebcamY) != int64(70) {
		t.Errorf("position writes = %v %v", log.get(recorderPathWebcamX), log.get(recorderPathWebcamY))
	}

	// A hotplugged microphone rebuilds the picker.
	old := v.micPicker
	srv.PutSource(pulsetest.Source(11, "usb", "USB Mic", native.InvalidIndex, ""))
	waitHeadless(t, "the new source", func() bool { return len(v.mics) == 3 })
	if v.micPicker == old {
		t.Error("the picker was not rebuilt")
	}
	// An unchanged list keeps the picker.
	same := v.micPicker
	v.syncMics(append([]recorder.DeviceChoice(nil), v.mics...))
	if v.micPicker != same {
		t.Error("an unchanged list rebuilt the picker")
	}
}

// The recorder's settings are lys-ui Items in item cards, each section
// a group under its icon-led label, and the record button is lys-ui's
// icon-and-label content.
func TestRecorderSettingsAreItemCards(t *testing.T) {
	ctx, _, _ := recorderTestCtx(t, []recorder.DeviceChoice{{ID: "", Label: "Automatic"}})
	v := recorderDropdown(ctx).(*recorderView)
	defer v.dropdownClosed()
	cards := treetest.WithClass(v, "recorder-card")
	if len(cards) != 2 {
		t.Fatalf("recorder cards = %d, want audio and webcam", len(cards))
	}
	for _, c := range cards {
		if !widget.HasClass(c, css.Card) || len(treetest.All[*widget.ItemGroup](c)) != 1 {
			t.Error("a recorder card is not lys-ui's card around an item group")
		}
	}
	if n := len(treetest.WithClass(cards[0], "recorder-row")); n != 3 {
		t.Errorf("audio rows = %d, want microphone, its device, and system audio", n)
	}
	if _, ok := any(v.recordFace).(*widget.ButtonContent); !ok || recordText(v) != i18n.T("dropdown-recorder-record") {
		t.Errorf("the record button reads %q", recordText(v))
	}
}

// styledWebcamDropdown is the recorder dropdown with a camera named
// camera, selected, the stylesheet attached, laid out at width w.
func styledWebcamDropdown(t *testing.T, w int, camera string) *recorderView {
	t.Helper()
	prev := recorderCameras
	recorderCameras = func() []recorder.DeviceChoice {
		return []recorder.DeviceChoice{{ID: "", Label: "Automatic"}, {ID: "/dev/video0", Label: camera}}
	}
	t.Cleanup(func() { recorderCameras = prev })
	cfg := config.Defaults()
	cfg.Recorder.WebcamDevice = "/dev/video0"
	ctx := styledContext(t, cfg)
	v := recorderDropdown(ctx).(*recorderView)
	t.Cleanup(v.dropdownClosed)
	ctx.Theme.Attach(v)
	arrangeDropdown(t, v, w, 1000)
	return v
}

// The position picker spans its row's content box at any dropdown
// width: the same inset from its row as the webcam switch row's title
// and switch have from theirs, on every side.
func TestRecorderPositionPickerInsetLikeTheRows(t *testing.T) {
	for _, w := range []int{300, 360, 480} {
		v := styledWebcamDropdown(t, w, "Cam")
		card := treetest.WithClass(v, "recorder-card")[1]
		switchRow := treetest.WithClass(card, "recorder-row")[0]
		title := boundsOf(treetest.WithClass(switchRow, "title")[0])
		sw := boundsOf(treetest.First[*widget.Switch](t, switchRow))
		rb := boundsOf(switchRow)
		pb := boundsOf(treetest.WithClass(card, "recorder-position-picker")[0])
		fb := v.preview.frame.Bounds()
		if got, want := fb.X-pb.X, title.X-rb.X; got != want {
			t.Errorf("width %d: frame left inset %d, rows' %d", w, got, want)
		}
		if got, want := pb.X+pb.W-(fb.X+fb.W), rb.X+rb.W-(sw.X+sw.W); got != want {
			t.Errorf("width %d: frame right inset %d, rows' %d", w, got, want)
		}
		if got, want := fb.Y-pb.Y, title.Y-rb.Y; got != want {
			t.Errorf("width %d: frame top inset %d, rows' %d", w, got, want)
		}
		if got, want := pb.Y+pb.H-(fb.Y+fb.H), rb.Y+rb.H-(title.Y+title.H); got != want {
			t.Errorf("width %d: frame bottom inset %d, rows' %d", w, got, want)
		}
		if fb.H != fb.W*9/16 {
			t.Errorf("width %d: frame %dx%d is not 16:9", w, fb.W, fb.H)
		}
	}
}

// A dropdown narrower than the configured width does not push the
// preview out of its card: the frame stays inside the card's content
// and the webcam frame inside the screen frame, wherever it sits.
func TestRecorderPositionPickerStaysInsideANarrowCard(t *testing.T) {
	v := styledWebcamDropdown(t, 260, "Cam")
	cb := boundsOf(treetest.WithClass(v, "recorder-card")[1])
	fb := v.preview.frame.Bounds()
	if fb.X < cb.X || fb.X+fb.W > cb.X+cb.W || fb.Y+fb.H > cb.Y+cb.H {
		t.Errorf("frame %v overflows the card %v", fb, cb)
	}
	for _, at := range [][2]uint8{{0, 0}, {100, 100}} {
		v.preview.place(at[0], at[1])
		v.preview.Arrange(v.preview.Bounds())
		if cam := v.preview.cam.Bounds(); cam.X < fb.X || cam.Y < fb.Y || cam.X+cam.W > fb.X+fb.W || cam.Y+cam.H > fb.Y+fb.H {
			t.Errorf("at %v the webcam frame %v leaves the screen frame %v", at, cam, fb)
		}
	}
}

// cameraSelect is the webcam card's camera picker and the right edge
// of its rows' content, where the webcam switch ends.
func cameraSelect(t *testing.T, v *recorderView) (*widget.Dropdown, int) {
	t.Helper()
	card := treetest.WithClass(v, "recorder-card")[1]
	sw := boundsOf(treetest.First[*widget.Switch](t, card))
	return treetest.First[*widget.Dropdown](t, card), sw.X + sw.W
}

// A camera named past the row's room narrows its select to the row:
// the select ends where the switch above it ends, narrower than its
// label wants, rather than running out of the card.
func TestRecorderLongCameraSelectNarrowsToItsRow(t *testing.T) {
	v := styledWebcamDropdown(t, 360, "Creative Live! Cam Sync 4K Analog Stereo")
	d, edge := cameraSelect(t, v)
	b := d.Bounds()
	if b.X+b.W != edge {
		t.Errorf("the select ends at %d, the rows' content at %d", b.X+b.W, edge)
	}
	if natural := d.Measure(widget.Constraints{Max: widget.Size{W: 1 << 12, H: 1 << 12}}).W; b.W >= natural {
		t.Errorf("the select is %dpx, its label wants %dpx: it did not narrow", b.W, natural)
	}

	v.syncMics([]recorder.DeviceChoice{{ID: "", Label: "Default"}, {ID: "usb", Label: "Creative Live! Cam Sync 4K Analog Stereo"}})
	arrangeDropdown(t, v, 360, 1000)
	audio := treetest.WithClass(v, "recorder-card")[0]
	sw := boundsOf(treetest.First[*widget.Switch](t, audio))
	if mb := v.micPicker.Bounds(); mb.X+mb.W != sw.X+sw.W {
		t.Errorf("the source select ends at %d, the rows' content at %d", mb.X+mb.W, sw.X+sw.W)
	}
}

// A camera whose name fits keeps the select at its natural width, at
// the rows' content edge: the row gives room, it does not squeeze.
func TestRecorderShortCameraSelectKeepsItsWidth(t *testing.T) {
	v := styledWebcamDropdown(t, 360, "Cam")
	d, edge := cameraSelect(t, v)
	b := d.Bounds()
	if natural := d.Measure(widget.Constraints{Max: widget.Size{W: 1 << 12, H: 1 << 12}}).W; b.W != natural {
		t.Errorf("the select is %dpx, its natural width %dpx", b.W, natural)
	}
	if b.X+b.W != edge {
		t.Errorf("the select ends at %d, the rows' content at %d", b.X+b.W, edge)
	}
}

// recordText is the record button's label.
func recordText(v *recorderView) string {
	return treetest.All[*widget.Label](v.recordFace)[0].Text()
}
