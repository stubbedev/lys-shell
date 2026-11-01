package nightlight

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/version"
)

// GeoClue2 names (geoclue.rs).
const (
	geoclueService     = "org.freedesktop.GeoClue2"
	geoclueManagerPath = dbus.ObjectPath("/org/freedesktop/GeoClue2/Manager")
	geoclueManager     = "org.freedesktop.GeoClue2.Manager"
	geoclueClient      = "org.freedesktop.GeoClue2.Client"
	geoclueLocation    = "org.freedesktop.GeoClue2.Location"
	// accuracyCity is GeoClue's "city" level: enough for a sunrise
	// schedule, and the least invasive level that yields coordinates.
	accuracyCity = uint32(4)
	// fixTimeout bounds the wait for the first location fix.
	fixTimeout = 15 * time.Second
)

// QueryLocation resolves the position once over GeoClue2 on conn (the
// system bus in the shell): get a client, set DesktopId and the city
// accuracy, start it, and take the first LocationUpdated. Any failure
// (no daemon, no agent, denied, timeout, a fix of 0, 0) is an error,
// which leaves the schedule on the last location it had, or on none.
func QueryLocation(ctx context.Context, conn *dbus.Conn) (Location, error) {
	return queryLocation(ctx, conn, fixTimeout)
}

func queryLocation(ctx context.Context, conn *dbus.Conn, timeout time.Duration) (Location, error) {
	var clientPath dbus.ObjectPath
	if err := conn.Object(geoclueService, geoclueManagerPath).CallWithContext(ctx, geoclueManager+".GetClient", 0).Store(&clientPath); err != nil {
		return Location{}, fmt.Errorf("geoclue: get client: %w", err)
	}
	client := conn.Object(geoclueService, clientPath)
	set := func(prop string, value any) error {
		return client.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Set", 0, geoclueClient, prop, dbus.MakeVariant(value)).Err
	}
	// DesktopId is mandatory before Start; without a requested accuracy
	// the daemon yields nothing.
	if err := set("DesktopId", version.Name); err != nil {
		return Location{}, fmt.Errorf("geoclue: desktop id: %w", err)
	}
	if err := set("RequestedAccuracyLevel", accuracyCity); err != nil {
		return Location{}, fmt.Errorf("geoclue: accuracy: %w", err)
	}
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(clientPath), dbus.WithMatchInterface(geoclueClient), dbus.WithMatchMember("LocationUpdated")}
	if err := conn.AddMatchSignal(match...); err != nil {
		return Location{}, fmt.Errorf("geoclue: match: %w", err)
	}
	defer func() { _ = conn.RemoveMatchSignal(match...) }()
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	stop := func() { _ = client.CallWithContext(context.Background(), geoclueClient+".Stop", 0).Err }
	if err := client.CallWithContext(ctx, geoclueClient+".Start", 0).Err; err != nil {
		return Location{}, fmt.Errorf("geoclue: start: %w", err)
	}
	defer stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return Location{}, fmt.Errorf("geoclue: wait for a location fix: %w", ctx.Err())
		case <-deadline.C:
			return Location{}, errors.New("geoclue: location fix timed out")
		case sig := <-signals:
			if sig.Path != clientPath || sig.Name != geoclueClient+".LocationUpdated" || len(sig.Body) != 2 {
				continue
			}
			locPath, ok := sig.Body[1].(dbus.ObjectPath)
			if !ok {
				continue
			}
			return readLocation(ctx, conn.Object(geoclueService, locPath))
		}
	}
}

// readLocation reads the Latitude and Longitude of a Location object.
func readLocation(ctx context.Context, obj dbus.BusObject) (Location, error) {
	latitude, err := readCoordinate(ctx, obj, "Latitude")
	if err != nil {
		return Location{}, err
	}
	longitude, err := readCoordinate(ctx, obj, "Longitude")
	if err != nil {
		return Location{}, err
	}
	loc, err := NewLocation(latitude, longitude)
	if err != nil {
		return Location{}, fmt.Errorf("geoclue: %w", err)
	}
	return loc, nil
}

func readCoordinate(ctx context.Context, obj dbus.BusObject, prop string) (float64, error) {
	var v dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, geoclueLocation, prop).Store(&v); err != nil {
		return 0, fmt.Errorf("geoclue: %s: %w", prop, err)
	}
	f, ok := v.Value().(float64)
	if !ok {
		return 0, fmt.Errorf("geoclue: %s is %T, want a double", prop, v.Value())
	}
	return f, nil
}
