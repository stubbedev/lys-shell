package regionoverlay

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/unxed/xkb-go"

	"github.com/stubbedev/lys-shell/shell/treetest"
)

var mons = []Monitor{
	{Connector: "DP-1", X: 0, Y: 0, Width: 1920, Height: 1080},
	{Connector: "DP-2", X: 1920, Y: 0, Width: 1280, Height: 1024},
}

func TestFinalize(t *testing.T) {
	t.Run("output-relative on the monitor holding the top-left", func(t *testing.T) {
		sel, ok := finalize(image.Rect(1900, 100, 2000, 300), mons)
		want := Selection{Output: "DP-1", X: 1900, Y: 100, Width: 100, Height: 200}
		if !ok || sel != want {
			t.Fatalf("finalize = %+v %v, want %+v", sel, ok, want)
		}
		sel, ok = finalize(image.Rect(1930, 10, 2030, 60), mons)
		if want := (Selection{Output: "DP-2", X: 10, Y: 10, Width: 100, Height: 50}); !ok || sel != want {
			t.Fatalf("finalize = %+v %v, want %+v", sel, ok, want)
		}
	})
	t.Run("a corner off every monitor falls back to the first", func(t *testing.T) {
		sel, ok := finalize(image.Rect(2000, 1050, 2100, 1100), mons)
		if !ok || sel.Output != "DP-1" || sel.X != 2000 || sel.Y != 1050 {
			t.Fatalf("finalize = %+v %v", sel, ok)
		}
	})
	t.Run("an empty rectangle is no selection", func(t *testing.T) {
		if _, ok := finalize(image.Rect(5, 5, 5, 90), mons); ok {
			t.Fatal("a zero-width rectangle selected")
		}
		if _, ok := finalize(image.Rect(0, 0, 50, 50), nil); ok {
			t.Fatal("no monitors selected")
		}
	})
}

// A click picks the topmost window under it, else the monitor it is
// on, else nothing.
func TestPick(t *testing.T) {
	windows := []image.Rectangle{image.Rect(100, 100, 200, 200), image.Rect(0, 0, 500, 500)}
	for p, want := range map[image.Point]image.Rectangle{
		image.Pt(150, 150):  windows[0],
		image.Pt(300, 300):  windows[1],
		image.Pt(1000, 900): mons[0].rect(),
		image.Pt(2500, 100): mons[1].rect(),
	} {
		if got, ok := pick(p, windows, mons); !ok || got != want {
			t.Errorf("pick(%v) = %v %v, want %v", p, got, ok, want)
		}
	}
	if _, ok := pick(image.Pt(2500, 1050), windows, mons); ok {
		t.Error("a point off every monitor picked something")
	}
}

func TestMonitorOf(t *testing.T) {
	m := MonitorOf(&app.Output{Name: "DP-1", LogicalX: -10, LogicalY: 5, LogicalW: 1280, LogicalH: 720, ModeW: 2560, ModeH: 1440, Scale: 2})
	if m != (Monitor{Connector: "DP-1", X: -10, Y: 5, Width: 1280, Height: 720}) {
		t.Fatalf("logical = %+v", m)
	}
	m = MonitorOf(&app.Output{Name: "X", ModeW: 2560, ModeH: 1440, Scale: 2})
	if m.Width != 1280 || m.Height != 720 {
		t.Fatalf("without xdg-output the mode / scale is used: %+v", m)
	}
}

func TestLabelOrigin(t *testing.T) {
	if x, y := labelOrigin(render.Rect{X: 10, Y: 100, W: 50, H: 50}, 26); x != 10 || y != 70 {
		t.Errorf("above = %d,%d", x, y)
	}
	if x, y := labelOrigin(render.Rect{X: 10, Y: 20, W: 50, H: 50}, 26); x != 10 || y != 24 {
		t.Errorf("inside = %d,%d", x, y)
	}
	if got := labelText(640, 480); got != "640 × 480" {
		t.Errorf("label = %q", got)
	}
}

// harness drives one area headlessly: the overlay has no application
// and no surfaces, only the reply channel a request would carry.
// accentCSS gives the area its accent the way region_overlay.css
// does.
const accentCSS = `.region-overlay-window .region-overlay-area { color: #b4befe; }`

// stillOf is img as an identified frame, nil as no frame.
func stillOf(img *image.RGBA) render.StillImage {
	if img == nil {
		return render.StillImage{}
	}
	return render.NewStillImage(img)
}

