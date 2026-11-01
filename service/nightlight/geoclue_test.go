package nightlight

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/third_party/godbus/prop"
)

// fakeGeoClue serves Manager.GetClient, a Client that emits
// LocationUpdated on Start, and one Location.
type fakeGeoClue struct {
	conn *dbus.Conn
}

func (g *fakeGeoClue) GetClient() (dbus.ObjectPath, *dbus.Error) {
	return "/org/freedesktop/GeoClue2/Client/1", nil
}

type fakeGeoClient struct{ g *fakeGeoClue }

func (c *fakeGeoClient) Start() *dbus.Error {
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = c.g.conn.Emit("/org/freedesktop/GeoClue2/Client/1", geoclueClient+".LocationUpdated",
			dbus.ObjectPath("/"), dbus.ObjectPath("/org/freedesktop/GeoClue2/Location/1"))
	}()
	return nil
}

func (c *fakeGeoClient) Stop() *dbus.Error { return nil }

// serveGeoClue puts a GeoClue on a private bus that reports one fix,
// and returns the client's properties and a connection to query it over.
func serveGeoClue(t *testing.T, latitude, longitude float64) (*prop.Properties, *dbus.Conn) {
	t.Helper()
	bus := dbustest.Start(t)
	daemon := bus.Conn(t)
	g := &fakeGeoClue{conn: daemon}
	if err := daemon.Export(g, geoclueManagerPath, geoclueManager); err != nil {
		t.Fatal(err)
	}
	clientPath := dbus.ObjectPath("/org/freedesktop/GeoClue2/Client/1")
	if err := daemon.Export(&fakeGeoClient{g: g}, clientPath, geoclueClient); err != nil {
		t.Fatal(err)
	}
	clientProps, err := prop.Export(daemon, clientPath, prop.Map{geoclueClient: {
		"DesktopId":              {Value: "", Writable: true, Emit: prop.EmitFalse},
		"RequestedAccuracyLevel": {Value: uint32(0), Writable: true, Emit: prop.EmitFalse},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prop.Export(daemon, "/org/freedesktop/GeoClue2/Location/1", prop.Map{geoclueLocation: {
		"Latitude":  {Value: latitude, Emit: prop.EmitConst},
		"Longitude": {Value: longitude, Emit: prop.EmitConst},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.RequestName(geoclueService, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}

	return clientProps, bus.Conn(t)
}

func TestQueryLocationOverGeoClue(t *testing.T) {
	clientProps, conn := serveGeoClue(t, 55.6761, 12.5683)
	loc, err := QueryLocation(context.Background(), conn)
	if err != nil {
		t.Fatalf("QueryLocation: %v", err)
	}
	if loc.Latitude() != 55.6761 || loc.Longitude() != 12.5683 {
		t.Errorf("location = %+v", loc)
	}
	// The client was configured before Start.
	if id, _ := clientProps.Get(geoclueClient, "DesktopId"); id.Value() != "lys-shell" {
		t.Errorf("DesktopId = %v, want lys-shell", id.Value())
	}
	if lvl, _ := clientProps.Get(geoclueClient, "RequestedAccuracyLevel"); lvl.Value() != accuracyCity {
		t.Errorf("accuracy = %v, want city", lvl.Value())
	}
}

// A fix of 0, 0 is an agent with nothing to say, not a position.
func TestQueryLocationRefusesANullFix(t *testing.T) {
	_, conn := serveGeoClue(t, 0, 0)
	if _, err := QueryLocation(context.Background(), conn); !errors.Is(err, ErrNullIsland) {
		t.Errorf("err = %v, want ErrNullIsland", err)
	}
}

func TestQueryLocationFailsWithoutADaemon(t *testing.T) {
	bus := dbustest.Start(t)
	if _, err := queryLocation(context.Background(), bus.Conn(t), 100*time.Millisecond); err == nil {
		t.Error("no GeoClue on the bus: want an error")
	}
}
