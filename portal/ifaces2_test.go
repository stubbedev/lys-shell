package portal

import (
	"errors"
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/wallpaper"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// fakeNotifyd is the shell's notification daemon.
type fakeNotifyd struct {
	conn   *dbus.Conn
	next   uint32
	notify dbustest.Var[[]any]
	closed dbustest.Var[[]uint32]
}

func (f *fakeNotifyd) Notify(appName string, replaces uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, expire int32) (uint32, *dbus.Error) {
	f.notify.Store([]any{appName, replaces, icon, summary, body, actions, len(hints), expire})
	f.next++
	return f.next, nil
}

func (f *fakeNotifyd) CloseNotification(id uint32) *dbus.Error {
	f.closed.Update(func(ids []uint32) []uint32 { return append(ids, id) })
	return nil
}

func serveNotifyd(t *testing.T, r *rig) *fakeNotifyd {
	t.Helper()
	f := &fakeNotifyd{conn: r.bus.Conn(t), next: 40}
	if err := f.conn.Export(f, notifications.ObjPath, notifications.Interface); err != nil {
		t.Fatal(err)
	}
	if _, err := f.conn.RequestName(notifications.Interface, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	return f
}

// gicon is a serialized GIcon, (sv).
type gicon struct {
	Kind string
	Data dbus.Variant
}

const notificationAdd = NotificationIface + ".AddNotification"

func TestNotificationShowsAndForwardsActions(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	n := Vardict{
		"title":          dbus.MakeVariant("Hello"),
		"body":           dbus.MakeVariant("World"),
		"icon":           dbus.MakeVariant(gicon{"themed", dbus.MakeVariant([]string{"mail-unread", "mail-unread-symbolic"})}),
		"default-action": dbus.MakeVariant("open"),
		"buttons": dbus.MakeVariant([]map[string]dbus.Variant{
			{"label": dbus.MakeVariant("Reply"), "action": dbus.MakeVariant("reply")},
			{"label": dbus.MakeVariant("No action")},
		}),
	}
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", n).Err; err != nil {
		t.Fatal(err)
	}
	want := []any{"org.app", uint32(0), "mail-unread", "Hello", "World", []string{"open", "Default", "reply", "Reply"}, 0, int32(-1)}
	got := d.notify.Load()
	if len(got) != len(want) || !slices.Equal(got[5].([]string), want[5].([]string)) {
		t.Fatalf("Notify = %v, want %v", got, want)
	}
	for i := range want {
		if i != 5 && got[i] != want[i] {
			t.Errorf("Notify arg %d = %v, want %v", i, got[i], want[i])
		}
	}

	// The daemon's press on our notification reaches the app; one on a
	// notification the portal did not create does not.
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", uint32(99), "x")
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", uint32(41), "reply")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name != NotificationIface+".ActionInvoked" {
				continue
			}
			if s.Body[0] != "org.app" || s.Body[1] != "n1" || s.Body[2] != "reply" {
				t.Fatalf("ActionInvoked = %v", s.Body)
			}
		case <-deadline:
			t.Fatal("no ActionInvoked forwarded")
		}
		break
	}

	// Remove closes the daemon's notification once; an unknown one is
	// left alone.
	for range 2 {
		if err := r.obj.Call(NotificationIface+".RemoveNotification", 0, "org.app", "n1").Err; err != nil {
			t.Fatal(err)
		}
	}
	_ = r.obj.Call(NotificationIface+".RemoveNotification", 0, "org.other", "n1").Err
	if got := d.closed.Load(); !slices.Equal(got, []uint32{41}) {
		t.Errorf("closed = %v, want [41]", got)
	}
}

func TestNotificationDefaults(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", Vardict{"icon": dbus.MakeVariant(gicon{"file", dbus.MakeVariant("/x.png")})}).Err; err != nil {
		t.Fatal(err)
	}
	if got := d.notify.Load(); got[3] != "org.app" || got[4] != "" || got[2] != "" || len(got[5].([]string)) != 0 {
		t.Errorf("Notify = %v: the app id titles it, no body, no icon, no actions", got)
	}
	if got := notificationIcon(Vardict{"icon": dbus.MakeVariant("dialog-information")}); got != "dialog-information" {
		t.Errorf("bare icon = %q", got)
	}
}

