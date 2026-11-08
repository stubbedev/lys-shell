package bar

import (
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/swaytest"
	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/service/shellipc"
)

// announcedConnectors waits for Shell1 to announce Connectors as want,
// failing with every announcement it saw instead.
func announcedConnectors(t *testing.T, signals <-chan *dbus.Signal, want []string) {
	t.Helper()
	var seen [][]string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case sig := <-signals:
			if len(sig.Body) < 2 || sig.Body[0] != shellipc.ServiceName {
				continue
			}
			changed, _ := sig.Body[1].(map[string]dbus.Variant)
			v, ok := changed["Connectors"]
			if !ok {
				continue
			}
			got, _ := v.Value().([]string)
			if slices.Equal(got, want) {
				return
			}
			seen = append(seen, got)
		case <-deadline:
			t.Fatalf("Shell1 never announced Connectors %q; it announced %q", want, seen)
		}
	}
}

// An output plugged after startup, and one unplugged, reach Shell1's
// Connectors and are announced with PropertiesChanged, on a real sway
// hotplugging headless outputs.
func TestShell1ConnectorsFollowHotplug(t *testing.T) {
	a := apptest.Run(t)
	bus := dbustest.Start(t)
	server, client := bus.Conn(t), bus.Conn(t)
	state := shellipc.NewState(nil)
	release, err := shellipc.Serve(server, state, shellipc.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := client.AddMatchSignal(dbus.WithMatchObjectPath(shellipc.ServicePath), dbus.WithMatchInterface("org.freedesktop.DBus.Properties"), dbus.WithMatchMember("PropertiesChanged")); err != nil {
		t.Fatal(err)
	}
	signals := make(chan *dbus.Signal, 16)
	client.Signal(signals)

	o := &fakeOpener{}
	set := newSurfaceSet(o.open)
	first := apptest.On(a, func() string {
		t.Cleanup(set.followOutputs(a.Session(), showClock, func(*app.Output) {}))
		set.publishTo(state.SetConnectors)
		return a.Session().Outputs()[0].Name
	})
	announcedConnectors(t, signals, []string{first})

	swaytest.Msg(t, "create_output")
	announcedConnectors(t, signals, []string{first, "HEADLESS-2"})
	get := func() []string {
		v, err := client.Object(shellipc.ServiceName, shellipc.ServicePath).GetProperty(shellipc.ServiceName + ".Connectors")
		if err != nil {
			t.Fatal(err)
		}
		return v.Value().([]string)
	}
	if got := get(); !slices.Equal(got, []string{first, "HEADLESS-2"}) {
		t.Fatalf("Connectors = %q after the plug", got)
	}

	swaytest.Msg(t, "output", "HEADLESS-2", "unplug")
	announcedConnectors(t, signals, []string{first})
	if got := get(); !slices.Equal(got, []string{first}) {
		t.Fatalf("Connectors = %q after the unplug", got)
	}
	select {
	case sig := <-signals:
		t.Errorf("an announcement past the unplug: %v", sig.Body)
	case <-time.After(200 * time.Millisecond):
	}
}
