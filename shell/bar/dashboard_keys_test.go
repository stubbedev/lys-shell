package bar

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/idleinhibit/inhibitduration"
	"github.com/stubbedev/lys-desktop/service/mpris"
	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-desktop/service/upower"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
)

type dashboardKeys struct {
	*dashboardRig
	kb      *keytest.Keyboard
	view    *dashboardView
	pop     *app.Popover
	window  *app.Window
	keys    int
	current widget.Widget
}

func openDashboardKeys(t *testing.T) *dashboardKeys {
	t.Helper()
	rig := newDashboardRig(t)
	d := &dashboardKeys{dashboardRig: rig}
	apptest.On(rig.a, func() error {
		w, err := rig.a.NewWindow(app.WindowConfig{
			Title: "Focused", AppID: "focused", Root: widget.NewBox(widget.Column, 0, 0),
			OnKey: func(*widget.Router, uint32, app.Mods) { d.keys++ },
		})
		d.window = w
		return err
	})
	d.kb = keytest.Dial(t, rig.a)
	apptest.Until(t, rig.a, "the toplevel holds the keyboard", func() bool {
		return rig.a.Session().KeyboardFocus() == d.window.HostSurface()
	})
	d.openHoldingKeys()
	return d
}

func (d *dashboardKeys) openHoldingKeys() {
	d.t.Helper()
	cfg := d.reg.cfg
	reads := d.network.reads.Load()
	apptest.On(d.a, func() error {
		content := d.reg.instance("dashboard", dashboardDropdown)
		panel := content.(*panelBox)
		panel.clamp(900)
		d.view = panel.child.(*dashboardView)
		d.track(content)
		width, _, _ := dropdownDims("dashboard", cfg)
		pc := dropdownPopover(cfg, d.anchor, content, 900, width)
		pc.OnClosed = d.view.dropdownHidden
		pc.Focus = d.view.firstControl()
		var rev *widget.Revealer
		pc.Content, rev = dropdownTree(content, cfg)
		rev.SetRevealed(true)
		rev.Finish()
		pop, err := d.a.OpenPopover(d.layer, pc)
		if err != nil {
			return err
		}
		d.pop = pop
		d.view.dropdownOpened()
		return nil
	})
	d.awaitReads(reads + 1)
	apptest.Until(d.t, d.a, "the dashboard holds the keyboard, laid out", func() bool {
		held := d.a.Session().KeyboardFocus()
		return held != nil && held != d.window.HostSurface() && d.current != nil && rectOf(d.current).W > 0
	})
}

func (d *dashboardKeys) track(content widget.Widget) {
	walkPages(content, func(w widget.Widget) {
		if f, ok := w.(interface{ SetOnFocusChanged(func(bool)) }); ok {
			f.SetOnFocusChanged(func(on bool) {
				if on {
					d.current = w
				}
			})
		}
	})
}

func (d *dashboardKeys) focused() widget.Widget {
	return apptest.On(d.a, func() widget.Widget { return d.current })
}

func (d *dashboardKeys) tile(i int) widget.Widget {
	d.t.Helper()
	tiles := apptest.On(d.a, func() []widget.Widget {
		return d.view.quick.Children()
	})
	if len(tiles) != 6 {
		d.t.Fatalf("%d quick actions, want 6", len(tiles))
	}
	return tiles[i]
}

func (d *dashboardKeys) withClass(class string) widget.Widget {
	d.t.Helper()
	w := apptest.On(d.a, func() widget.Widget { return findByClass(d.view, class) })
	if w == nil {
		d.t.Fatalf("no widget has class %q", class)
	}
	return w
}

func (d *dashboardKeys) expectFocus(what string, want widget.Widget) {
	d.t.Helper()
	if got := d.focused(); got != want {
		d.t.Errorf("%s: focus on %T %v, want %T %v", what, got, rectOf(got), want, rectOf(want))
	}
}

func rectOf(w widget.Widget) render.Rect {
	if b, ok := w.(widget.Boundser); ok {
		return b.Bounds()
	}
	return render.Rect{}
}