func harness(frame *image.RGBA, windows ...image.Rectangle) (*Overlay, *area, chan result) {
	o := &Overlay{monitors: mons[:1], shown: windows, sheet: widget.NewStylesheet(accentCSS, 0)}
	o.ed = editor{bounds: layout(o.monitors)}
	ch := make(chan result, 1)
	o.reply = ch
	a := &area{o: o, mon: mons[0], frame: stillOf(frame)}
	o.areas = []*area{a}
	root := o.surfaceRoot(a)
	widget.SizeWithin(root, widget.Size{W: 40, H: 30})
	root.Arrange(render.Rect{W: 40, H: 30})
	return o, a, ch
}

// drag presses at from, drags to to and releases.
func drag(a *area, from, to widget.Point) {
	a.HoverMove(from)
	a.SetPressed(true)
	if to != from {
		a.DragMove(to)
	}
	a.PressEnd()
}

func pending(t *testing.T, ch chan result) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("answered %+v before a confirm", r)
	default:
	}
}

func TestGestures(t *testing.T) {
	t.Run("a drag draws the selection; Enter confirms it", func(t *testing.T) {
		o, a, ch := harness(nil)
		o.key(xkb.KeyReturn, 0)
		pending(t, ch)
		drag(a, widget.Point{X: 5, Y: 6}, widget.Point{X: 25, Y: 16})
		pending(t, ch)
		o.key(xkb.KeyReturn, 0)
		if r := <-ch; !r.ok || r.sel != (Selection{Output: "DP-1", X: 5, Y: 6, Width: 20, Height: 10}) {
			t.Fatalf("answer = %+v", r)
		}
	})
	t.Run("the selection moves, resizes and nudges before the confirm", func(t *testing.T) {
		o, a, ch := harness(nil)
		drag(a, widget.Point{X: 100, Y: 100}, widget.Point{X: 300, Y: 200})
		drag(a, widget.Point{X: 150, Y: 150}, widget.Point{X: 160, Y: 170})
		drag(a, widget.Point{X: 310, Y: 220}, widget.Point{X: 410, Y: 320})
		o.key(xkb.KeyLeft, 0)
		o.key(xkb.KeyDown, app.ModShift)
		o.key(xkb.KeyKPEnter, 0)
		if r := <-ch; !r.ok || r.sel != (Selection{Output: "DP-1", X: 109, Y: 130, Width: 300, Height: 200}) {
			t.Fatalf("answer = %+v", r)
		}
	})
	t.Run("a click picks the window under it; a double-click confirms", func(t *testing.T) {
		_, a, ch := harness(nil, image.Rect(50, 60, 250, 160))
		drag(a, widget.Point{X: 100, Y: 100}, widget.Point{X: 100, Y: 100})
		pending(t, ch)
		a.DoubleClickAt(widget.Point{X: 1000, Y: 1000})
		pending(t, ch)
		a.DoubleClickAt(widget.Point{X: 100, Y: 100})
		if r := <-ch; !r.ok || r.sel != (Selection{Output: "DP-1", X: 50, Y: 60, Width: 200, Height: 100}) {
			t.Fatalf("answer = %+v", r)
		}
	})
	t.Run("a click on no window picks the output", func(t *testing.T) {
		o, a, ch := harness(nil)
		drag(a, widget.Point{X: 5, Y: 6}, widget.Point{X: 5, Y: 6})
		o.key(xkb.KeyReturn, 0)
		if r := <-ch; !r.ok || r.sel != (Selection{Output: "DP-1", Width: 1920, Height: 1080}) {
			t.Fatalf("answer = %+v", r)
		}
	})
	t.Run("Escape cancels, a selection or not", func(t *testing.T) {
		o, a, ch := harness(nil)
		drag(a, widget.Point{X: 5, Y: 6}, widget.Point{X: 25, Y: 16})
		o.key(xkb.KeyEscape, 0)
		if r := <-ch; r.ok {
			t.Fatalf("Escape selected %+v", r.sel)
		}
	})
	t.Run("a finished request answers once", func(t *testing.T) {
		o, _, ch := harness(nil)
		o.finish(Selection{}, false)
		o.confirm()
		<-ch
		select {
		case r := <-ch:
			t.Fatalf("second answer %+v", r)
		default:
		}
	})
}

func paint(a *area) []byte {
	data := make([]byte, render.Stride(40)*30)
	cv := render.New(data, render.Stride(40), 40, 30)
	a.Paint(cv)
	return data
}

func px(data []byte, x, y int) render.Color {
	return render.ColorFromBytes(data[y*render.Stride(40)+x*4:])
}

func TestPaintLive(t *testing.T) {
	o, a, _ := harness(nil)
	data := paint(a)
	if got := px(data, 20, 20); got != render.Color(dimAlpha<<24) {
		t.Fatalf("idle wash = %#x", uint32(got))
	}
	o.ed.sel = image.Rect(10, 10, 30, 25)
	data = paint(a)
	if got := px(data, 20, 18); got != 0 {
		t.Fatalf("the hole is %#x, want transparent", uint32(got))
	}
	if got := px(data, 2, 2); got != render.Color(dimAlpha<<24) {
		t.Fatalf("outside the hole %#x, want the wash", uint32(got))
	}
	if got := px(data, 10, 18); got != render.RGB(0xb4, 0xbe, 0xfe) {
		t.Fatalf("border %#x, want the area's color", uint32(got))
	}
}