// fakeWallpaperd is the shell's dev.stubbe.lys.Wallpaper1.
type fakeWallpaperd struct{ set dbustest.Var[[]string] }

func (f *fakeWallpaperd) SetWallpaper(path, monitor string) *dbus.Error {
	f.set.Store([]string{path, monitor})
	return nil
}

// fakeDialogs answers every dialog with yes or no.
type fakeDialogs struct {
	yes   dbustest.Var[bool]
	asked dbustest.Var[[]any]
}

func (f *fakeDialogs) Access(r portaldialogs.AccessRequest) bool {
	f.asked.Store([]any{r})
	return f.yes.Load()
}

func (f *fakeDialogs) Account(reason string) bool { f.asked.Store([]any{reason}); return f.yes.Load() }

func (f *fakeDialogs) ConfirmWallpaper(uri string) bool {
	f.asked.Store([]any{uri})
	return f.yes.Load()
}

func (f *fakeDialogs) ChooseApplication(choices []string, contentType, uri string) string {
	f.asked.Store([]any{choices, contentType, uri})
	if f.yes.Load() {
		return "org.chosen.desktop"
	}
	return ""
}

func (f *fakeDialogs) ConfirmInstall(name, icon string) bool {
	f.asked.Store([]any{name, icon})
	return f.yes.Load()
}