func TestDashboardKeysDriveIt(t *testing.T) {
	d := openDashboardKeys(t)
	dnd, idle, saver := d.tile(3), d.tile(4), d.tile(5)
	mute := d.withClass("dashboard-volume-icon")

	d.expectFocus("opening", dnd)

	d.kb.Tap(keytest.Left)
	d.expectFocus("Left at the row's start", dnd)
	d.kb.Tap(keytest.Right)
	d.expectFocus("Right", idle)
	d.kb.Tap(keytest.Right)
	d.expectFocus("Right again", saver)
	d.kb.Tap(keytest.Right)
	d.expectFocus("Right at the row's end", saver)
	d.kb.Tap(keytest.Left)
	d.expectFocus("Left", idle)
	d.kb.Tap(keytest.Left)
	d.expectFocus("Left again", dnd)

	d.kb.Tap(keytest.Up)
	settings := d.withClass("dashboard-settings-btn")
	d.expectFocus("Up over the disabled tiles", settings)
	d.kb.Tap(keytest.Up)
	d.expectFocus("Up at the top", settings)
	d.kb.Tap(keytest.Down)
	if got := d.focused(); got == mute {
		t.Error("Down from the header skipped the quick actions")
	}
	d.kb.Tap(keytest.Tab)
	if !apptest.On(d.a, func() bool { return widget.FocusVisible(d.current) }) {
		t.Error("Tab does not show its focus")
	}
	apptest.On(d.a, func() error { return d.pop.SetFocus(dnd) })

	dndOn := func() bool { return apptest.On(d.a, d.view.ctx.Notifications.DND) }
	if dndOn() {
		t.Fatal("do not disturb starts on")
	}
	d.kb.Tap(keytest.Return)
	if !dndOn() {
		t.Error("Return did not press the focused tile")
	}
	d.expectFocus("Return", dnd)
	d.kb.Tap(keytest.Space)
	if dndOn() {
		t.Error("Space did not press the focused tile")
	}
	if apptest.On(d.a, d.pop.Closed) {
		t.Fatal("pressing a tile closed the dashboard")
	}

	d.kb.Tap(keytest.H)
	d.expectFocus("a key with no meaning here", dnd)
	if dndOn() || apptest.On(d.a, d.pop.Closed) {
		t.Error("a key with no meaning here acted")
	}

	d.kb.Tap(keytest.Down)
	if got := d.focused(); got != mute {
		t.Fatalf("Down from the tile: focus on %T, want the mute button", got)
	}
	d.kb.Tap(keytest.Right)
	knob := d.focused()
	if widget.RoleOf(knob) != widget.RoleSlider {
		t.Fatalf("Right from mute: focus on %T, want the volume slider", knob)
	}
	slider := knob.(interface{ Value() float64 })
	before := apptest.On(d.a, slider.Value)
	d.kb.Tap(keytest.Right)
	d.expectFocus("Right on the slider", knob)
	if got := apptest.On(d.a, slider.Value); got <= before {
		t.Errorf("Right on the slider: value %v, want above %v", got, before)
	}
	d.kb.Tap(keytest.Left)
	d.expectFocus("Left on the slider", knob)
	if got := apptest.On(d.a, slider.Value); got != before {
		t.Errorf("Left on the slider: value %v, want back at %v", got, before)
	}
	d.kb.Tap(keytest.Up)
	if got := d.focused(); got == knob {
		t.Error("Up stayed on the slider")
	}
	if got := apptest.On(d.a, slider.Value); got != before {
		t.Errorf("Up changed the slider to %v", got)
	}

	d.kb.Tap(keytest.Escape)
	apptest.Until(t, d.a, "Escape closes the dashboard", d.pop.Closed)
	if dndOn() {
		t.Error("Escape pressed a tile")
	}
	apptest.Until(t, d.a, "the keys go back to the toplevel", func() bool {
		return d.a.Session().KeyboardFocus() == d.window.HostSurface()
	})
	sent := apptest.On(d.a, func() int { return d.keys })
	d.kb.Tap(keytest.Return)
	if apptest.On(d.a, func() int { return d.keys }) != sent+1 {
		t.Error("a key after closing did not reach the toplevel")
	}
	if dndOn() {
		t.Error("a key after closing still reached the dashboard")
	}

	d.openHoldingKeys()
	d.expectFocus("reopening", dnd)
}

