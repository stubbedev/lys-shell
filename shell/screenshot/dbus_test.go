package screenshot

import (
	"context"
	"errors"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// switchCapturer is a host whose failure the test flips while the
// daemon serves it from the bus goroutine.
type switchCapturer struct{ err dbustest.Var[error] }

func (s *switchCapturer) Capture(c portaldialogs.Caller, mode, target string) (string, error) {
	return fakeCapturer{err: s.err.Load()}.Capture(c, mode, target)
}

func (s *switchCapturer) CapturePrivate(c portaldialogs.Caller, mode, target string, pointer bool) (string, error) {
	return fakeCapturer{err: s.err.Load()}.CapturePrivate(c, mode, target, pointer)
}

func (s *switchCapturer) PickColor(c portaldialogs.Caller) ([3]float64, bool, error) {
	return fakeCapturer{err: s.err.Load()}.PickColor(c)
}

func (s *switchCapturer) Dismiss(string) {}

func TestDaemonOverTheBus(t *testing.T) {
	bus := dbustest.Start(t)
	server, client := bus.Conn(t), bus.Conn(t)
	host := &switchCapturer{}
	d := &Daemon{host: host}
	release, err := d.Export(server)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	c := NewClient(client)
	path, err := c.Capture(context.Background(), portaldialogs.Caller{}, "output", "DP-1")
	if err != nil || path != "/p/outputDP-1" {
		t.Fatalf("Capture = %q %v", path, err)
	}
	color, picked, err := c.PickColor(context.Background(), portaldialogs.Caller{})
	if err != nil || !picked || color != [3]float64{0.5, 0.25, 1} {
		t.Fatalf("PickColor = %v %v %v", color, picked, err)
	}
	if path, err := c.CapturePrivate(context.Background(), portaldialogs.Caller{}, "window", "id-1", false); err != nil || path != "/private/windowid-1" {
		t.Fatalf("CapturePrivate = %q %v", path, err)
	}
	if path, err := c.CapturePrivate(context.Background(), portaldialogs.Caller{}, "screen", "", true); err != nil || path != "/private/screen+pointer" {
		t.Fatalf("CapturePrivate with the pointer = %q %v", path, err)
	}

	// A second daemon cannot steal the name.
	if _, err := (&Daemon{host: fakeCapturer{}}).Export(client); err == nil {
		t.Fatal("a second daemon claimed the owned name")
	}

	// Host errors arrive as Failed replies carrying the message.
	host.err.Store(errors.New("window capture not supported on this compositor"))
	_, err = c.Capture(context.Background(), portaldialogs.Caller{}, "window", "")
	var dbusErr dbus.Error
	if !errors.As(err, &dbusErr) || dbusErr.Name != "org.freedesktop.DBus.Error.Failed" ||
		dbusErr.Body[0] != "window capture not supported on this compositor" {
		t.Fatalf("Capture error = %#v", err)
	}
}
