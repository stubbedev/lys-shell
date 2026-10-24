package portal

import (
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// fakeShooter is the shell's dev.stubbe.lys.Screenshot1; an app's
// capture must be the private one, so the user's records as such.
type fakeShooter struct {
	gate
	path      string
	err       dbustest.Var[error]
	modes     dbustest.Var[[]string]
	unpicked  dbustest.Var[bool]
	userShots dbustest.Var[int]
}

func (f *fakeShooter) Capture(portalreq.Caller, string, string) (string, *dbus.Error) {
	f.userShots.Update(func(n int) int { return n + 1 })
	return "/home/u/Pictures/user.png", nil
}

func (f *fakeShooter) CapturePrivate(c portalreq.Caller, mode, target string) (string, *dbus.Error) {
	f.modes.Update(func(m []string) []string { return append(m, mode+"|"+target) })
	if f.enter(c) {
		return "", nil
	}
	if err := f.err.Load(); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return f.path, nil
}

func (f *fakeShooter) PickColor(c portalreq.Caller) (bool, float64, float64, float64, *dbus.Error) {
	if f.enter(c) || f.unpicked.Load() {
		return false, 0, 0, 0, nil
	}
	if err := f.err.Load(); err != nil {
		return false, 0, 0, 0, dbus.MakeFailedError(err)
	}
	return true, 0.25, 0.5, 1, nil
}

func (f *fakeShooter) Dismiss(handle string) *dbus.Error {
	f.gate.Dismiss(handle)
	return nil
}

func serveShooter(t *testing.T, r *rig, f *fakeShooter) {
	t.Helper()
	conn := r.bus.Conn(t)
	if err := conn.Export(f, screenshot.ServicePath, screenshot.ServiceName); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(screenshot.ServiceName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
}

const (
	screenshotCall = "org.freedesktop.impl.portal.Screenshot.Screenshot"
	pickColorCall  = "org.freedesktop.impl.portal.Screenshot.PickColor"
)

func TestScreenshotProperties(t *testing.T) {
	r := newRig(t, nil)
	for name, want := range map[string]uint32{"AvailableTargets": 15, "version": 3} {
		if v, err := r.obj.GetProperty("org.freedesktop.impl.portal.Screenshot." + name); err != nil || v.Value() != want {
			t.Errorf("%s = %v, %v", name, v, err)
		}
	}
}

// An app's screenshot is the private capture, replied as a file URI;
// without a target, interactive selects an area and anything else takes
// the screen; PickColor replies the color.
func TestScreenshotRepliesTheFileURI(t *testing.T) {
	r := newRig(t, nil)
	f := &fakeShooter{path: "/home/u/.cache/lys-shell/portal-screenshots/Shot 1#.png"}
	serveShooter(t, r, f)
	code, res := r.interactive(t, screenshotCall, handle, "org.app", "", Vardict{"interactive": dbus.MakeVariant(true)})
	if code != ResponseSuccess || res["uri"].Value() != "file:///home/u/.cache/lys-shell/portal-screenshots/Shot%201%23.png" {
		t.Errorf("interactive = %d %v", code, res)
	}
	r.interactive(t, screenshotCall, handle, "org.app", "", Vardict{})
	if got := f.modes.Load(); len(got) != 2 || got[0] != "region|" || got[1] != "screen|" {
		t.Errorf("modes = %v: interactive selects a region, else the whole screen", got)
	}
	if f.userShots.Load() != 0 {
		t.Error("an app's screenshot took the user's capture, with its save, clipboard and notification")
	}
	code, res = r.interactive(t, pickColorCall, handle, "org.app", "", Vardict{})
	if c, ok := res["color"].Value().([]any); code != ResponseSuccess || !ok || c[0] != 0.25 || c[1] != 0.5 || c[2] != 1.0 {
		t.Errorf("PickColor = %d %v", code, res)
	}
}

// Each target takes its capture: the screen, an area, the active
// window, or the window the user picks from the share picker's window
// page; a target not offered fails.
func TestScreenshotTargets(t *testing.T) {
	r := newRig(t, nil)
	f := &fakeShooter{path: "/c/shot.png"}
	serveShooter(t, r, f)
	p := serveSharePicker(t, r, "/window:id-7")
	target := func(v uint32) Vardict {
		return Vardict{"target": dbus.MakeVariant(v), "interactive": dbus.MakeVariant(true)}
	}
	for _, v := range []uint32{targetScreen, targetArea, targetActiveWindow, targetPickedWindow} {
		if code, _ := r.interactive(t, screenshotCall, handle, "org.app", "", target(v)); code != ResponseSuccess {
			t.Errorf("target %d = %d", v, code)
		}
	}
	want := []string{"screen|", "region|", "window|", "window|id-7"}
	if got := f.modes.Load(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Errorf("modes = %v, want %v", got, want)
	}
	if asked := p.asked.Load(); len(asked) != 1 || asked[0] != (sharepicker.Request{Sources: sharepicker.SourceWindows}) {
		t.Errorf("window picker asked %v", asked)
	}
	for _, v := range []uint32{0, 16, targetScreen | targetArea} {
		if code, _ := r.interactive(t, screenshotCall, handle, "org.app", "", target(v)); code != ResponseOther {
			t.Errorf("target %d = %d, want other", v, code)
		}
	}
	p.reply.Store("")
	if code, _ := r.interactive(t, screenshotCall, handle, "org.app", "", target(targetPickedWindow)); code != ResponseCancelled {
		t.Errorf("no window picked = %d, want cancelled", code)
	}
	p.reply.Store("/screen:DP-1")
	if code, _ := r.interactive(t, screenshotCall, handle, "org.app", "", target(targetPickedWindow)); code != ResponseOther {
		t.Errorf("an output for a window pick = %d, want other", code)
	}
}

// A cancel is cancelled and a failure other, for captures and picks.
func TestScreenshotCancelAndFailure(t *testing.T) {
	r := newRig(t, nil)
	if code, _ := r.interactive(t, screenshotCall, handle, "org.app", "", Vardict{}); code != ResponseOther {
		t.Errorf("no shell = %d", code)
	}
	f := &fakeShooter{}
	serveShooter(t, r, f)
	if code, res := r.interactive(t, screenshotCall, handle, "org.app", "", Vardict{}); code != ResponseCancelled || len(res) != 0 {
		t.Errorf("empty path = %d %v, want cancelled", code, res)
	}
	f.unpicked.Store(true)
	if code, res := r.interactive(t, pickColorCall, handle, "org.app", "", Vardict{}); code != ResponseCancelled || len(res) != 0 {
		t.Errorf("pick cancel = %d %v, want cancelled", code, res)
	}
	f.unpicked.Store(false)
	f.err.Store(errors.New("grim failed"))
	if code, _ := r.interactive(t, screenshotCall, handle, "org.app", "", Vardict{}); code != ResponseOther {
		t.Errorf("capture error = %d", code)
	}
	if code, res := r.interactive(t, pickColorCall, handle, "org.app", "", Vardict{}); code != ResponseOther || len(res) != 0 {
		t.Errorf("pick failure = %d %v, want other", code, res)
	}
}
