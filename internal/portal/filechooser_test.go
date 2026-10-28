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
	code := ResponseSuccess
	if f.cancel {
		code = ResponseCancelled
	}
	results := map[string]dbus.Variant{"uris": dbus.MakeVariant([]string{f.uri})}
	go func() {
		time.Sleep(5 * time.Millisecond)
		_ = f.conn.Emit(path, responseSig, code, results)
	}()
	return path, nil
}

func serveChooser(t *testing.T, bus *dbustest.Bus, uri string, cancel bool) *fakeChooser {
	t.Helper()
	conn := bus.Conn(t)
	f := &fakeChooser{conn: conn, uri: uri, cancel: cancel}
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
