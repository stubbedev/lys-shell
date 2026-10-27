package bar

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

func newFakeLysWM(t *testing.T) *lyswmtest.WM {
	t.Helper()
	clearCompositorEnv(t)
	return lyswmtest.Start(t)
}

const lysWMTwoOutputs = `[{"id":1,"output":"DP-1","windows":2,"visible":false,"focused":false},` +
	`{"id":2,"output":"DP-1","windows":0,"visible":true,"focused":true},` +
	`{"id":3,"output":"HDMI-A-1","windows":1,"visible":true,"focused":false}]`

const lysWMWindows = `[{"id":4,"app_id":"foot","title":"~","workspace":1,"output":"DP-1","urgent":false},` +
	`{"id":5,"app_id":"firefox","title":"Docs","workspace":1,"output":"DP-1","urgent":true},` +
	`{"id":9,"app_id":"","title":"","workspace":3,"output":"HDMI-A-1","urgent":false}]`

func TestLysWMModels(t *testing.T) {
	var replies []lyswm.Workspace
	var wins []lyswm.Window
	if err := json.Unmarshal([]byte(lysWMTwoOutputs), &replies); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lysWMWindows), &wins); err != nil {
		t.Fatal(err)
	}
	workspaces, windows := lysWMModels(replies, wins)
	want := []cwsWorkspace{
		{id: 1, num: 1, output: "DP-1", hasOutput: true, urgent: true, hasWindows: true},
		{id: 2, num: 2, output: "DP-1", hasOutput: true, active: true, focused: true},
		{id: 3, num: 3, output: "HDMI-A-1", hasOutput: true, active: true, hasWindows: true},
	}
	if !reflect.DeepEqual(workspaces, want) {
		t.Errorf("workspaces =\n%+v\nwant\n%+v", workspaces, want)
	}
	if len(windows) != 3 || windows[1].id != 5 || windows[1].workspace != 1 || !windows[1].urgent || windows[1].app.AppID != "firefox" {
		t.Errorf("windows = %+v", windows)
	}
	if windows[2].app.HasAppID || windows[2].app.HasTitle {
		t.Errorf("an empty app id and title read as set: %+v", windows[2].app)
	}
}

func newTestLysWM(t *testing.T, cfg *config.Config, connector string) *cwsModule {
	t.Helper()
	ctx := newTestContext(t, cfg)
	ctx.Connector = connector
	module, err := Create("lys-wm-workspaces", ctx)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := module.(*cwsModule)
	t.Cleanup(m.Stop)
	return m
}

func TestLysWMWorkspacesShowTheBarOutputsNumbers(t *testing.T) {
	f := newFakeLysWM(t)
	f.Result("workspaces", lysWMTwoOutputs)
	f.Result("windows", lysWMWindows)
	m := newTestLysWM(t, config.Defaults(), "DP-1")
	labels := make([]string, 0, len(m.models))
	for _, model := range m.models {
		labels = append(labels, model.label)
	}
	if !reflect.DeepEqual(labels, []string{"1", "2"}) {
		t.Errorf("DP-1's bar shows %v, want its workspaces 1 and 2", labels)
	}

	cfg := config.Defaults()
	cfg.LysWMWorkspaces.MonitorSpecific = false
	if got := len(newTestLysWM(t, cfg, "DP-1").models); got != 3 {
		t.Errorf("monitor-specific off shows %d workspaces, want all 3", got)
	}
}

func TestLysWMWorkspacesClicksDispatch(t *testing.T) {
	f := newFakeLysWM(t)
	f.Result("workspaces", lysWMTwoOutputs)
	cfg := config.Defaults()
	cfg.LysWMWorkspaces.MiddleClick = config.ParseWorkspaceClickAction("focus:last")
	m := newTestLysWM(t, cfg, "DP-1")
	first := m.segs.Segment(0)
	router, p := treetest.Pointer(m.segs, first, 500, 40)
	router.Press(widget.BTNLeft, p, 0)
	router.Release(widget.BTNLeft, p)
	f.ExpectDispatch(t, "workspace 1")
	router.Axis(0, 1, 0)
	f.ExpectDispatch(t, "workspace e+1")
	router.Axis(0, -1, 0)
	f.ExpectDispatch(t, "workspace e-1")
	router.Press(widget.BTNMiddle, p, 0)
	f.ExpectNoDispatch(t)
	if err := (lysWMBackend{}).focusLast(context.Background()); !errors.Is(err, errNoPreviousWorkspace) {
		t.Errorf("focus:last = %v, want the missing-action error", err)
	}
}

