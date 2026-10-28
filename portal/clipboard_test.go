package portal

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// fakeClip is the data-control device: the selection it holds, and the
// claims made on it.
type fakeClip struct {
	changed dbustest.Var[func(selectionChange)]
	starts  dbustest.Var[int]
	mimes   dbustest.Var[[]string]
	pass    dbustest.Var[func(string, *os.File)]
	cleared dbustest.Var[int]
	data    string
}

func (f *fakeClip) own(mimes []string, pass func(string, *os.File)) error {
	f.mimes.Store(mimes)
	f.pass.Store(pass)
	return nil
}

func (f *fakeClip) clear() error {
	f.cleared.Update(func(n int) int { return n + 1 })
	return nil
}

func (f *fakeClip) read(mime string) (*os.File, error) {
	if mime != "text/plain" {
		return nil, errors.New("not offered")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	go func() {
		_, _ = w.WriteString(f.data)
		_ = w.Close()
	}()
	return r, nil
}

// clipRig has two RemoteDesktop sessions, not started, a consenting
// shell, and virtual input.
func clipRig(t *testing.T, startErr error) (*rig, *fakeClip) {
	t.Helper()
	f := &fakeClip{data: "copied"}
	r := newRig(t, func(b *Backend) {
		b.clipboard = newClipboardBridge(b.conn, b.sessions, func(changed func(selectionChange)) (clipDevice, error) {
			f.starts.Update(func(n int) int { return n + 1 })
			if startErr != nil {
				return nil, startErr
			}
			f.changed.Store(changed)
			return f, nil
		})
		b.remote = newRemoteDesktop(b.conn, b.sessions, b.screenCast, func() (inputSink, error) {
			return &fakeSink{keymap: []byte(testKeymap)}, nil
		})
	})
	serveDialogs(t, r, true)
	for _, sess := range []dbus.ObjectPath{sessA, sessB} {
		if code, _ := r.interactive(t, RemoteDesktopIface+".CreateSession", handle, sess, "org.app", Vardict{}); code != ResponseSuccess {
			t.Fatalf("CreateSession %s = %d", sess, code)
		}
	}
	return r, f
}

// enableClipboard requests the clipboard for sess and starts it.
func (r *rig) enableClipboard(t *testing.T, sess dbus.ObjectPath) {
	t.Helper()
	if err := r.obj.Call(ClipboardIface+".RequestClipboard", 0, sess, Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	if code, _ := r.interactive(t, RemoteDesktopIface+".Start", handle, sess, "org.app", "", Vardict{}); code != ResponseSuccess {
		t.Fatalf("Start %s = %d", sess, code)
	}
}

// ownerChanged reads a SelectionOwnerChanged: its session, mime types
// and session_is_owner.
func ownerChanged(t *testing.T, s *dbus.Signal) (dbus.ObjectPath, []string, bool) {
	t.Helper()
	options, _ := s.Body[1].(map[string]dbus.Variant)
	mimes, okM := options["mime_types"].Value().([]string)
	isOwner, okO := options["session_is_owner"].Value().(bool)
	if !okM || !okO {
		t.Fatalf("SelectionOwnerChanged options %v lack mime_types or session_is_owner", options)
	}
	return s.Body[0].(dbus.ObjectPath), mimes, isOwner
}

// clipSignal waits for the next Clipboard signal.
func (r *rig) clipSignal(t *testing.T, member string) *dbus.Signal {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name == ClipboardIface+"."+member {
				return s
			}
		case <-deadline:
			t.Fatalf("no %s", member)
			return nil
		}
	}
}

func (r *rig) noClipSignal(t *testing.T) {
	t.Helper()
	timeout := time.After(200 * time.Millisecond)
	for {
		select {
		case s := <-r.signals:
			if len(s.Name) > len(ClipboardIface) && s.Name[:len(ClipboardIface)] == ClipboardIface {
				t.Fatalf("unexpected %s %v", s.Name, s.Body)
			}
		case <-timeout:
			return
		}
	}
}

const (
	sessA = dbus.ObjectPath("/org/freedesktop/portal/desktop/session/1_2/a")
	sessB = dbus.ObjectPath("/org/freedesktop/portal/desktop/session/1_2/b")
	sessC = dbus.ObjectPath("/org/freedesktop/portal/desktop/session/1_2/c")
)

