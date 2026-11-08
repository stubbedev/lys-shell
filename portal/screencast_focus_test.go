package portal

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-desktop/service/lyswm/lyswmtest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const twoOutputsDP2Focused = `[{"name":"DP-1","rect":{"x":0,"y":0,"width":1280,"height":720},"focused":false},` +
	`{"name":"DP-2","rect":{"x":1280,"y":0,"width":1920,"height":1080},"focused":true}]`

func TestAMonitorRequestOnLysWMSharesTheFocusedOutputWithoutThePicker(t *testing.T) {
	r, f := castRig(t)
	wm := lyswmtest.Start(t)
	wm.Result("outputs", twoOutputsDP2Focused)
	p := serveSharePicker(t, r, "/screen:DP-1")
	r.castSession(t, Vardict{"persist_mode": dbus.MakeVariant(uint32(2))})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:DP-2"}) {
		t.Errorf("started %v, want the focused DP-2", got)
	}
	if asked := p.asked.Load(); len(asked) != 0 {
		t.Errorf("the picker was asked %v", asked)
	}
	if _, ok := res["restore_data"]; ok {
		t.Error("a share nobody chose in the picker granted a restore token")
	}
}

func TestAMonitorRequestOnOneOutputSharesItWithoutThePicker(t *testing.T) {
	r, f := castRig(t)
	f.layout.Store([]outputGeometry{{name: "eDP-1", width: 1920, height: 1200}})
	p := serveSharePicker(t, r, "/screen:DP-1")
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:eDP-1"}) {
		t.Errorf("started %v, want the one output", got)
	}
	if asked := p.asked.Load(); len(asked) != 0 {
		t.Errorf("the picker was asked %v", asked)
	}
}

func TestWithoutLysWMSeveralOutputsStillAskThePicker(t *testing.T) {
	r, f := castRig(t)
	p := serveSharePicker(t, r, "/screen:DP-1")
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:DP-1"}) {
		t.Errorf("started %v, want the picker's DP-1", got)
	}
	if asked := p.asked.Load(); len(asked) != 1 {
		t.Errorf("the picker was asked %d times, want once", len(asked))
	}
}

func TestSeveralSourcesOrNoShownWindowStillAskThePickerOnLysWM(t *testing.T) {
	for name, options := range map[string]Vardict{
		"a window":         {"types": dbus.MakeVariant(sourceWindow)},
		"a window or more": {"types": dbus.MakeVariant(sourceMonitor | sourceWindow)},
		"several monitors": {"multiple": dbus.MakeVariant(true)},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := castRig(t)
			wm := lyswmtest.Start(t)
			wm.Result("outputs", twoOutputsDP2Focused)
			p := serveSharePicker(t, r, "")
			r.castSession(t, options)
			if code, _ := r.castStart(t); code != ResponseCancelled {
				t.Errorf("Start = %d, want the picker's cancel", code)
			}
			if asked := p.asked.Load(); len(asked) != 1 {
				t.Errorf("the picker was asked %d times, want once", len(asked))
			}
		})
	}
}

func TestAFocusedOutputLysWMDoesNotListAsksThePicker(t *testing.T) {
	r, f := castRig(t)
	wm := lyswmtest.Start(t)
	wm.Result("outputs", `[{"name":"HDMI-A-1","focused":true}]`)
	p := serveSharePicker(t, r, "/screen:DP-1")
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:DP-1"}) || len(p.asked.Load()) != 1 {
		t.Errorf("started %v after %d picker calls, want the picker's choice", got, len(p.asked.Load()))
	}
}
