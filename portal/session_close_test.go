package portal

import (
	"sync"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// sessionStartCase is a Start that shares the screens of a session
// (ScreenCast's, or RemoteDesktop's sharing them), with the picker
// replying two sources.
type sessionStartCase struct {
	name  string
	setup func(t *testing.T) (*rig, *fakeStarts, *fakeSharePicker)
	start func(r *rig) (uint32, error)
}

func sessionStartCases() []sessionStartCase {
	const reply = "/screen:DP-1;window:w1"
	sources := Vardict{"types": dbus.MakeVariant(sourceMonitor | sourceWindow), "multiple": dbus.MakeVariant(true)}
	call := func(r *rig, method string) (uint32, error) {
		var code uint32
		var res map[string]dbus.Variant
		err := r.obj.Call(method, 0, handle, sessA, "org.app", "", Vardict{}).Store(&code, &res)
		return code, err
	}
	return []sessionStartCase{
		{"ScreenCast", func(t *testing.T) (*rig, *fakeStarts, *fakeSharePicker) {
			r, f := castRig(t)
			p := serveSharePicker(t, r, reply)
			r.castSession(t, sources)
			return r, f, p
		}, func(r *rig) (uint32, error) { return call(r, ScreenCastIface+".Start") }},
		{"RemoteDesktop", func(t *testing.T) (*rig, *fakeStarts, *fakeSharePicker) {
			r, _, f := remoteRigStarts(t, nil)
			serveDialogs(t, r, true)
			p := serveSharePicker(t, r, reply)
			r.interactive(t, RemoteDesktopIface+".CreateSession", handle, sessA, "org.app", Vardict{})
			r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", sources)
			r.interactive(t, RemoteDesktopIface+".SelectDevices", handle, sessA, "org.app", Vardict{})
			return r, f, p
		}, func(r *rig) (uint32, error) { return call(r, RemoteDesktopIface+".Start") }},
	}
}

// startAsync runs tc's Start, its reply on the channel.
func startAsync(t *testing.T, tc sessionStartCase, r *rig) <-chan uint32 {
	t.Helper()
	done := make(chan uint32, 1)
	go func() {
		code, err := tc.start(r)
		if err != nil {
			t.Error(err)
		}
		done <- code
	}()
	return done
}

func awaitReply(t *testing.T, done <-chan uint32) uint32 {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(2 * time.Second):
		t.Fatal("the call did not reply")
		return 0
	}
}

func (r *rig) closeSession(t *testing.T, path dbus.ObjectPath) {
	t.Helper()
	if err := r.client.Object(BusName, path).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Errorf("Close %s: %v", path, err)
	}
}

// unclosed counts the started streams nothing stopped.
func (f *fakeStarts) unclosed() int {
	n := 0
	for _, s := range f.streams.Load() {
		if !s.closed.Load() {
			n++
		}
	}
	return n
}

// The app closing its session while the picker is open takes the
// picker down; Start replies cancelled and starts nothing.
func TestSessionCloseDismissesItsPicker(t *testing.T) {
	for _, tc := range sessionStartCases() {
		t.Run(tc.name, func(t *testing.T) {
			r, f, p := tc.setup(t)
			p.hold.Store(true)
			done := startAsync(t, tc, r)
			p.waitOpen(t, handle)
			r.closeSession(t, sessA)
			if code := awaitReply(t, done); code != ResponseCancelled {
				t.Errorf("Start = %d after the session closed, want cancelled", code)
			}
			if got := p.dismissed.Load(); len(got) != 1 || got[0] != string(handle) {
				t.Errorf("picker dismissed %v, want the request's handle", got)
			}
			if got := f.started.Load(); len(got) != 0 {
				t.Errorf("a closed session started %v", got)
			}
			if n := f.unclosed(); n != 0 {
				t.Errorf("%d streams outlived their session", n)
			}
		})
	}
}

// A session that closes after the pick, while its streams start, keeps
// none of them: each is stopped, Start replies cancelled, and no share
// is listed.
func TestSessionCloseWhileStreamsStartStopsThem(t *testing.T) {
	for _, tc := range sessionStartCases() {
		t.Run(tc.name, func(t *testing.T) {
			r, f, _ := tc.setup(t)
			var once sync.Once
			f.starting.Store(func() { once.Do(func() { r.closeSession(t, sessA) }) })
			if code := awaitReply(t, startAsync(t, tc, r)); code != ResponseCancelled {
				t.Errorf("Start = %d after the session closed, want cancelled", code)
			}
			if got := len(f.streams.Load()); got != 2 {
				t.Fatalf("%d streams started, want both picked", got)
			}
			if n := f.unclosed(); n != 0 {
				t.Errorf("%d streams outlived their session", n)
			}
			if got := r.backend.shares.list(); len(got) != 0 {
				t.Errorf("shares %+v of a closed session", got)
			}
		})
	}
}

// An open session streams what the picker answers, whatever it took to
// start: the negative of the closes above.
func TestOpenSessionStreamsThePick(t *testing.T) {
	for _, tc := range sessionStartCases() {
		t.Run(tc.name, func(t *testing.T) {
			r, f, p := tc.setup(t)
			f.starting.Store(func() {})
			if code := awaitReply(t, startAsync(t, tc, r)); code != ResponseSuccess {
				t.Fatalf("Start = %d", code)
			}
			if got := p.dismissed.Load(); len(got) != 0 {
				t.Errorf("an answered picker was dismissed: %v", got)
			}
			if n := f.unclosed(); n != 2 {
				t.Errorf("%d streams run, want both picked", n)
			}
			if !r.sessionRuns(sessA, 40) || !r.sessionRuns(sessA, 41) {
				t.Error("the session does not list its streams")
			}
			if got := r.backend.shares.list(); len(got) != 1 || got[0].Session != sessA {
				t.Errorf("shares %+v, want the session's", got)
			}
		})
	}
}

// A GlobalShortcuts session closed while its consent dialog is open
// takes the dialog down and registers nothing.
func TestSessionCloseDismissesItsShortcutConsent(t *testing.T) {
	r, f, d := shortcutsRig(t, nil)
	r.shortcutSession(t, sessA)
	d.hold.Store(true)
	done := make(chan uint32, 1)
	go func() {
		var code uint32
		var res map[string]dbus.Variant
		if err := r.obj.Call(GlobalShortcutsIface+".BindShortcuts", 0, handle, sessA, []shortcut{{"mic", props("Mute")}}, "", Vardict{}).Store(&code, &res); err != nil {
			t.Error(err)
		}
		done <- code
	}()
	d.waitOpen(t, handle)
	r.closeSession(t, sessA)
	if code := awaitReply(t, done); code != ResponseCancelled {
		t.Errorf("BindShortcuts = %d after the session closed, want cancelled", code)
	}
	if got := d.dismissed.Load(); len(got) != 1 || got[0] != string(handle) {
		t.Errorf("dismissed %v, want the request's handle", got)
	}
	if got := f.registered.Load(); len(got) != 0 {
		t.Errorf("a closed session registered %v", got)
	}
}
