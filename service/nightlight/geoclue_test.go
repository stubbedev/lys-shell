package nightlight

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	desktopidentity "github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-desktop/service/nightlight/nightschedule"
	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/third_party/godbus/prop"
)

const geoclueDropIn = "resources/geoclue-lys-shell.conf"

var quickWaits = geoclueWaits{agent: 50 * time.Millisecond, fix: 100 * time.Millisecond}

// fakeGeoClue serves Manager.GetClient, a Client that emits
// LocationUpdated on Start, and one Location.
type fakeGeoClue struct {
	conn *dbus.Conn
	// needsAgent holds GetClient until AddAgent, as the real daemon does.
	needsAgent bool
	addAgent   func(id string) *dbus.Error
	agents     chan agentRegistration
	released   chan struct{}
	noFix      bool
	deleted    chan dbus.ObjectPath
}

type agentRegistration struct {
	id     string
	sender dbus.Sender
}

func (g *fakeGeoClue) GetClient() (dbus.ObjectPath, *dbus.Error) {
	if g.needsAgent {
		<-g.released
	}
	return "/org/freedesktop/GeoClue2/Client/1", nil
}

func (g *fakeGeoClue) DeleteClient(path dbus.ObjectPath) *dbus.Error {
	g.deleted <- path
	return nil
}

func (g *fakeGeoClue) AddAgent(sender dbus.Sender, id string) *dbus.Error {
	if g.addAgent != nil {
		if err := g.addAgent(id); err != nil {
			return err
		}
	}
	g.agents <- agentRegistration{id: id, sender: sender}
	close(g.released)
	return nil
}

type fakeGeoClient struct{ g *fakeGeoClue }

func (c *fakeGeoClient) Start() *dbus.Error {
	if c.g.noFix {
		return nil
	}
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
	return serveFakeGeoClue(t, &fakeGeoClue{}, latitude, longitude)
}

func serveFakeGeoClue(t *testing.T, g *fakeGeoClue, latitude, longitude float64) (*prop.Properties, *dbus.Conn) {
	t.Helper()
	bus := dbustest.Start(t)
	daemon := bus.Conn(t)
	g.conn = daemon
	g.agents = make(chan agentRegistration, 1)
	g.deleted = make(chan dbus.ObjectPath, 4)
	g.released = make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-g.released:
		default:
			close(g.released)
		}
	})
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
	if _, err := QueryLocation(context.Background(), conn); !errors.Is(err, nightschedule.ErrNullIsland) {
		t.Errorf("err = %v, want ErrNullIsland", err)
	}
}

func TestQueryLocationFailsWithoutADaemon(t *testing.T) {
	bus := dbustest.Start(t)
	if _, err := queryLocation(context.Background(), bus.Conn(t), quickWaits); err == nil {
		t.Error("no GeoClue on the bus: want an error")
	}
}

// With no agent GetClient stays unanswered, so the lookup becomes the
// agent, and answers as one.
func TestQueryLocationRegistersAsTheAgentWhenNoneAnswers(t *testing.T) {
	g := &fakeGeoClue{needsAgent: true}
	_, conn := serveFakeGeoClue(t, g, 55.6761, 12.5683)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	loc, err := queryLocation(ctx, conn, geoclueWaits{agent: 50 * time.Millisecond, fix: 5 * time.Second})
	if err != nil {
		t.Fatalf("queryLocation: %v", err)
	}
	if loc.Latitude() != 55.6761 {
		t.Errorf("location = %+v", loc)
	}
	reg := <-g.agents
	if reg.id != desktopidentity.Name {
		t.Errorf("registered as %q, want %q", reg.id, desktopidentity.Name)
	}
	if string(reg.sender) != conn.Names()[0] {
		t.Errorf("registered from %s, want %s", reg.sender, conn.Names()[0])
	}
	agent := g.conn.Object(string(reg.sender), geoclueAgentPath)
	var level dbus.Variant
	if err := agent.Call("org.freedesktop.DBus.Properties.Get", 0, geoclueAgent, "MaxAccuracyLevel").Store(&level); err != nil {
		t.Fatalf("MaxAccuracyLevel: %v", err)
	}
	if level.Value() != accuracyCity {
		t.Errorf("MaxAccuracyLevel = %v, want city", level.Value())
	}
	for _, c := range []struct {
		id        string
		requested uint32
		allowed   bool
		level     uint32
	}{
		{desktopidentity.Name, accuracyCity, true, accuracyCity},
		{desktopidentity.Name, 8, true, accuracyCity},
		{"org.example.SandboxedApp", accuracyCity, false, 0},
	} {
		var allowed bool
		var granted uint32
		if err := agent.Call(geoclueAgent+".AuthorizeApp", 0, c.id, c.requested).Store(&allowed, &granted); err != nil {
			t.Fatalf("AuthorizeApp(%s): %v", c.id, err)
		}
		if allowed != c.allowed || granted != c.level {
			t.Errorf("AuthorizeApp(%s, %d) = %v, %d, want %v, %d", c.id, c.requested, allowed, granted, c.allowed, c.level)
		}
	}
}

// A running agent answers at once; registering would replace it.
func TestQueryLocationLeavesARunningAgentAlone(t *testing.T) {
	g := &fakeGeoClue{}
	_, conn := serveFakeGeoClue(t, g, 55.6761, 12.5683)
	if _, err := queryLocation(context.Background(), conn, geoclueWaits{agent: time.Hour, fix: 5 * time.Second}); err != nil {
		t.Fatalf("queryLocation: %v", err)
	}
	select {
	case reg := <-g.agents:
		t.Errorf("registered as an agent beside a running one: %+v", reg)
	default:
	}
}