// A session must request the clipboard before it starts and use it only
// after; the device starts once, and an enabled session reads the
// selection and hears of every new one with its mime types and that
// another client owns it.
func TestClipboardReadsAndFollowsTheSelection(t *testing.T) {
	r, f := clipRig(t, nil)
	if err := r.obj.Call(ClipboardIface+".SelectionRead", 0, sessA, "text/plain").Err; err == nil {
		t.Error("read without RequestClipboard")
	}
	for range 2 {
		if err := r.obj.Call(ClipboardIface+".RequestClipboard", 0, sessA, Vardict{}).Err; err != nil {
			t.Fatal(err)
		}
	}
	if f.starts.Load() != 1 {
		t.Errorf("device started %d times", f.starts.Load())
	}
	if err := r.obj.Call(ClipboardIface+".SelectionRead", 0, sessA, "text/plain").Err; err == nil {
		t.Error("a session not started read the clipboard")
	}
	if code, _ := r.interactive(t, RemoteDesktopIface+".Start", handle, sessA, "org.app", "", Vardict{}); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if err := r.obj.Call(ClipboardIface+".RequestClipboard", 0, sessA, Vardict{}).Err; err == nil {
		t.Error("RequestClipboard after Start succeeded")
	}
	var fd dbus.UnixFD
	if err := r.obj.Call(ClipboardIface+".SelectionRead", 0, sessA, "text/plain").Store(&fd); err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(os.NewFile(uintptr(fd), "read"))
	if string(got) != "copied" {
		t.Errorf("read %q", got)
	}
	if err := r.obj.Call(ClipboardIface+".SelectionRead", 0, sessA, "image/png").Err; err == nil {
		t.Error("an unoffered mime read")
	}

	f.changed.Load()(selectionChange{mimes: []string{"text/plain", "image/png"}})
	sess, mimes, isOwner := ownerChanged(t, r.clipSignal(t, "SelectionOwnerChanged"))
	if sess != sessA || !slices.Equal(mimes, []string{"text/plain", "image/png"}) || isOwner {
		t.Errorf("owner changed for %s: %v, owner %v", sess, mimes, isOwner)
	}
	r.noClipSignal(t) // only the enabled session hears it
}

// The session's selection is ours to serve: its echo tells it it is the
// owner (and the others that they are not), and a receiver's request
// reaches it alone, under a serial only it may answer.
func TestClipboardServesTheSessionsSelection(t *testing.T) {
	r, f := clipRig(t, nil)
	r.enableClipboard(t, sessA)
	r.enableClipboard(t, sessB)
	if err := r.obj.Call(ClipboardIface+".SetSelection", 0, sessA, Vardict{"mime_types": dbus.MakeVariant([]string{"text/plain", "text/uri-list"})}).Err; err != nil {
		t.Fatal(err)
	}
	if got := f.mimes.Load(); !slices.Equal(got, []string{"text/plain", "text/uri-list"}) {
		t.Errorf("claimed %v", got)
	}
	if err := r.obj.Call(ClipboardIface+".SetSelection", 0, sessA, Vardict{}).Err; err == nil {
		t.Error("SetSelection without mime_types succeeded")
	}
	f.changed.Load()(selectionChange{mimes: []string{"text/plain", "text/uri-list"}, own: true})
	owners := map[dbus.ObjectPath]bool{}
	for range 2 {
		sess, _, isOwner := ownerChanged(t, r.clipSignal(t, "SelectionOwnerChanged"))
		owners[sess] = isOwner
	}
	if !owners[sessA] || owners[sessB] || len(owners) != 2 {
		t.Errorf("session_is_owner = %v, want only %s", owners, sessA)
	}

	// A receiver asks: only the owner is told, takes the pipe and fills it.
	recv, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer recv.Close()
	f.pass.Load()("text/uri-list", pipe)
	s := r.clipSignal(t, "SelectionTransfer")
	if s.Body[0] != sessA || s.Body[1] != "text/uri-list" || s.Body[2] != uint32(1) {
		t.Fatalf("transfer = %v", s.Body)
	}
	r.noClipSignal(t)
	if err := r.obj.Call(ClipboardIface+".SelectionWrite", 0, sessB, uint32(1)).Err; err == nil {
		t.Error("another session took the owner's transfer")
	}
	var fd dbus.UnixFD
	if err := r.obj.Call(ClipboardIface+".SelectionWrite", 0, sessA, uint32(1)).Store(&fd); err != nil {
		t.Fatal(err)
	}
	w := os.NewFile(uintptr(fd), "write")
	_, _ = w.WriteString("file:///x")
	_ = w.Close()
	if err := r.obj.Call(ClipboardIface+".SelectionWriteDone", 0, sessA, uint32(1), true).Err; err != nil {
		t.Error(err)
	}
	_ = recv.SetReadDeadline(time.Now().Add(2 * time.Second))
	if got, err := io.ReadAll(recv); err != nil || string(got) != "file:///x" {
		t.Errorf("receiver got %q, %v: the backend kept its copy open or lost the data", got, err)
	}
	// Bounded: a pipe handed out twice is already closed, and its reply
	// never arrives.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var de dbus.Error
	if err := r.obj.CallWithContext(ctx, ClipboardIface+".SelectionWrite", 0, sessA, uint32(1)).Err; !errors.As(err, &de) || de.Name != dbusx.ErrFailed {
		t.Errorf("a second SelectionWrite = %v, want the no-pending-transfer error", err)
	}

	// A transfer nobody took is closed when another client's selection
	// replaces ours.
	recv2, pipe2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer recv2.Close()
	f.pass.Load()("text/uri-list", pipe2)
	if s := r.clipSignal(t, "SelectionTransfer"); s.Body[2] != uint32(2) {
		t.Errorf("serial = %v", s.Body[2])
	}
	f.changed.Load()(selectionChange{mimes: []string{"text/plain"}})
	_ = recv2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if got, err := io.ReadAll(recv2); err != nil || len(got) != 0 {
		t.Errorf("an abandoned transfer stayed open: %q, %v", got, err)
	}
}