func serveDialogs(t *testing.T, r *rig, yes bool) *fakeDialogs {
	t.Helper()
	f := &fakeDialogs{}
	f.yes.Store(yes)
	release, err := portaldialogs.NewDaemon(f).Export(r.bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return f
}

func serveWallpaperd(t *testing.T, r *rig) *fakeWallpaperd {
	t.Helper()
	f := &fakeWallpaperd{}
	conn := r.bus.Conn(t)
	if err := conn.Export(f, wallpaper.ServicePath, wallpaper.Interface); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(wallpaper.ServiceName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	return f
}

func (r *rig) setWallpaper(t *testing.T, uri string, options Vardict) uint32 {
	t.Helper()
	var code uint32
	if err := r.obj.Call("org.freedesktop.impl.portal.Wallpaper.SetWallpaperURI", 0, handle, "org.app", "", uri, options).Store(&code); err != nil {
		t.Fatal(err)
	}
	return code
}

func TestWallpaperSetsAFileURI(t *testing.T) {
	r := newRig(t, nil)
	w := serveWallpaperd(t, r)
	if code := r.setWallpaper(t, "file:///home/u/My%20Walls/a%23b.png", Vardict{}); code != ResponseSuccess {
		t.Errorf("code = %d", code)
	}
	if got := w.set.Load(); !slices.Equal(got, []string{"/home/u/My Walls/a#b.png", ""}) {
		t.Errorf("SetWallpaper = %q: the decoded path on every monitor", got)
	}
	if code := r.setWallpaper(t, "https://example.com/x.png", Vardict{}); code != ResponseOther {
		t.Errorf("https code = %d", code)
	}
}

func TestWallpaperPreview(t *testing.T) {
	r := newRig(t, nil)
	w := serveWallpaperd(t, r)
	preview := Vardict{"show-preview": dbus.MakeVariant(true)}
	// No dialog host: the request fails rather than applying unasked.
	if code := r.setWallpaper(t, "file:///a.png", preview); code != ResponseOther || w.set.Load() != nil {
		t.Errorf("no host: code %d, set %v", code, w.set.Load())
	}
	d := serveDialogs(t, r, false)
	if code := r.setWallpaper(t, "file:///a.png", preview); code != ResponseCancelled || w.set.Load() != nil {
		t.Errorf("declined: code %d, set %v", code, w.set.Load())
	}
	if got := d.asked.Load(); got[0] != "file:///a.png" {
		t.Errorf("previewed %v", got)
	}
	d.yes.Store(true)
	if code := r.setWallpaper(t, "file:///a.png", preview); code != ResponseSuccess || w.set.Load()[0] != "/a.png" {
		t.Errorf("accepted: code %d, set %v", code, w.set.Load())
	}
}

func TestWallpaperFailsWithoutTheShell(t *testing.T) {
	r := newRig(t, nil)
	if code := r.setWallpaper(t, "file:///a.png", Vardict{}); code != ResponseOther {
		t.Errorf("code = %d", code)
	}
}

// fakeScreensaver is a screensaver whose state the test sets and whose
// watch it can fire.
type fakeScreensaver struct {
	active  dbustest.Var[bool]
	watcher dbustest.Var[func(bool)]
	stops   dbustest.Var[int]
}

func (f *fakeScreensaver) screensaverActive() (bool, error) { return f.active.Load(), nil }

func (f *fakeScreensaver) watchScreensaver(fn func(bool)) (func(), error) {
	f.watcher.Store(fn)
	return func() { f.stops.Update(func(n int) int { return n + 1 }) }, nil
}

// inhibitRig is a rig whose logind locks and shell idle holds are
// recorded fakes; lockErr and idleErr make them fail.
type inhibitRig struct {
	*rig
	asked    dbustest.Var[[]string]
	held     dbustest.Var[*os.File]
	idleHeld dbustest.Var[int]
	lockErr  dbustest.Var[error]
	idleErr  dbustest.Var[error]
	saver    *fakeScreensaver
}

func newInhibitRig(t *testing.T) *inhibitRig {
	t.Helper()
	ir := &inhibitRig{saver: &fakeScreensaver{}}
	ir.rig = newRig(t, func(b *Backend) {
		b.inhibitor.lock = func(what, why string) (*os.File, error) {
			ir.asked.Update(func(w []string) []string { return append(w, what+" "+why) })
			if err := ir.lockErr.Load(); err != nil {
				return nil, err
			}
			pr, pw, err := os.Pipe()
			ir.held.Store(pr)
			return pw, err
		}
		b.inhibitor.holdIdle = func(app, reason string) (func(), error) {
			if err := ir.idleErr.Load(); err != nil {
				return nil, err
			}
			ir.idleHeld.Update(func(n int) int { return n + 1 })
			return func() { ir.idleHeld.Update(func(n int) int { return n - 1 }) }, nil
		}
		b.inhibitor.screensaver = ir.saver
	})
	return ir
}

func (ir *inhibitRig) inhibit(flags uint32, options Vardict) error {
	return ir.obj.Call(InhibitIface+".Inhibit", 0, handle, "org.app", "", flags, options).Err
}

func TestInhibitHoldsTheLockUntilClose(t *testing.T) {
	r := newInhibitRig(t)
	if err := r.inhibit(inhibitIdle|inhibitSuspend|inhibitLogout, Vardict{"reason": dbus.MakeVariant("Playing music")}); err != nil {
		t.Fatal(err)
	}
	if got := r.asked.Load(); !slices.Equal(got, []string{"idle:sleep:shutdown Playing music"}) {
		t.Fatalf("logind asked for %v, want the lock with the app's reason", got)
	}
	if r.idleHeld.Load() != 1 {
		t.Error("idle was not held on the shell's inhibitor")
	}
	req := r.client.Object(BusName, handle)
	readDone := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(r.held.Load())
		readDone <- err
	}()
	select {
	case <-readDone:
		t.Fatal("the lock was released before Close")
	case <-time.After(50 * time.Millisecond):
	}
	if err := req.Call(RequestIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("Close kept the lock")
	}
	if r.idleHeld.Load() != 0 {
		t.Error("Close kept the shell idle hold")
	}
	if err := req.Call(RequestIface+".Close", 0).Err; err == nil {
		t.Error("the Request outlived its lock")
	}
}

// Without a reason logind still gets one; suspend alone takes no idle
// hold.
func TestInhibitDefaultsTheReason(t *testing.T) {
	r := newInhibitRig(t)
	if err := r.inhibit(inhibitSuspend, Vardict{}); err != nil {
		t.Fatal(err)
	}
	if got := r.asked.Load(); !slices.Equal(got, []string{"sleep Application requested"}) || r.idleHeld.Load() != 0 {
		t.Errorf("asked %v, idle holds %d", got, r.idleHeld.Load())
	}
}

// User switching has nothing to inhibit it: asked alone the call fails;
// beside idle the idle part still holds.
func TestInhibitUserSwitchIsRefused(t *testing.T) {
	r := newInhibitRig(t)
	if err := r.inhibit(inhibitUserSwitch, Vardict{}); err == nil {
		t.Error("a user-switch inhibition succeeded")
	}
	if got := r.asked.Load(); len(got) != 0 {
		t.Errorf("logind asked %v for a user switch", got)
	}
	req := r.client.Object(BusName, handle)
	if err := req.Call(RequestIface+".Close", 0).Err; err != nil {
		t.Errorf("Close after a refused inhibition: %v", err)
	}
	if err := r.inhibit(inhibitUserSwitch|inhibitIdle, Vardict{}); err != nil {
		t.Errorf("idle beside a user switch: %v", err)
	}
	if r.idleHeld.Load() != 1 {
		t.Error("the idle part was not held")
	}
}

// When logind and the shell both refuse, the call fails, but the
// Request is there for the frontend's Close.
func TestInhibitFailureStillExportsTheRequest(t *testing.T) {
	r := newInhibitRig(t)
	r.lockErr.Store(errors.New("no logind"))
	r.idleErr.Store(errors.New("no shell"))
	if err := r.inhibit(inhibitIdle, Vardict{}); err == nil {
		t.Error("an inhibition that holds nothing succeeded")
	}
	if err := r.client.Object(BusName, handle).Call(RequestIface+".Close", 0).Err; err != nil {
		t.Errorf("Close after a failed lock: %v", err)
	}
	// The shell alone still holds idle.
	r.idleErr.Store(nil)
	if err := r.inhibit(inhibitIdle, Vardict{}); err != nil || r.idleHeld.Load() != 1 {
		t.Errorf("logind down, shell up: %v, %d holds", err, r.idleHeld.Load())
	}
}

// A monitor gets the current state right after it opens and again when
// the screen locks; a closed monitor gets nothing and the watch stops.
func TestInhibitMonitorFollowsTheLockScreen(t *testing.T) {
	r := newInhibitRig(t)
	if err := r.client.AddMatchSignal(dbus.WithMatchInterface(InhibitIface)); err != nil {
		t.Fatal(err)
	}
	next := func() (dbus.ObjectPath, Vardict) {
		t.Helper()
		timeout := time.After(2 * time.Second)
		for {
			select {
			case s := <-r.signals:
				if s.Name == InhibitIface+".StateChanged" {
					return s.Body[0].(dbus.ObjectPath), s.Body[1].(map[string]dbus.Variant)
				}
			case <-timeout:
				t.Fatal("no StateChanged")
			}
		}
	}
	var code uint32
	if err := r.obj.Call(InhibitIface+".CreateMonitor", 0, handle, sessA, "org.app", "").Store(&code); err != nil || code != ResponseSuccess {
		t.Fatalf("CreateMonitor = %d, %v", code, err)
	}
	session, state := next()
	if session != sessA || state["screensaver-active"].Value() != false || state["session-state"].Value() != uint32(1) {
		t.Errorf("initial state %s %v", session, state)
	}
	r.saver.watcher.Load()(true)
	if _, state := next(); state["screensaver-active"].Value() != true {
		t.Errorf("after locking: %v", state)
	}
	if err := r.obj.Call(InhibitIface+".QueryEndResponse", 0, sessA).Err; err != nil {
		t.Errorf("QueryEndResponse: %v", err)
	}
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if r.saver.stops.Load() != 1 {
		t.Error("the screensaver watch outlived the last monitor")
	}
	r.saver.watcher.Load()(false)
	select {
	case s := <-r.signals:
		if s.Name == InhibitIface+".StateChanged" {
			t.Errorf("a closed monitor got %v", s.Body)
		}
	case <-time.After(150 * time.Millisecond):
	}
}
