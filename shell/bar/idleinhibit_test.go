package bar

import (
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/render"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
)

func TestIdleInhibitLabel(t *testing.T) {
	// Off: dashes for remaining, the stored duration still shows.
	if got := idleInhibitLabel("{{ state }} {{ remaining }} {{ duration }}", false, 60, 0); got != i18n.T("bar-idle-inhibit-off")+" - 60" {
		t.Errorf("= %q", got)
	}
	// On, timed: H:MM:SS above the hour, M:SS within it.
	if got := idleInhibitLabel("{{ state }} {{ remaining }}", true, 60, 3661); got != i18n.T("bar-idle-inhibit-on")+" 1:01:01" {
		t.Errorf("= %q", got)
	}
	if got := idleInhibitLabel("{{ state }} {{ remaining }}", true, 60, 95); got != i18n.T("bar-idle-inhibit-on")+" 1:35" {
		t.Errorf("= %q", got)
	}
	// On, indefinite: the infinity sign for both.
	if got := idleInhibitLabel("{{ state }} {{ remaining }} {{ duration }}", true, 0, 0); got != i18n.T("bar-idle-inhibit-on")+" ∞ ∞" {
		t.Errorf("= %q", got)
	}
}

func TestLoadFileAppliesIdleInhibit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	good := "modules:\n  idle-inhibit:\n    format: \"[{{ state }}]\"\n    startup-duration: 30\n    icon-active: tb-eye-symbolic\n"
	if err := osWrite(path, good); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.IdleInhibit.Format != "[{{ state }}]" || c.IdleInhibit.StartupDuration != 30 {
		t.Errorf("config = %+v", c.IdleInhibit)
	}
	if got := c.IdleInhibit.Icons()[config.IdleInhibitActive].Name; got != "tb-eye-symbolic" {
		t.Errorf("icon-active = %q", got)
	}
	if got := c.IdleInhibit.Icons()[config.IdleInhibitInactive].Name; got != "ld-bed-symbolic" {
		t.Errorf("icon-inactive default changed: %q", got)
	}

	if err := osWrite(path, "modules:\n  idle-inhibit:\n    format: \"\"\n"); err != nil {
		t.Fatal(err)
	}
	// The schema puts no constraint on the format string.
	if _, err := config.LoadFile(path); err != nil {
		t.Errorf("empty format: accepted by the schema, got %v", err)
	}
}

func TestIdleInhibitModuleFollowsState(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	state := idleinhibit.NewState(60)
	ctx.IdleInhibit = state

	module, err := Create("idle-inhibit", ctx)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	label := findLabel(module.Root())
	if label.Text() != i18n.T("bar-idle-inhibit-off") {
		t.Errorf("initial label = %q, want Off", label.Text())
	}

	// Enabling the shared state flips the label (the module's follower
	// goroutine consumes the change ticks; poll the label).
	state.Enable(true)
	waitForText(t, label, i18n.T("bar-idle-inhibit-on"))
	state.Disable()
	waitForText(t, label, i18n.T("bar-idle-inhibit-off"))

	// A module built without the shared state still works standalone.
	standalone, err := Create("idle-inhibit", newTestContext(t, cfg))
	if err != nil {
		t.Fatalf("standalone Create: %v", err)
	}
	if _, ok := standalone.(*idleInhibitModule); !ok {
		t.Errorf("standalone = %T", standalone)
	}
	if label := findLabel(standalone.Root()); label.Text() != i18n.T("bar-idle-inhibit-off") {
		t.Errorf("standalone label = %q", label.Text())
	}
}

// A configured label-color reaches the label and icon through the
// button's variables only: neither pins an ink, on or off.
func TestIdleInhibitKeepsTheStylesheetInk(t *testing.T) {
	cfg := config.Defaults()
	red, err := config.ParseColorValue("red")
	if err != nil {
		t.Fatal(err)
	}
	cfg.IdleInhibit.LabelColor = red
	ctx := newTestContext(t, cfg)
	state := idleinhibit.NewState(60)
	ctx.IdleInhibit = state
	module, err := Create("idle-inhibit", ctx)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := module.(*idleInhibitModule)
	for _, on := range []bool{false, true} {
		if on {
			state.Enable(true)
			waitForText(t, m.btn.Label(), i18n.T("bar-idle-inhibit-on"))
		}
		if ink, tint := onHeadlessLoop(func() render.Color { return m.btn.Label().Color() }), onHeadlessLoop(func() render.Color { return m.icon.Tint() }); ink != 0 || tint != 0 {
			t.Errorf("active %v: label ink %#08x icon tint %#08x, want the stylesheet's", on, ink, tint)
		}
	}
	state.Disable()
}

// namedHost is a fakeHost with an identity, so two compare unequal.
type namedHost struct {
	fakeHost
	name string
}

// The bar holds one inhibitor while the state inhibits, from the user's
// toggle or an app's hold, moves it when its bar closes, and drops it
// when nothing asks any more.
func TestIdleHolderFollowsTheStateAcrossBars(t *testing.T) {
	state := idleinhibit.NewState(0)
	var bound []app.Host
	live := 0
	h := &idleHolder{state: state, inhibit: func(host app.Host) (func(), error) {
		bound = append(bound, host)
		live++
		return func() { live-- }, nil
	}}
	first, second := &namedHost{name: "DP-1"}, &namedHost{name: "DP-2"}
	h.add(first)
	h.add(second)
	if live != 0 {
		t.Fatal("an inhibitor without anything asking for one")
	}
	id := state.AddHold(idleinhibit.Hold{Owner: ":1.9", App: "org.app"})
	h.sync()
	if live != 1 || bound[0] != first {
		t.Fatalf("an app's hold: %d live inhibitors on %v", live, bound)
	}
	h.sync()
	if live != 1 {
		t.Errorf("a repeated sync stacked inhibitors: %d", live)
	}
	h.remove(first)
	if live != 1 || bound[len(bound)-1] != second {
		t.Errorf("closing the holding bar: %d live, last bound %v", live, bound[len(bound)-1])
	}
	state.ReleaseHold(id)
	state.Enable(true)
	h.sync()
	if live != 1 {
		t.Errorf("the user's toggle: %d live", live)
	}
	state.Disable()
	h.sync()
	if live != 0 {
		t.Errorf("nothing asks: %d live inhibitors", live)
	}
	h.remove(second)
	state.Enable(true)
	h.sync()
	if live != 0 {
		t.Error("an inhibitor bound with no bar open")
	}
}
