package portal

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/wallpaper"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// notifyCall is one Notify the fake daemon received.
type notifyCall struct {
	appName       string
	replaces      uint32
	icon, summary string
	body          string
	actions       []string
	hints         map[string]dbus.Variant
	expire        int32
}

// fakeNotifyd is the shell's notification daemon; fail makes Notify
// refuse.
type fakeNotifyd struct {
	conn   *dbus.Conn
	next   uint32
	calls  dbustest.Var[[]notifyCall]
	closed dbustest.Var[[]uint32]
	fail   dbustest.Var[bool]
}

func (f *fakeNotifyd) Notify(appName string, replaces uint32, icon, summary, body string, actions []string, hints map[string]dbus.Variant, expire int32) (uint32, *dbus.Error) {
	if f.fail.Load() {
		return 0, dbus.MakeFailedError(errors.New("refused"))
	}
	f.calls.Update(func(c []notifyCall) []notifyCall {
		return append(c, notifyCall{appName, replaces, icon, summary, body, actions, hints, expire})
	})
	if replaces != 0 {
		return replaces, nil
	}
	f.next++
	return f.next, nil
}

func (f *fakeNotifyd) CloseNotification(id uint32) *dbus.Error {
	f.closed.Update(func(ids []uint32) []uint32 { return append(ids, id) })
	return nil
}

func (f *fakeNotifyd) last(t *testing.T) notifyCall {
	t.Helper()
	calls := f.calls.Load()
	if len(calls) == 0 {
		t.Fatal("no Notify")
	}
	return calls[len(calls)-1]
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

// nextActionInvoked waits for the portal's ActionInvoked.
func nextActionInvoked(t *testing.T, r *rig) []any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name == NotificationIface+".ActionInvoked" {
				return s.Body
			}
		case <-deadline:
			t.Fatal("no ActionInvoked forwarded")
		}
	}
}

func TestNotificationShowsAndForwardsActions(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	n := Vardict{
		"title":                 dbus.MakeVariant("Hello"),
		"body":                  dbus.MakeVariant("World <3"),
		"icon":                  dbus.MakeVariant(gicon{"themed", dbus.MakeVariant([]string{"mail-unread", "mail-unread-symbolic"})}),
		"default-action":        dbus.MakeVariant("app.open"),
		"default-action-target": dbus.MakeVariant(dbus.MakeVariant("inbox")),
		"buttons": dbus.MakeVariant([]map[string]dbus.Variant{
			{"label": dbus.MakeVariant("Reply"), "action": dbus.MakeVariant("app.reply"), "target": dbus.MakeVariant(dbus.MakeVariant(uint32(7)))},
			{"label": dbus.MakeVariant("No action")},
			{"action": dbus.MakeVariant("app.alert"), "purpose": dbus.MakeVariant("system.custom-alert")},
		}),
	}
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", n).Err; err != nil {
		t.Fatal(err)
	}
	got := d.last(t)
	if got.appName != "org.app" || got.replaces != 0 || got.icon != "mail-unread" || got.summary != "Hello" || got.body != "World &lt;3" || got.expire != -1 {
		t.Errorf("Notify = %+v", got)
	}
	// The default action is the daemon's body-click key with no button
	// of its own; a button needs an action and a label.
	if want := []string{"default", "", "button-0", "Reply"}; !slices.Equal(got.actions, want) {
		t.Errorf("actions = %q, want %q", got.actions, want)
	}
	if got.hints["desktop-entry"].Value() != "org.app" {
		t.Errorf("hints = %v, want the app for grouping", got.hints)
	}

	// A body click reaches the app as its default action and target; a
	// press on a notification the portal did not create does not.
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", uint32(99), "default")
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", uint32(41), "default")
	body := nextActionInvoked(t, r)
	if body[0] != "org.app" || body[1] != "n1" || body[2] != "app.open" {
		t.Fatalf("ActionInvoked = %v", body)
	}
	if params := body[3].([]dbus.Variant); len(params) != 1 || params[0].Value() != "inbox" {
		t.Errorf("default parameter = %v, want the target", params)
	}
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".ActionInvoked", uint32(41), "button-0")
	body = nextActionInvoked(t, r)
	if body[2] != "app.reply" || body[3].([]dbus.Variant)[0].Value() != uint32(7) {
		t.Errorf("button ActionInvoked = %v", body)
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

// Reusing an id updates the notification in place; show-as-new replaces
// it with a fresh one.
func TestNotificationReplacesByID(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	for _, title := range []string{"one", "two"} {
		if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", Vardict{"title": dbus.MakeVariant(title)}).Err; err != nil {
			t.Fatal(err)
		}
	}
	if got := d.last(t); got.replaces != 41 || got.summary != "two" {
		t.Errorf("second Notify = %+v, want it to replace 41", got)
	}
	again := Vardict{"title": dbus.MakeVariant("three"), "display-hint": dbus.MakeVariant([]string{"show-as-new"})}
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", again).Err; err != nil {
		t.Fatal(err)
	}
	if got := d.last(t); got.replaces != 0 || !slices.Equal(d.closed.Load(), []uint32{41}) {
		t.Errorf("show-as-new: Notify %+v, closed %v", got, d.closed.Load())
	}
	if err := r.obj.Call(NotificationIface+".RemoveNotification", 0, "org.app", "n1").Err; err != nil {
		t.Fatal(err)
	}
	if got := d.closed.Load(); !slices.Equal(got, []uint32{41, 42}) {
		t.Errorf("closed = %v, want the fresh one closed by Remove", got)
	}
}