// A refusal is certain, so it says what to change, and at once.
func TestQueryLocationReportsARefusedAgent(t *testing.T) {
	g := &fakeGeoClue{needsAgent: true, addAgent: func(id string) *dbus.Error {
		return dbus.NewError("org.freedesktop.DBus.Error.AccessDenied", []any{id + " not allowed to act as agent"})
	}}
	_, conn := serveFakeGeoClue(t, g, 55.6761, 12.5683)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err := queryLocation(ctx, conn, quickWaits)
	if err == nil {
		t.Fatal("a refused agent: want an error")
	}
	for _, want := range []string{"whitelist", "[agent]", "/etc/geoclue/conf.d/", "not allowed to act as agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the refusal, not a timeout", err)
	}
}

// A GeoClue that never answers keeps the deadline in the chain and says
// the likely cause, as a likely one.
func TestQueryLocationNamesTheLikelyCauseOfAHang(t *testing.T) {
	g := &fakeGeoClue{needsAgent: true, addAgent: func(string) *dbus.Error {
		select {}
	}}
	_, conn := serveFakeGeoClue(t, g, 55.6761, 12.5683)
	t.Run("the agent never registers", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err := queryLocation(ctx, conn, quickWaits)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want the deadline", err)
		}
		if !strings.Contains(err.Error(), "likely") || !strings.Contains(err.Error(), "/etc/geoclue/conf.d/") {
			t.Errorf("err = %q, want the likely cause and the file", err)
		}
	})
	t.Run("get client never answers", func(t *testing.T) {
		g := &fakeGeoClue{needsAgent: true}
		_, conn := serveFakeGeoClue(t, g, 55.6761, 12.5683)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err := queryLocation(ctx, conn, geoclueWaits{agent: time.Hour, fix: time.Second})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want the deadline", err)
		}
		for _, want := range []string{"likely", "not a certain one", "agent", "/etc/geoclue/geoclue.conf"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("err = %q, want it to name %q", err, want)
			}
		}
	})
}

// A client that starts and hears nothing has a different likely cause.
func TestQueryLocationNamesTheLikelyCauseOfNoFix(t *testing.T) {
	_, conn := serveFakeGeoClue(t, &fakeGeoClue{noFix: true}, 55.6761, 12.5683)
	_, err := queryLocation(context.Background(), conn, quickWaits)
	if err == nil {
		t.Fatal("no fix: want an error")
	}
	for _, want := range []string{"likely", "not a certain one", "source", "/etc/geoclue/geoclue.conf", "latitude and longitude"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The packaged drop-in holds what geoclue's loader needs: an entry named
// as the client's DesktopId with all three keys, since a new entry missing
// one is dropped, and a whitelist that keeps upstream's agents, since a
// drop-in's list replaces the main file's.
func TestGeoClueDropInMatchesTheClient(t *testing.T) {
	groups := map[string]map[string]string{}
	var group string
	for line := range strings.SplitSeq(readRepoFile(t, geoclueDropIn), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "["):
			group = strings.Trim(line, "[]")
			groups[group] = map[string]string{}
		default:
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				t.Fatalf("not a key: %q", line)
			}
			groups[group][key] = value
		}
	}
	entry, ok := groups[desktopidentity.Name]
	if !ok {
		t.Fatalf("no [%s] entry: %v", desktopidentity.Name, groups)
	}
	if entry["allowed"] != "true" || entry["system"] != "true" {
		t.Errorf("entry = %v, want allowed and system", entry)
	}
	if _, ok := entry["users"]; !ok {
		t.Errorf("entry = %v, want a users key: geoclue drops an entry without one", entry)
	}
	agents := strings.Split(groups["agent"]["whitelist"], ";")
	for _, want := range []string{desktopidentity.Name, "geoclue-demo-agent", "gnome-shell", "io.elementary.desktop.agent-geoclue2", "sm.puri.Phosh", "lipstick"} {
		if !slices.Contains(agents, want) {
			t.Errorf("whitelist = %v, want %q", agents, want)
		}
	}
}

func TestGeoClueDropInIsPackaged(t *testing.T) {
	const installed = "/geoclue/conf.d/50-lys-shell.conf"
	base := filepath.Base(geoclueDropIn)
	for path, want := range map[string][]string{
		"scripts/ci/build-archive.sh":     {geoclueDropIn},
		"packaging/debian/rules":          {base, "etc" + installed},
		"packaging/rpm/lys-shell.spec.in": {base, "%{_sysconfdir}" + installed},
	} {
		text := readRepoFile(t, path)
		for _, w := range want {
			if !strings.Contains(text, w) {
				t.Errorf("%s does not mention %s", path, w)
			}
		}
	}
	if docs := readRepoFile(t, "docs/config/modules/nightlight.md"); !strings.Contains(docs, "/etc"+installed) {
		t.Errorf("the docs do not name /etc%s", installed)
	}
}

func TestALookupOnASharedConnectionDeletesItsClient(t *testing.T) {
	g := &fakeGeoClue{}
	_, conn := serveFakeGeoClue(t, g, 55.6761, 12.5683)
	for range 2 {
		if _, err := QueryLocation(context.Background(), conn); err != nil {
			t.Fatalf("QueryLocation: %v", err)
		}
		select {
		case path := <-g.deleted:
			if path != "/org/freedesktop/GeoClue2/Client/1" {
				t.Errorf("deleted %s, want the lookup's client", path)
			}
		default:
			t.Fatal("the lookup left its client to the connection's end")
		}
	}
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatalf("the lookup closed the shared connection: %v", err)
	}
}