// The accent is the stylesheet's, read at paint time: outside the
// overlay window, or with the rule gone, the area has no color.
func TestAccentIsTheAreaColor(t *testing.T) {
	_, a, _ := harness(nil)
	if got := widget.CascadeColor(a); got != render.RGB(0xb4, 0xbe, 0xfe) {
		t.Errorf("area color %#x, want the .region-overlay-area rule", uint32(got))
	}
	bare := &area{}
	bare.AddClass("region-overlay-area")
	host := widget.NewBox(widget.Column, 0, 0)
	host.AttachStylesheet(widget.NewStylesheet(accentCSS, 0))
	host.Append(bare, true)
	if got := widget.CascadeColor(bare); got == render.RGB(0xb4, 0xbe, 0xfe) {
		t.Error("an area outside the overlay window took the accent")
	}
}

func TestPaintFrozen(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 80, 60))
	for i := 0; i < len(frame.Pix); i += 4 {
		frame.Pix[i], frame.Pix[i+3] = 200, 255
	}
	o, a, _ := harness(frame)
	o.ed.sel = image.Rect(10, 10, 30, 25)
	data := paint(a)
	if got := px(data, 20, 18); got != render.RGB(200, 0, 0) {
		t.Fatalf("inside the selection %#x, want the bright frame", uint32(got))
	}
	dim := px(data, 2, 2)
	if dim.A() != 255 || dim.R() >= 200 || dim.R() == 0 {
		t.Fatalf("outside the selection %#x, want the washed frame", uint32(dim))
	}
	if want := uint8((200*(255-dimAlpha) + 127) / 255); dim.R() != want {
		t.Fatalf("wash red %d, want %d", dim.R(), want)
	}
}

// A selection up for adjustment shows its handles; one being drawn
// shows none.
func TestPaintHandles(t *testing.T) {
	o, a, _ := harness(nil)
	o.ed.sel = image.Rect(10, 10, 30, 25)
	data := paint(a)
	if got := px(data, 30, 25); got != render.RGB(0xb4, 0xbe, 0xfe) {
		t.Errorf("bottom-right handle %#x, want the accent", uint32(got))
	}
	if got := px(data, 20, 25); got != render.RGB(0xb4, 0xbe, 0xfe) {
		t.Errorf("bottom-middle handle %#x, want the accent", uint32(got))
	}
	o.ed.gesture = gestureCreate
	if got := px(paint(a), 32, 27); got != render.Color(dimAlpha<<24) {
		t.Errorf("a selection being drawn painted a handle: %#x", uint32(got))
	}
}

// Over a frozen frame the magnifier follows the pointer while an edge
// is being placed, and not once the selection is only being adjusted.
func TestMagnifier(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 40, 30))
	o, a, _ := harness(frame)
	if a.magnifying() {
		t.Error("magnifying before the pointer arrived")
	}
	a.HoverMove(widget.Point{X: 5, Y: 5})
	if !a.magnifying() {
		t.Error("no magnifier while placing the first corner")
	}
	o.ed.sel = image.Rect(1, 1, 20, 20)
	if a.magnifying() {
		t.Error("a magnifier while only adjusting")
	}
	o.ed.press(image.Pt(20, 20))
	if !a.magnifying() {
		t.Error("no magnifier while resizing")
	}
	a.SetHovered(false)
	if a.magnifying() {
		t.Error("a magnifier after the pointer left")
	}
	_, live, _ := harness(nil)
	live.HoverMove(widget.Point{X: 5, Y: 5})
	if live.magnifying() {
		t.Error("a magnifier over the live screen")
	}
}

// The size label and the hint are pills on lys-ui's theme: the text at
// the body size, padded by its padding, the backing rounded by its
// radius so the corner stays clear while the edge is backed.
func TestPillTakesTheTheme(t *testing.T) {
	th := *widget.DarkTheme()
	th.Padding, th.Radius = 10, 8
	widget.SetTheme(&th)
	rem := widget.RootFontSize()
	widget.SetRootFontSize(20)
	t.Cleanup(func() { widget.SetTheme(nil); widget.SetRootFontSize(rem) })
	o, a, _ := harness(nil)
	o.font = treetest.Font(t)
	p := a.pill("8 × 8")
	ref := o.font.Shape("8 × 8", 20)
	if p.pad != th.Padding || p.radius != th.Radius || p.h != int(math.Ceil(ref.Ascent()))+2*th.Padding || p.w != int(math.Ceil(ref.Advance()))+2*th.Padding {
		t.Fatalf("pill %dx%d pad %d radius %d, want the theme's metrics", p.w, p.h, p.pad, p.radius)
	}
	const w, h = 200, 80
	data := make([]byte, render.Stride(w)*h)
	p.paint(render.New(data, render.Stride(w), w, h), 0, 0, render.RGB(0xff, 0xff, 0xff))
	at := func(x, y int) render.Color { return render.ColorFromBytes(data[y*render.Stride(w)+x*4:]) }
	if got := at(0, 0); got != 0 {
		t.Errorf("corner %#x, want the rounding to leave it clear", uint32(got))
	}
	if got := at(p.w/2, 0); got.A() == 0 {
		t.Error("the top edge is not backed")
	}
}

