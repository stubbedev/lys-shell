package nightlight

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	desktopidentity "github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-desktop/service/nightlight/nightschedule"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/third_party/godbus/prop"
)

// GeoClue2 names (geoclue.rs).
const (
	geoclueService     = "org.freedesktop.GeoClue2"
	geoclueManagerPath = dbus.ObjectPath("/org/freedesktop/GeoClue2/Manager")
	geoclueManager     = "org.freedesktop.GeoClue2.Manager"
	geoclueClient      = "org.freedesktop.GeoClue2.Client"
	geoclueLocation    = "org.freedesktop.GeoClue2.Location"
	geoclueAgent       = "org.freedesktop.GeoClue2.Agent"
	geoclueAgentPath   = dbus.ObjectPath("/org/freedesktop/GeoClue2/Agent")
	// geoclueConfigFiles is where a host's GeoClue policy lives.
	geoclueConfigFiles = "/etc/geoclue/geoclue.conf and /etc/geoclue/conf.d/"
	// accuracyCity is GeoClue's "city" level: enough for a sunrise
	// schedule, and the least invasive level that yields coordinates.
	accuracyCity = uint32(4)
	// fixTimeout bounds the wait for the first location fix.
	fixTimeout = 15 * time.Second
	// agentGrace is how long GetClient may stay unanswered before the
	// lookup takes it that no agent is coming and registers as one. A
	// running agent answers at once, and registering would replace it.
	agentGrace = 5 * time.Second

	deleteClientTimeout = 2 * time.Second
)

// geoclueWaits bounds the two waits of a lookup.
type geoclueWaits struct {
	agent, fix time.Duration
}

// QueryLocation resolves the position once over GeoClue2 on conn (the
// system bus in the shell): get a client, set DesktopId and the city
// accuracy, start it, and take the first LocationUpdated. GeoClue answers
// GetClient only once an agent serves this user, so with none running
// the lookup registers as one for as long as conn stays open; the caller
// closes conn when the lookup returns, which ends that. Any failure (no
// daemon, not allowed as an agent, denied, timeout, a fix of 0, 0) is an
// error, which leaves the schedule on the last location it had, or on
// none.
func QueryLocation(ctx context.Context, conn *dbus.Conn) (nightschedule.Location, error) {
	return queryLocation(ctx, conn, geoclueWaits{agent: agentGrace, fix: fixTimeout})
}

// getClient asks for a client, registering as the agent if nothing
// answers within waits.agent.
func getClient(ctx context.Context, conn *dbus.Conn, wait time.Duration) (dbus.ObjectPath, error) {
	manager := conn.Object(geoclueService, geoclueManagerPath)
	call := manager.GoWithContext(ctx, geoclueManager+".GetClient", 0, make(chan *dbus.Call, 1))
	grace := time.NewTimer(wait)
	defer grace.Stop()
	select {
	case <-call.Done:
	case <-grace.C:
		if err := registerAgent(ctx, conn, manager); err != nil {
			return "", err
		}
		<-call.Done
	}
	var clientPath dbus.ObjectPath
	err := call.Store(&clientPath)
	if err != nil {
		return "", noAnswerError("get client", err)
	}
	return clientPath, nil
}

// noAnswerError wraps a failed call, naming what a timeout likely means.
func noAnswerError(step string, err error) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("geoclue: %s: %w", step, err)
	}
	return fmt.Errorf("geoclue: %s: %w: GeoClue answers only once an agent serves this user, so the likely cause, not a certain one, is that no agent runs and GeoClue's whitelist does not allow %s as one; check %s", step, err, desktopidentity.Name, geoclueConfigFiles)
}

// selfAgent is the agent a lookup registers when the session has none:
// it allows this shell at city accuracy and, with no way to ask the
// user, nothing else. GeoClue never asks it about a client that is not
// sandboxed, so in practice a sandboxed app is what it turns down.
type selfAgent struct{}

// AuthorizeApp is the Agent interface's method.
func (selfAgent) AuthorizeApp(desktopID string, requested uint32) (bool, uint32, *dbus.Error) {
	if desktopID != desktopidentity.Name {
		return false, 0, nil
	}
	return true, min(requested, accuracyCity), nil
}

