package screenshot_test

import (
	"context"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/portal"
	"github.com/stubbedev/lys-shell/shell/colorpicker"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

type busyRegion struct{}

func (busyRegion) Request(context.Context, regionoverlay.Frames) (regionoverlay.Selection, bool, error) {
	return regionoverlay.Selection{}, false, screenshot.ErrBusy
}

type busyPicker struct{}

func (busyPicker) Request(context.Context, regionoverlay.Frames, colorpicker.Options) (colorpicker.Pick, bool, error) {
	return colorpicker.Pick{}, false, screenshot.ErrBusy
}

// The real portal backend in front of the real daemon: an app asking while a
// capture overlay is open gets a failed request, with no result.
func TestPortalAnswersARefusedRequestAsFailed(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	bus := dbustest.Start(t)
	host := &screenshot.Host{Region: busyRegion{}, Picker: busyPicker{}}
	if _, err := screenshot.NewDaemon(host).Export(bus.Conn(t)); err != nil {
		t.Fatal(err)
	}
	stop, err := portal.New(bus.Conn(t), config.Load(t.TempDir(), config.DiscardDiagnostics)).Serve()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	obj := bus.Conn(t).Object(portal.BusName, portal.ObjectPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const handle = dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_1/busy")
	const iface = "org.freedesktop.impl.portal.Screenshot"
	const targetArea = uint32(4)
	for name, call := range map[string]*dbus.Call{
		"Screenshot": obj.CallWithContext(ctx, iface+".Screenshot", 0, handle, "org.app", "", map[string]dbus.Variant{"target": dbus.MakeVariant(targetArea)}),
		"PickColor":  obj.CallWithContext(ctx, iface+".PickColor", 0, handle, "org.app", "", map[string]dbus.Variant{}),
	} {
		var code uint32
		var results map[string]dbus.Variant
		if err := call.Store(&code, &results); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if code != portal.ResponseOther || len(results) != 0 {
			t.Errorf("%s answered %d %v, want the failed response %d and no result", name, code, results, portal.ResponseOther)
		}
	}
}