// A frozen surface paints straight from the captured frame: no
// resampled or washed copy of it is made, at the frame's own size or
// scaled. The wash darkens outside the selection and leaves the frame
// exact inside it.
func TestPaintFrozenFromTheFrame(t *testing.T) {
	const w, h = 400, 300
	mon := Monitor{Connector: "DP-1", Width: w, Height: h}
	surface := func(frame *image.RGBA) (*area, *render.Canvas, []byte) {
		o := &Overlay{monitors: []Monitor{mon}, sheet: widget.NewStylesheet(accentCSS, 0)}
		o.ed = editor{bounds: layout(o.monitors), sel: image.Rect(100, 50, 300, 250)}
		a := &area{o: o, mon: mon, frame: render.NewStillImage(frame)}
		root := o.surfaceRoot(a)
		widget.SizeWithin(root, widget.Size{W: w, H: h})
		root.Arrange(render.Rect{W: w, H: h})
		data := make([]byte, render.Stride(w)*h)
		return a, render.New(data, render.Stride(w), w, h), data
	}
	for _, scale := range []int{1, 2} {
		frame := image.NewRGBA(image.Rect(0, 0, w*scale, h*scale))
		for i := range frame.Pix {
			frame.Pix[i] = uint8(i * 7)
			if i%4 == 3 {
				frame.Pix[i] = 255
			}
		}
		a, cv, data := surface(frame)
		if got := treetest.HeapBytes(func() { a.Paint(cv) }); got >= uint64(len(frame.Pix))/4 {
			t.Errorf("scale %d: painting allocated %d bytes, a copy of the %d-byte frame", scale, got, len(frame.Pix))
		}
		at := func(x, y int) render.Color { return render.ColorFromBytes(data[y*render.Stride(w)+x*4:]) }
		src := func(x, y int) color.RGBA { return frame.RGBAAt(x*scale, y*scale) }
		if c, want := at(200, 150), src(200, 150); c != render.RGB(want.R, want.G, want.B) {
			t.Errorf("scale %d: inside the selection %#x, want the frame's %v", scale, uint32(c), want)
		}
		washed := func(v uint8) uint8 { return uint8((uint32(v)*(255-dimAlpha) + 127) / 255) }
		for _, p := range []image.Point{{10, 10}, {350, 280}, {50, 150}, {350, 150}, {200, 20}} {
			s := src(p.X, p.Y)
			if c, want := at(p.X, p.Y), render.RGB(washed(s.R), washed(s.G), washed(s.B)); c != want {
				t.Errorf("scale %d: outside the selection at %v %#x, want the washed %#x", scale, p, uint32(c), uint32(want))
			}
		}
	}
}

// around covers outer less the hole exactly once per pixel.
func TestAround(t *testing.T) {
	outer := render.Rect{X: 5, Y: 7, W: 40, H: 30}
	for _, hole := range []render.Rect{
		{X: 15, Y: 12, W: 10, H: 8},
		{X: 5, Y: 7, W: 40, H: 30},
		{X: 0, Y: 0, W: 20, H: 20},
		{X: 30, Y: 20, W: 50, H: 50},
		{},
		{X: 100, Y: 100, W: 5, H: 5},
	} {
		cover := map[image.Point]int{}
		for _, r := range around(outer, hole) {
			if r.Empty() {
				t.Errorf("hole %+v: an empty band %+v", hole, r)
			}
			for y := r.Y; y < r.Y+r.H; y++ {
				for x := r.X; x < r.X+r.W; x++ {
					cover[image.Pt(x, y)]++
				}
			}
		}
		in := func(r render.Rect, x, y int) bool { return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H }
		for y := range 60 {
			for x := range 60 {
				want := 0
				if in(outer, x, y) && !in(hole, x, y) {
					want = 1
				}
				if got := cover[image.Pt(x, y)]; got != want {
					t.Fatalf("hole %+v: (%d,%d) covered %d times, want %d", hole, x, y, got, want)
				}
			}
		}
	}
}