func registerAgent(ctx context.Context, conn *dbus.Conn, manager dbus.BusObject) error {
	if err := conn.Export(selfAgent{}, geoclueAgentPath, geoclueAgent); err != nil {
		return fmt.Errorf("geoclue: export the agent: %w", err)
	}
	if _, err := prop.Export(conn, geoclueAgentPath, prop.Map{geoclueAgent: {
		"MaxAccuracyLevel": {Value: accuracyCity, Emit: prop.EmitConst},
	}}); err != nil {
		return fmt.Errorf("geoclue: export the agent's properties: %w", err)
	}
	err := manager.CallWithContext(ctx, geoclueManager+".AddAgent", 0, desktopidentity.Name).Err
	var refused dbus.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refused) && refused.Name == "org.freedesktop.DBus.Error.AccessDenied":
		return fmt.Errorf("geoclue: no agent serves this user, and GeoClue does not allow %s as one: add it to whitelist under [agent] in %s, then restart geoclue: %w", desktopidentity.Name, geoclueConfigFiles, err)
	default:
		return noAnswerError("register as the agent", err)
	}
}

func queryLocation(ctx context.Context, conn *dbus.Conn, waits geoclueWaits) (nightschedule.Location, error) {
	clientPath, err := getClient(ctx, conn, waits.agent)
	if err != nil {
		return nightschedule.Location{}, err
	}
	defer deleteClient(conn, clientPath)
	client := conn.Object(geoclueService, clientPath)
	set := func(prop string, value any) error {
		return client.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Set", 0, geoclueClient, prop, dbus.MakeVariant(value)).Err
	}
	// DesktopId is mandatory before Start; without a requested accuracy
	// the daemon yields nothing.
	if err := set("DesktopId", desktopidentity.Name); err != nil {
		return nightschedule.Location{}, fmt.Errorf("geoclue: desktop id: %w", err)
	}
	if err := set("RequestedAccuracyLevel", accuracyCity); err != nil {
		return nightschedule.Location{}, fmt.Errorf("geoclue: accuracy: %w", err)
	}
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(clientPath), dbus.WithMatchInterface(geoclueClient), dbus.WithMatchMember("LocationUpdated")}
	if err := conn.AddMatchSignal(match...); err != nil {
		return nightschedule.Location{}, fmt.Errorf("geoclue: match: %w", err)
	}
	defer func() { _ = conn.RemoveMatchSignal(match...) }()
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	stop := func() { _ = client.CallWithContext(context.Background(), geoclueClient+".Stop", 0).Err }
	if err := client.CallWithContext(ctx, geoclueClient+".Start", 0).Err; err != nil {
		return nightschedule.Location{}, fmt.Errorf("geoclue: start: %w", err)
	}
	defer stop()
	deadline := time.NewTimer(waits.fix)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return nightschedule.Location{}, fmt.Errorf("geoclue: wait for a location fix: %w", ctx.Err())
		case <-deadline.C:
			return nightschedule.Location{}, fmt.Errorf("geoclue: no location fix within %s: the likely cause, not a certain one, is that GeoClue has no source that can place this machine; check the wifi url and the static source in %s, or set latitude and longitude", waits.fix, geoclueConfigFiles)
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
func readLocation(ctx context.Context, obj dbus.BusObject) (nightschedule.Location, error) {
	latitude, err := readCoordinate(ctx, obj, "Latitude")
	if err != nil {
		return nightschedule.Location{}, err
	}
	longitude, err := readCoordinate(ctx, obj, "Longitude")
	if err != nil {
		return nightschedule.Location{}, err
	}
	loc, err := nightschedule.NewLocation(latitude, longitude)
	if err != nil {
		return nightschedule.Location{}, fmt.Errorf("geoclue: %w", err)
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

func deleteClient(conn *dbus.Conn, clientPath dbus.ObjectPath) {
	ctx, cancel := context.WithTimeout(context.Background(), deleteClientTimeout)
	defer cancel()
	if err := conn.Object(geoclueService, geoclueManagerPath).CallWithContext(ctx, geoclueManager+".DeleteClient", 0, clientPath).Err; err != nil {
		log.Printf("geoclue: delete client %s: %v", clientPath, err)
	}
}