// The owner session closing gives up its claim, and its transfers end;
// another session closing leaves the claim.
func TestClipboardOwnerClosing(t *testing.T) {
	r, f := clipRig(t, nil)
	r.enableClipboard(t, sessA)
	r.enableClipboard(t, sessB)
	_ = r.obj.Call(ClipboardIface+".SetSelection", 0, sessA, Vardict{"mime_types": dbus.MakeVariant([]string{"text/plain"})}).Err
	if err := r.client.Object(BusName, sessB).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if f.cleared.Load() != 0 {
		t.Error("another session closing cleared the claim")
	}
	recv, pipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer recv.Close()
	f.pass.Load()("text/plain", pipe)
	r.clipSignal(t, "SelectionTransfer")
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if f.cleared.Load() != 1 {
		t.Errorf("the owner closing cleared %d times", f.cleared.Load())
	}
	_ = recv.SetReadDeadline(time.Now().Add(2 * time.Second))
	if got, err := io.ReadAll(recv); err != nil || len(got) != 0 {
		t.Errorf("the closed owner's transfer stayed open: %q, %v", got, err)
	}
	// A request after the owner is gone ends at once.
	recv2, pipe2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer recv2.Close()
	f.pass.Load()("text/plain", pipe2)
	_ = recv2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if got, err := io.ReadAll(recv2); err != nil || len(got) != 0 {
		t.Errorf("a request with no owner stayed open: %q, %v", got, err)
	}
}

// The clipboard is a RemoteDesktop session's, and only while it lives:
// another kind of session, or a closed one, cannot use it.
func TestClipboardBelongsToLiveRemoteSessions(t *testing.T) {
	r, _ := clipRig(t, nil)
	if code, _ := r.interactive(t, ScreenCastIface+".CreateSession", handle, sessC, "org.app", Vardict{}); code != ResponseSuccess {
		t.Fatal("ScreenCast CreateSession failed")
	}
	if err := r.obj.Call(ClipboardIface+".RequestClipboard", 0, sessC, Vardict{}).Err; err == nil {
		t.Error("a ScreenCast session enabled the clipboard")
	}
	r.enableClipboard(t, sessA)
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	for _, call := range [][]any{
		{ClipboardIface + ".SelectionRead", sessA, "text/plain"},
		{ClipboardIface + ".SetSelection", sessA, Vardict{"mime_types": dbus.MakeVariant([]string{"text/plain"})}},
		{ClipboardIface + ".SelectionWriteDone", sessA, uint32(1), true},
	} {
		if err := r.obj.Call(call[0].(string), 0, call[1:]...).Err; err == nil {
			t.Errorf("a closed session's %s succeeded", call[0])
		}
	}
	if got := r.backend.clipboard.enabledSessions(); len(got) != 0 {
		t.Errorf("enabled sessions after close: %v", got)
	}
}

// Without data-control RequestClipboard fails, so the app can tell, and
// is tried again on the next request.
func TestClipboardWithoutDataControl(t *testing.T) {
	r, f := clipRig(t, errors.New("no data-control"))
	for _, sess := range []dbus.ObjectPath{sessA, sessB} {
		if err := r.obj.Call(ClipboardIface+".RequestClipboard", 0, sess, Vardict{}).Err; err == nil {
			t.Errorf("RequestClipboard for %s succeeded without data-control", sess)
		}
	}
	if f.starts.Load() != 2 {
		t.Errorf("a failed start is not retried: %d", f.starts.Load())
	}
	if err := r.obj.Call(ClipboardIface+".SetSelection", 0, sessA, Vardict{"mime_types": dbus.MakeVariant([]string{"text/plain"})}).Err; err == nil {
		t.Error("a session without the clipboard set the selection")
	}
}

// When the compositor connection ends the device goes with it: sessions
// must request the clipboard again, which starts a fresh device.
func TestClipboardRestartsAfterTheLoopEnds(t *testing.T) {
	r, f := clipRig(t, nil)
	r.enableClipboard(t, sessA)
	r.backend.clipboard.loopEnded()
	if err := r.obj.Call(ClipboardIface+".SelectionRead", 0, sessA, "text/plain").Err; err == nil {
		t.Error("a read after the device died")
	}
	if err := r.obj.Call(ClipboardIface+".RequestClipboard", 0, sessB, Vardict{}).Err; err != nil {
		t.Fatal(err)
	}
	if f.starts.Load() != 2 {
		t.Errorf("device starts = %d, want a fresh one", f.starts.Load())
	}
}
