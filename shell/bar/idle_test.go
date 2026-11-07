package bar

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/service/idle"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
)

type countingDim struct{ dims, undims atomic.Int32 }

func (c *countingDim) Dim(idle.DimPercent) (func() error, error) {
	c.dims.Add(1)
	return func() error { c.undims.Add(1); return nil }, nil
}
func (c *countingDim) Lock() error                      { return nil }
func (c *countingDim) ScreenOff() (func() error, error) { return nil, nil }
func (c *countingDim) Suspend() error                   { return nil }

type inhibitRig struct {
	state   *idleinhibit.State
	actions *countingDim
}

func startInhibitRig(t *testing.T) *inhibitRig {
	t.Helper()
	a := apptest.Run(t)
	state := idleinhibit.NewState(idleinhibit.Duration{})
	holder := &idleHolder{state: state, inhibit: a.InhibitIdle}
	var anchor *widget.Box
	if err := apptest.On(a, func() error {
		anchor = widget.NewBox(widget.Row, 0, 0)
		anchor.SetSizeRequest(40, 30)
		layer, err := a.NewLayer(app.LayerConfig{Root: anchor, Anchor: app.AnchorTop | app.AnchorLeft | app.AnchorRight, Height: 30, Namespace: "idle-rig", Layer: app.LayerTop})
		if err != nil {
			return err
		}
		holder.add(layer)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	apptest.Until(t, a, "the bar is laid out", func() bool { return anchor.Bounds() != render.Rect{} })
	changes, stop := state.Changes()
	t.Cleanup(stop)
	go func() {
		for range changes {
			a.Invoke(holder.sync)
		}
	}()
	comp, err := idle.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(comp.Close)
	rig := &inhibitRig{state: state, actions: &countingDim{}}
	svc := idle.New(comp, rig.actions)
	t.Cleanup(func() { _ = svc.Close() })
	share, err := idle.NewDimPercent(50)
	if err != nil {
		t.Fatal(err)
	}
	second, err := idle.NewTimeout(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p := idle.Policy{DimPercent: share}
	p.Timeouts[idle.Dim] = idle.Timeouts{AC: second, Battery: second}
	if err := svc.Configure(p, true, idle.AC); err != nil {
		t.Fatal(err)
	}
	return rig
}

func (r *inhibitRig) awaitDim(t *testing.T, what string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); r.actions.dims.Load() == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: the dim stage never ran", what)
		}
	}
}

func (r *inhibitRig) holdsFor(t *testing.T, what string, d time.Duration) {
	t.Helper()
	time.Sleep(d)
	if n := r.actions.dims.Load(); n != 0 {
		t.Fatalf("%s: the dim stage ran %d times while held", what, n)
	}
}

func TestEveryInhibitorHoldsTheIdleStagesAndReleasingReArmsThem(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hold    func(t *testing.T, s *idleinhibit.State) (release func())
		holdFor time.Duration
	}{
		{"caffeine", func(_ *testing.T, s *idleinhibit.State) func() {
			s.Enable(true)
			return s.Disable
		}, 2500 * time.Millisecond},
		{"portal", func(_ *testing.T, s *idleinhibit.State) func() {
			id := s.AddHold(idleinhibit.Hold{Owner: ":1.42", App: "org.example.Player", Reason: "Playing"})
			return func() { s.ReleaseHold(id) }
		}, 2500 * time.Millisecond},
		{"screensaver", holdThroughScreenSaver, 2500 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := startInhibitRig(t)
			release := tc.hold(t, rig.state)
			rig.holdsFor(t, tc.name, tc.holdFor)
			release()
			rig.awaitDim(t, tc.name+" released")
		})
	}
}

func TestWithoutAnInhibitorTheStageRuns(t *testing.T) {
	rig := startInhibitRig(t)
	rig.awaitDim(t, "nothing held")
}

func holdThroughScreenSaver(t *testing.T, s *idleinhibit.State) func() {
	t.Helper()
	bus := dbustest.Start(t)
	release, err := idleinhibit.NewDaemon(s).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	app := bus.Conn(t)
	var cookie uint32
	if err := app.Object(idleinhibit.ScreenSaverName, idleinhibit.ScreenSaverPath).Call(idleinhibit.ScreenSaverName+".Inhibit", 0, "chromium", "Playing video").Store(&cookie); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := app.Object(idleinhibit.ScreenSaverName, idleinhibit.ScreenSaverPath).Call(idleinhibit.ScreenSaverName+".UnInhibit", 0, cookie).Err; err != nil {
			t.Error(err)
		}
	}
}

type fixedWatcher struct{ idle []func() }

func (w *fixedWatcher) Watch(_ time.Duration, idle, _ func()) (func(), error) {
	w.idle = append(w.idle, idle)
	return func() {}, nil
}

func TestIdleTooltipNamesTheNextStageAndWhoHoldsIdle(t *testing.T) {
	state := idleinhibit.NewState(idleinhibit.Duration{})
	if got := idleTooltip(nil, state); got != i18n.T("bar-idle-policy-off") {
		t.Errorf("no policy: %q", got)
	}
	w := &fixedWatcher{}
	svc := idle.New(w, &countingDim{})
	p := config.DefaultsIdle().Policy()
	if err := svc.Configure(p, false, idle.AC); err != nil {
		t.Fatal(err)
	}
	if got := idleTooltip(svc, state); got != i18n.T("bar-idle-policy-off") {
		t.Errorf("disabled policy: %q", got)
	}
	if err := svc.Configure(p, true, idle.AC); err != nil {
		t.Fatal(err)
	}
	if got := idleTooltip(svc, state); got != i18n.T("bar-idle-next", i18n.Str("stage", "dim"), i18n.Str("after", "4m")) {
		t.Errorf("armed: %q", got)
	}
	w.idle[0]()
	if got := idleTooltip(svc, state); got != i18n.T("bar-idle-next", i18n.Str("stage", "lock"), i18n.Str("after", "5m")) {
		t.Errorf("dimmed: %q", got)
	}
	state.Enable(true)
	state.AddHold(idleinhibit.Hold{Owner: ":1.7", App: "chromium"})
	if got := idleTooltip(svc, state); got != i18n.T("bar-idle-held-by", i18n.Str("holders", "you, chromium")) {
		t.Errorf("held: %q", got)
	}
}