func TestLysWMWorkspacesRequireLysWM(t *testing.T) {
	advertise(t, "SWAYSOCK")
	if _, err := Create("lys-wm-workspaces", newTestContext(t, config.Defaults())); err == nil {
		t.Error("lys-wm-workspaces under sway: want an error, got a module")
	}
}

func TestLysWMWorkspacesFollowTheEventStream(t *testing.T) {
	f := newFakeLysWM(t)
	ticks, stop, err := lysWMBackend{}.subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	f.AwaitWatch(t)
	f.Events <- `{"event":"submap","submap":"resize"}`
	f.Events <- `{"event":"workspace","output":"DP-1","workspace":3}`
	select {
	case <-ticks:
	case <-time.After(2 * time.Second):
		t.Fatal("a workspace event did not tick")
	}
	select {
	case <-ticks:
		t.Error("the submap event ticked the workspaces too")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWindowTitleFollowsLysWMFocus(t *testing.T) {
	f := newFakeLysWM(t)
	f.Result("activewindow", `{"id":4,"app_id":"foot","title":"~"}`)
	cfg := config.Defaults()
	cfg.WindowTitle.Format = "{{ app }}: {{ title }}"
	module, err := Create("window-title", newTestContext(t, cfg))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	label := findLabel(module.Root())
	waitForText(t, label, "foot: ~")
	f.AwaitWatch(t)
	f.Result("activewindow", `{"id":5,"app_id":"firefox","title":"Docs"}`)
	f.Events <- `{"event":"focus","window":5}`
	waitForText(t, label, "firefox: Docs")
	f.Result("activewindow", `null`)
	f.Events <- `{"event":"focus","window":null}`
	waitForText(t, label, windowTitleLabel(cfg.WindowTitle.Format, "", ""))
}

func TestKeyboardLayoutReadsLysWMsLayoutBeforeAnyEvent(t *testing.T) {
	f := newFakeLysWM(t)
	label := newTestKeyboardLayout(t, config.Defaults())
	if got := onHeadlessLoop(label.Text); got != "English (US)" {
		t.Errorf("before any layout event the label reads %q, want the layout request's English (US)", got)
	}
	f.AwaitWatch(t)
	f.Events <- `{"event":"layout","layout":{"index":1,"name":"German"}}`
	waitLabel(t, label, "German")
}

func TestKeyboardLayoutLysWMUnnamedLayoutIsAnError(t *testing.T) {
	f := newFakeLysWM(t)
	f.Result("layout", `{"index":2,"name":""}`)
	source, err := keyboardLayoutSource(newTestContext(t, config.Defaults()))
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	name, err := source.read()
	if err == nil || !strings.Contains(err.Error(), "layout 2 has no name") {
		t.Errorf("read = %q, %v; want the unnamed-layout error", name, err)
	}
	label := newTestKeyboardLayout(t, config.Defaults())
	if got := onHeadlessLoop(label.Text); got != unknownLayout {
		t.Errorf("an unnamed layout reads %q, want %q", got, unknownLayout)
	}
}

func TestKeyboardLayoutLysWMWithoutALayoutAnswerFails(t *testing.T) {
	f := newFakeLysWM(t)
	f.Result("layout", `null`)
	if _, err := Create("keyboard-input", newTestContext(t, config.Defaults())); err == nil || !strings.Contains(err.Error(), "seed the layout") {
		t.Errorf("Create = %v, want the seed error", err)
	}
}

func TestKeybindModeShowsLysWMsSubmapBeforeAnyEvent(t *testing.T) {
	f := newFakeLysWM(t)
	f.Result("submap", `"resize"`)
	module, err := Create("keybind-mode", newTestContext(t, config.Defaults()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitForText(t, findLabel(module.Root()), "resize")
}

func TestKeybindModeFollowsLysWMSubmaps(t *testing.T) {
	f := newFakeLysWM(t)
	module, err := Create("keybind-mode", newTestContext(t, config.Defaults()))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	label := findLabel(module.Root())
	defaultLabel := keybindModeLabel(config.Defaults().KeybindMode.Format, "")
	waitForText(t, label, defaultLabel)
	f.AwaitWatch(t)
	f.Events <- `{"event":"submap","submap":"resize"}`
	waitForText(t, label, "resize")
	f.Events <- `{"event":"submap","submap":""}`
	waitForText(t, label, defaultLabel)
	if strings.Contains(onHeadlessLoop(label.Text), "resize") {
		t.Error("the default binds still read as resize")
	}
}