func keysView(t *testing.T, h int) (*dashboardView, *widget.Router) {
	t.Helper()
	ctx := styledContext(t, config.Defaults())
	ctx.Pulse = &fakePulseSource{ticks: make(chan struct{}, 1)}
	v := dashboardDropdown(ctx).(*dashboardView)
	root := widget.NewBox(widget.Column, 0, 0)
	ctx.Theme.Attach(root)
	root.Append(v, true)
	widget.SizeWithin(root, widget.Size{W: 380, H: h})
	root.Arrange(render.Rect{W: 380, H: h})
	widget.CollectDamage(root)
	widget.SizeWithin(root, widget.Size{W: 380, H: h})
	root.Arrange(render.Rect{W: 380, H: h})
	return v, &widget.Router{Root: root}
}

func TestDashboardHomeAndEnd(t *testing.T) {
	v, r := keysView(t, 2000)
	order := focusablesUnder(v.root, nil)
	first, last := order[0], order[len(order)-1]
	if err := r.SetFocus(v.firstControl()); err != nil {
		t.Fatal(err)
	}
	r.KeyAction(widget.KeyEnd, 0)
	if r.Focused() != last {
		t.Errorf("End landed on %T, want the last control", r.Focused())
	}
	r.KeyAction(widget.KeyHome, 0)
	if r.Focused() != first {
		t.Errorf("Home landed on %T, want the first control", r.Focused())
	}
	r.KeyAction(widget.KeyNextPage, 0)
	if r.Focused() != first {
		t.Errorf("Page Down moved the focus to %T", r.Focused())
	}
	if got := v.NavigateFocus(widget.NewBox(widget.Row, 0, 0), widget.KeyNextPage); got != nil {
		t.Errorf("a key with no meaning navigated to %T", got)
	}
	var knob widget.Widget
	for _, w := range order {
		if widget.RoleOf(w) == widget.RoleSlider {
			knob = w
			break
		}
	}
	if knob == nil {
		t.Fatal("no slider takes focus")
	}
	for _, k := range []widget.KeyAction{widget.KeyHome, widget.KeyEnd, widget.KeyLeft, widget.KeyRight} {
		if got := v.NavigateFocus(knob, k); got != nil {
			t.Errorf("key %v left the slider for %T", k, got)
		}
	}
}

func TestDashboardKeysScrollTheirTargetIntoView(t *testing.T) {
	v, r := keysView(t, 300)
	if err := r.SetFocus(v.firstControl()); err != nil {
		t.Fatal(err)
	}
	inView := func() bool {
		view := v.scroll.Bounds()
		b := r.Focused().(widget.Boundser).Bounds()
		return b.Y >= view.Y && b.Y+b.H <= view.Y+view.H
	}
	r.KeyAction(widget.KeyEnd, 0)
	widget.CollectDamage(r.Root)
	r.Root.Arrange(render.Rect{W: 380, H: 300})
	if _, y := v.scroll.Offset(); y == 0 {
		t.Fatal("End did not scroll")
	}
	if !inView() {
		t.Errorf("End left %+v outside the view %+v", r.Focused().(widget.Boundser).Bounds(), v.scroll.Bounds())
	}
	r.KeyAction(widget.KeyHome, 0)
	r.Root.Arrange(render.Rect{W: 380, H: 300})
	if _, y := v.scroll.Offset(); y != 0 {
		t.Errorf("Home left the scroll at %d", y)
	}
}

func TestNearestToward(t *testing.T) {
	at := func(x, y, w, h int) *rectWidget { return &rectWidget{r: render.Rect{X: x, Y: y, W: w, H: h}} }
	from := at(100, 100, 40, 40)
	ahead := at(100, 200, 40, 40)
	diagonal := at(160, 150, 40, 40)
	beside := at(160, 100, 40, 40)
	above := at(100, 40, 40, 40)
	order := []widget.Widget{from, ahead, diagonal, beside, above}
	for _, tc := range []struct {
		name string
		k    widget.KeyAction
		want widget.Widget
	}{
		{"down prefers straight ahead", widget.KeyDown, ahead},
		{"up", widget.KeyUp, above},
		{"right stays on the line", widget.KeyRight, beside},
		{"left has nothing", widget.KeyLeft, nil},
	} {
		if got := nearestToward(from, order, tc.k); got != tc.want {
			t.Errorf("%s: wrong neighbour", tc.name)
		}
	}
	if got := nearestToward(from, []widget.Widget{from, diagonal}, widget.KeyRight); got != nil {
		t.Error("right left the line for a control on another row")
	}
	if got := nearestToward(ahead, []widget.Widget{ahead}, widget.KeyUp); got != nil {
		t.Error("alone, up found a neighbour")
	}
}

