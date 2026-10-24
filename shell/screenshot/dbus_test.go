package screenshot

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
)

// switchCapturer is a host whose failure the test flips while the
// daemon serves it from the bus goroutine.
type switchCapturer struct{ err dbustest.Var[error] }

func (s *switchCapturer) Capture(c portalreq.Caller, mode, target string) (string, error) {
	return fakeCapturer{err: s.err.Load()}.Capture(c, mode, target)
}

func (s *switchCapturer) PickColor(c portalreq.Caller) (float64, float64, float64, error) {
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
	path, err := c.Capture(context.Background(), portalreq.Caller{}, "output", "DP-1")
	if err != nil || path != "/p/outputDP-1" {
		t.Fatalf("Capture = %q %v", path, err)
	}
	r, g, b, err := c.PickColor(context.Background(), portalreq.Caller{})
	if err != nil || r != 0.5 || g != 0.25 || b != 1 {
		t.Fatalf("PickColor = %v %v %v %v", r, g, b, err)
	}

	// A second daemon cannot steal the name.
	if _, err := (&Daemon{host: fakeCapturer{}}).Export(client); err == nil {
		t.Fatal("a second daemon claimed the owned name")
	}

	// Host errors arrive as Failed replies carrying the message.
	host.err.Store(errors.New("window capture not supported on this compositor"))
	_, err = c.Capture(context.Background(), portalreq.Caller{}, "window", "")
	var dbusErr dbus.Error
	if !errors.As(err, &dbusErr) || dbusErr.Name != "org.freedesktop.DBus.Error.Failed" ||
		dbusErr.Body[0] != "window capture not supported on this compositor" {
		t.Fatalf("Capture error = %#v", err)
	}
}
