package portal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// fakeChooser answers OpenFile with uri through a Response signal on the
// spec's request path, recording the filters; cancel makes the user
// dismiss it.
type fakeChooser struct {
	conn    *dbus.Conn
	uri     string
	cancel  bool
	filters dbustest.Var[[]filterEntry]
	title   dbustest.Var[string]
	// legacy answers on a path of the portal's own choosing, as one that
	// ignores handle_token does.
	legacy bool
	// impostor, when set, is another peer that announces the request
	// cancelled before the portal answers it.
	impostor *dbus.Conn
}

func (f *fakeChooser) OpenFile(sender dbus.Sender, _ string, title string, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	f.title.Store(title)
	var entries []filterEntry
	if v, ok := options["filters"]; ok {
		_ = v.Store(&entries)
	}
	f.filters.Store(entries)
	handle, _ := options["handle_token"].Value().(string)
	path := dbus.ObjectPath(requestPrefix + strings.ReplaceAll(strings.TrimPrefix(string(sender), ":"), ".", "_") + "/" + handle)
	if f.legacy {
		path = requestPrefix + "legacy/1"
	}
	if f.impostor != nil {
		if err := responded.Emit(f.impostor, path, response{Code: ResponseCancelled, Results: map[string]dbus.Variant{}}); err != nil {
			return "", dbus.MakeFailedError(err)
		}
		if err := f.impostor.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
			return "", dbus.MakeFailedError(err)
		}
	}
	code := ResponseSuccess
	if f.cancel {
		code = ResponseCancelled
	}
	results := map[string]dbus.Variant{"uris": dbus.MakeVariant([]string{f.uri})}
	go func() {
		time.Sleep(5 * time.Millisecond)
		_ = responded.Emit(f.conn, path, response{Code: code, Results: results})
	}()
	return path, nil
}

func serveChooser(t *testing.T, bus *dbustest.Bus, uri string, cancel bool) *fakeChooser {
	t.Helper()
	return serveChooserAs(t, bus, &fakeChooser{uri: uri, cancel: cancel})
}

// serveChooserAs puts f, set up as the test wants it, on bus as the
// portal.
func serveChooserAs(t *testing.T, bus *dbustest.Bus, f *fakeChooser) *fakeChooser {
	t.Helper()
	conn := bus.Conn(t)
	f.conn = conn
	if err := conn.Export(f, ObjectPath, FileChooserIface); err != nil {
		t.Fatal(err)
	}
	if reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own: %v %v", reply, err)
	}
	return f
}

func TestOpenFile(t *testing.T) {
	bus := dbustest.Start(t)
	f := serveChooser(t, bus, "file:///home/u/wg%200.conf", false)
	path, err := OpenFile(context.Background(), bus.Conn(t), "Import", FileFilter{Name: "WireGuard", Patterns: []string{"*.conf"}})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/home/u/wg 0.conf" {
		t.Errorf("path = %q", path)
	}
	if got := f.filters.Load(); len(got) != 1 || got[0].Name != "WireGuard" || len(got[0].Rules) != 1 || got[0].Rules[0].Pattern != "*.conf" {
		t.Errorf("filters = %+v", got)
	}
	if f.title.Load() != "Import" {
		t.Errorf("title = %q", f.title.Load())
	}
}

func TestOpenFileCancelledAndRemote(t *testing.T) {
	bus := dbustest.Start(t)
	serveChooser(t, bus, "file:///x", true)
	if _, err := OpenFile(context.Background(), bus.Conn(t), "Import"); !errors.Is(err, ErrCancelled) {
		t.Errorf("cancelled = %v", err)
	}
	bus2 := dbustest.Start(t)
	serveChooser(t, bus2, "https://example.com/x.conf", false)
	if _, err := OpenFile(context.Background(), bus2.Conn(t), "Import"); err == nil {
		t.Error("a remote uri was accepted")
	}
}

// A portal that ignores handle_token answers on a path of its own, which
// the call follows once the portal names it.
func TestOpenFileFollowsAnOldPortalsRequestPath(t *testing.T) {
	bus := dbustest.Start(t)
	serveChooserAs(t, bus, &fakeChooser{uri: "file:///home/u/old.conf", legacy: true})
	path, err := OpenFile(context.Background(), bus.Conn(t), "Import")
	if err != nil || path != "/home/u/old.conf" {
		t.Fatalf("OpenFile = %q, %v; want the old portal's answer", path, err)
	}
}

// Only the portal answers a request: another peer announcing it
// cancelled on the request's path does not end the call, and the call
// leaves no match rule on the caller's connection.
func TestOpenFileTakesItsResponseFromThePortalOnly(t *testing.T) {
	bus := dbustest.Start(t)
	serveChooserAs(t, bus, &fakeChooser{uri: "file:///home/u/real.conf", impostor: bus.Conn(t)})
	conn := bus.Conn(t)
	path, err := OpenFile(context.Background(), conn, "Import")
	if err != nil || path != "/home/u/real.conf" {
		t.Fatalf("OpenFile = %q, %v; want the portal's answer, not the impostor's cancellation", path, err)
	}
	if rules := dbustest.MatchRules(t, conn); len(rules) != 0 {
		t.Fatalf("the call left the match rules %q", rules)
	}
}