type rectWidget struct {
	widget.Base
	r render.Rect
}

func (w *rectWidget) Bounds() render.Rect                   { return w.r }
func (w *rectWidget) Measure(widget.Axis, int) widget.Sizes { return widget.Sizes{} }
func (w *rectWidget) Arrange(render.Rect)                   {}
func (w *rectWidget) Paint(*render.Canvas)                  {}
func (w *rectWidget) HitTest(widget.Point) widget.Widget    { return nil }

func lookContext(t *testing.T) ModuleContext {
	t.Helper()
	ctx := fixtureContext(t)
	ctx.Notifications = notifications.NewService()
	ctx.Notifications.SetDND(true)
	ctx.IdleInhibit = idleinhibit.NewState(inhibitduration.Duration{})
	ctx.Media = newFakeMedia(mpris.Player{BusName: "org.mpris.MediaPlayer2.x", Title: "Song", Artist: "Band", State: mpris.StatePlaying, CanGoNext: true, CanGoPrevious: true})
	ctx.Battery = newFakeBattery(upower.Device{Percentage: 64, IsPresent: true, State: upower.StateDischarging, TimeToEmpty: 3 * time.Hour})
	ctx.Network = &fakeNetworkSource{snap: network.Snapshot{HasWifi: true, WifiEnabled: true, WifiSSID: "lan", Primary: network.ConnectionWifi}, ticks: make(chan struct{}, 2)}
	ctx.PowerProfiles = &fakePPSource{snap: powerprofiles.Snapshot{Available: true, Active: powerprofiles.ProfilePowerSaver}, ticks: make(chan struct{}, 2)}
	ctx.Pulse = &fakePulseSource{dev: pulse.Device{Description: "Speakers", Volume: pct(40)}, ticks: make(chan struct{}, 1)}
	return ctx
}

func repaint(root *widget.Box) []byte {
	widget.CollectDamage(root)
	b := root.Bounds()
	data := make([]byte, render.Stride(b.W)*b.H)
	for i := 3; i < len(data); i += 4 {
		data[i] = previewBackdrop
	}
	root.Paint(render.New(data, render.Stride(b.W), b.W, b.H))
	return data
}

func regionDiffers(a, b []byte, w int, r render.Rect) bool {
	stride := render.Stride(w)
	for y := r.Y; y < r.Y+r.H; y++ {
		from, to := y*stride+r.X*4, y*stride+(r.X+r.W)*4
		if !bytes.Equal(a[from:to], b[from:to]) {
			return true
		}
	}
	return false
}

func TestDashboardFocusShowsOnEveryControl(t *testing.T) {
	restore := widget.SetAnimationsInstant(true)
	t.Cleanup(restore)
	ctx := lookContext(t)
	v := dashboardDropdown(ctx).(*dashboardView)
	data, w, h, root := paintPreview(ctx, v, "dashboard")
	r := &widget.Router{Root: root}
	rest := repaint(root)
	dir := os.Getenv(previewDirEnv)
	if dir != "" {
		writePreview(t, filepath.Join(dir, "dashboard-look.png"), data, w, h)
	}
	first := v.firstControl()
	controls := focusablesUnder(v.root, nil)
	if len(controls) < 10 {
		t.Fatalf("%d controls take focus, want the header's, the tiles, volume, media and session", len(controls))
	}
	for _, c := range controls {
		if err := r.SetFocus(c); err != nil {
			t.Fatalf("focus %T: %v", c, err)
		}
		focused := repaint(root)
		if !regionDiffers(rest, focused, w, boundsOf(c)) {
			t.Errorf("focus on %T at %+v paints nothing", c, boundsOf(c))
		}
		if dir != "" && c == first {
			writePreview(t, filepath.Join(dir, "dashboard-focus.png"), focused, w, h)
		}
		other := render.Rect{X: 0, Y: 0, W: w, H: boundsOf(v.scroll).Y}
		if c != controls[0] && regionDiffers(rest, focused, w, other) {
			t.Errorf("focus on %T changed the header", c)
		}
	}
}
