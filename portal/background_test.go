package portal

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/toplevels"
	"github.com/stubbedev/lys-shell/service/notifications"
)

// fakeWindows is the compositor's window list; err makes it fail.
type fakeWindows struct {
	list dbustest.Var[[]toplevels.Window]
	err  dbustest.Var[error]
}

func (f *fakeWindows) windows() ([]toplevels.Window, error) {
	if err := f.err.Load(); err != nil {
		return nil, err
	}
	return f.list.Load(), nil
}

func (f *fakeWindows) close() {}

func backgroundRig(t *testing.T) (*rig, *fakeWindows) {
	t.Helper()
	f := &fakeWindows{}
	return newRig(t, func(b *Backend) { b.windows = f }), f
}

func appState(t *testing.T, r *rig) (map[string]uint32, error) {
	t.Helper()
	var raw map[string]dbus.Variant
	if err := r.obj.Call(BackgroundIface+".GetAppState", 0).Store(&raw); err != nil {
		return nil, err
	}
	out := map[string]uint32{}
	for id, v := range raw {
		state, ok := v.Value().(uint32)
		if !ok {
			t.Fatalf("%s's state %v is not a u", id, v)
		}
		out[id] = state
	}
	return out, nil
}

// An app with a window is running, active when one of its windows is
// the activated one; a window with no app id names no app. No window
// list is an error, never an empty answer.
func TestGetAppStateFollowsTheWindows(t *testing.T) {
	r, f := backgroundRig(t)
	if got, err := appState(t, r); err != nil || len(got) != 0 {
		t.Errorf("no windows = %v, %v", got, err)
	}
	f.list.Store([]toplevels.Window{
		{AppID: "org.a.App"}, {AppID: "org.b.App"}, {AppID: "org.b.App", Activated: true}, {AppID: ""},
	})
	got, err := appState(t, r)
	if want := map[string]uint32{"org.a.App": appRunning, "org.b.App": appActive}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("GetAppState = %v, %v; want %v", got, err, want)
	}
	f.err.Store(errors.New("no compositor"))
	if got, err := appState(t, r); err == nil {
		t.Errorf("without a window list GetAppState = %v", got)
	}
}

// The window list starts on the first ask and is asked again after a
// failure; its changes reach the frontend as RunningApplicationsChanged.
func TestWindowListRetriesAndAnnounces(t *testing.T) {
	r := newRig(t, nil)
	attempts := 0
	list := &windowList{conn: r.client, watch: func(func()) (*toplevels.Watcher, error) {
		attempts++
		return nil, errors.New("no compositor")
	}}
	for range 2 {
		if _, err := list.windows(); err == nil {
			t.Fatal("a failed watch listed windows")
		}
	}
	if attempts != 2 {
		t.Errorf("%d attempts, want one per ask", attempts)
	}
	list.changed()
	for deadline := time.After(2 * time.Second); ; {
		select {
		case s := <-r.signals:
			if s.Name == BackgroundIface+".RunningApplicationsChanged" && s.Path == ObjectPath {
				return
			}
		case <-deadline:
			t.Fatal("no RunningApplicationsChanged")
		}
	}
}

// notifyBackground calls NotifyBackground in the background.
func notifyBackground(r *rig) <-chan []any {
	done := make(chan []any, 1)
	go func() {
		var code uint32
		var res map[string]dbus.Variant
		err := r.obj.Call(BackgroundIface+".NotifyBackground", 0, handle, "org.app", "App").Store(&code, &res)
		done <- []any{code, res, err}
	}()
	return done
}

// waitNotify waits for the n-th Notify.
func waitNotify(t *testing.T, d *fakeNotifyd, n int) notifyCall {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); len(d.calls.Load()) < n; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no Notify")
		}
	}
	return d.calls.Load()[n-1]
}

// NotifyBackground asks in a persistent notification and replies only
// once the user answers: Forbid 0, Allow 1, Allow once 2, and a
// notification closed unanswered allows this instance (2).
func TestNotifyBackgroundAsksTheUser(t *testing.T) {
	r, _ := backgroundRig(t)
	d := serveNotifyd(t, r)
	for i, tc := range []struct {
		signal string
		key    string
		want   uint32
	}{
		{"ActionInvoked", "forbid", backgroundForbid},
		{"ActionInvoked", "allow", backgroundAllow},
		{"ActionInvoked", "allow-once", backgroundAllowOnce},
		{"NotificationClosed", "", backgroundAllowOnce},
	} {
		done := notifyBackground(r)
		call := waitNotify(t, d, i+1)
		if call.appName != "org.app" || call.summary != "App is running in the background" || call.expire != 0 ||
			!slices.Equal(call.actions, backgroundActions) || call.hints["desktop-entry"].Value() != "org.app" {
			t.Errorf("Notify = %+v", call)
		}
		select {
		case got := <-done:
			t.Fatalf("replied %v before the user answered", got)
		case <-time.After(50 * time.Millisecond):
		}
		id := d.next.Load()
		if tc.signal == "ActionInvoked" {
			_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", id, tc.key)
		} else {
			_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".NotificationClosed", id, uint32(2))
		}
		got := <-done
		if res, _ := got[1].(map[string]dbus.Variant); got[2] != nil || got[0] != ResponseSuccess || res["result"].Value() != tc.want {
			t.Errorf("%s %q = %v, want result %d", tc.signal, tc.key, got, tc.want)
		}
	}
	// Another notification's action answers nothing.
	done := notifyBackground(r)
	waitNotify(t, d, 5)
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", d.next.Load()+7, "allow")
	select {
	case got := <-done:
		t.Fatalf("another notification's action answered %v", got)
	case <-time.After(50 * time.Millisecond):
	}
	// The frontend closing the request withdraws the notification.
	if err := r.client.Object(BusName, handle).Call(RequestIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if got := <-done; got[0] != ResponseCancelled {
		t.Errorf("after Close = %v, want cancelled", got)
	}
	if closed := d.closed.Load(); !slices.Contains(closed, d.next.Load()) {
		t.Errorf("closed %v, want the withdrawn %d", closed, d.next.Load())
	}
}

// With no notification daemon there is no answer to wait for: the call
// fails with no result rather than allowing.
func TestNotifyBackgroundWithoutADaemon(t *testing.T) {
	r, _ := backgroundRig(t)
	got := <-notifyBackground(r)
	if res, _ := got[1].(map[string]dbus.Variant); got[0] != ResponseOther || len(res) != 0 {
		t.Errorf("NotifyBackground without a daemon = %v", got)
	}
}