// A notification the daemon closes is forgotten: removing it later
// asks the daemon nothing.
func TestNotificationClosedByTheDaemonIsForgotten(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	_ = d.conn.Emit(notifications.ObjPath, notifications.Interface+".NotificationClosed", uint32(41), uint32(2))
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.backend.notifier.mu.Lock()
		n := len(r.backend.notifier.byDaemon)
		r.backend.notifier.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed notification stayed tracked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := r.obj.Call(NotificationIface+".RemoveNotification", 0, "org.app", "n1").Err; err != nil {
		t.Fatal(err)
	}
	if got := d.closed.Load(); len(got) != 0 {
		t.Errorf("closed %v after the daemon closed it", got)
	}
}

// The v2 options reach the daemon as hints.
func TestNotificationOptionsBecomeHints(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	n := Vardict{
		"priority":     dbus.MakeVariant("urgent"),
		"category":     dbus.MakeVariant("im.received"),
		"display-hint": dbus.MakeVariant([]string{"transient", "persistent"}),
		"sound":        dbus.MakeVariant("silent"),
		"markup-body":  dbus.MakeVariant("<b>bold</b>"),
		"body":         dbus.MakeVariant("plain"),
	}
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", n).Err; err != nil {
		t.Fatal(err)
	}
	got := d.last(t)
	for k, want := range map[string]any{"urgency": byte(2), "category": "im.received", "transient": true, "resident": true, "suppress-sound": true} {
		if got.hints[k].Value() != want {
			t.Errorf("hint %s = %v, want %v", k, got.hints[k].Value(), want)
		}
	}
	if got.body != "<b>bold</b>" || got.expire != 0 {
		t.Errorf("body %q expire %d, want the markup body and no expiry", got.body, got.expire)
	}
	for priority, want := range map[string]byte{"low": 0, "normal": 1, "high": 1, "urgent": 2} {
		if got := priorityUrgency(priority); got != want {
			t.Errorf("priority %s = %d, want %d", priority, got, want)
		}
	}
}

// A file-descriptor icon (what the frontend sends since version 2) and
// a bytes icon reach the daemon as image-data.
func TestNotificationFileIconBecomesImageData(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	icons := []dbus.Variant{
		dbus.MakeVariant(gicon{"file-descriptor", dbus.MakeVariant(dbus.UnixFD(f.Fd()))}),
		dbus.MakeVariant(gicon{"bytes", dbus.MakeVariant(buf.Bytes())}),
	}
	for _, icon := range icons {
		if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", Vardict{"icon": icon}).Err; err != nil {
			t.Fatal(err)
		}
		fields, ok := d.last(t).hints["image-data"].Value().([]any)
		if !ok || len(fields) != 7 || fields[0] != int32(3) || fields[1] != int32(2) || fields[5] != int32(4) {
			t.Fatalf("image-data = %v", d.last(t).hints["image-data"])
		}
		if px := fields[6].([]byte); px[0] != 255 || px[3] != 255 {
			t.Errorf("first pixel = %v, want opaque red", px[:4])
		}
	}
}

func TestNotificationDefaultsAndFailures(t *testing.T) {
	r := newRig(t, nil)
	d := serveNotifyd(t, r)
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n1", Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	if got := d.last(t); got.summary != "org.app" || got.body != "" || got.icon != "" || len(got.actions) != 0 {
		t.Errorf("Notify = %+v: the app id titles it, no body, no icon, no actions", got)
	}
	if got := notificationIcon(Vardict{"icon": dbus.MakeVariant("dialog-information")}); got != "dialog-information" {
		t.Errorf("bare icon = %q", got)
	}
	d.fail.Store(true)
	if err := r.obj.Call(notificationAdd, 0, "org.app", "n2", Vardict{}).Err; err == nil {
		t.Error("a notification the daemon refused reported success")
	}
	v, err := r.obj.GetProperty(NotificationIface + ".SupportedOptions")
	if err != nil {
		t.Fatal(err)
	}
	opts, _ := v.Value().(map[string]dbus.Variant)
	if _, ok := opts["category"]; !ok {
		t.Errorf("SupportedOptions = %v", opts)
	}
	if v, _ := r.obj.GetProperty(NotificationIface + ".version"); v.Value() != uint32(2) {
		t.Errorf("version = %v", v)
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
	gate
	yes   dbustest.Var[bool]
	asked dbustest.Var[[]any]
}

// answer records a dialog and gives the configured verdict, no when
// it was dismissed.
func (f *fakeDialogs) answer(c portalreq.Caller, args ...any) bool {
	f.asked.Store(args)
	return !f.enter(c) && f.yes.Load()
}

func (f *fakeDialogs) Access(c portalreq.Caller, r portaldialogs.AccessRequest) (bool, []portalreq.Selection) {
	if !f.answer(c, r) {
		return false, nil
	}
	picked := make([]portalreq.Selection, 0, len(r.Choices))
	for _, ch := range r.Choices {
		picked = append(picked, portalreq.Selection{ID: ch.ID, Value: ch.Start()})
	}
	return true, picked
}

func (f *fakeDialogs) Account(c portalreq.Caller, reason string) bool { return f.answer(c, reason) }

func (f *fakeDialogs) ConfirmWallpaper(c portalreq.Caller, uri string) bool {
	return f.answer(c, uri)
}

func (f *fakeDialogs) ChooseApplication(c portalreq.Caller, choices []string, contentType, uri string) string {
	if f.answer(c, choices, contentType, uri) {
		return "org.chosen.desktop"
	}
	return ""
}

func (f *fakeDialogs) ConfirmInstall(c portalreq.Caller, name, icon string) bool {
	return f.answer(c, name, icon)
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
