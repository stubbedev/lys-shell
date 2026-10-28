package brightness

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

type fakeSource struct {
	mu      sync.Mutex
	devices []Device
	setErr  error
	sets    []string
}

func (f *fakeSource) Devices(context.Context) ([]Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Device(nil), f.devices...), nil
}

func (f *fakeSource) Subscribe(context.Context) (<-chan struct{}, func(), error) {
	return nil, func() {}, nil
}

func (f *fakeSource) Set(_ context.Context, name string, percent float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return f.setErr
	}
	f.sets = append(f.sets, name)
	for i, d := range f.devices {
		if d.Name == name {
			f.devices[i].Brightness = rawLevel(percent, d.Max)
		}
	}
	return nil
}

func (f *fakeSource) level(name string) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.devices {
		if d.Name == name {
			return d.Brightness
		}
	}
	return math.MaxUint32
}

func laptopWithMonitor() *fakeSource {
	return &fakeSource{devices: []Device{
		{Name: "intel_backlight", Type: TypeRaw, Brightness: 500, Max: 1000, Connector: "eDP-1"},
		{Name: "ddci2c-6", Type: TypeDDC, Brightness: 40, Max: 100, Connector: "DP-2"},
	}}
}

func TestSetOutputChangesOnlyThatOutputsDevices(t *testing.T) {
	src := laptopWithMonitor()
	level, err := SetOutput(t.Context(), src, "DP-2", Relative(15), 5)
	if err != nil || level != 55 {
		t.Fatalf("+15 on DP-2 = %v, %v; want 55", level, err)
	}
	if src.level("ddci2c-6") != 55 || src.level("intel_backlight") != 500 {
		t.Errorf("levels: monitor %d panel %d, want 55 and the panel untouched", src.level("ddci2c-6"), src.level("intel_backlight"))
	}
	level, err = SetOutput(t.Context(), src, "eDP-1", Relative(-100), 5)
	if err != nil || level != 5 || src.level("intel_backlight") != 50 {
		t.Errorf("-100 on eDP-1 = %v, %v (raw %d); want the 5%% floor", level, err, src.level("intel_backlight"))
	}
}

func TestSetOutputFailsLoudly(t *testing.T) {
	src := laptopWithMonitor()
	if _, err := SetOutput(t.Context(), src, "HDMI-A-1", Relative(10), 5); err == nil || !strings.Contains(err.Error(), "drives output HDMI-A-1") {
		t.Errorf("an output without a device: %v", err)
	}
	src.setErr = errors.New("logind: access denied")
	_, err := SetOutput(t.Context(), src, "eDP-1", Relative(10), 5)
	if err == nil || err.Error() != "brightness: set intel_backlight on eDP-1: logind: access denied" {
		t.Errorf("a failed write: %v", err)
	}
	if len(src.sets) != 0 {
		t.Errorf("writes recorded despite failing: %v", src.sets)
	}
}

func serveDaemon(t *testing.T, src Source, focused func() (string, bool)) dbus.BusObject {
	t.Helper()
	bus := dbustest.Start(t)
	release, err := NewDaemon(src, focused, func() float64 { return 5 }).Export(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return bus.Conn(t).Object(ServiceName, ServicePath)
}

func apply(obj dbus.BusObject, output string, relative bool, percent int32) (string, float64, error) {
	var name string
	var level float64
	err := obj.Call(ServiceName+".Apply", 0, output, relative, percent).Store(&name, &level)
	return name, level, err
}

func TestDaemonAppliesToTheFocusedOrNamedOutput(t *testing.T) {
	src := laptopWithMonitor()
	obj := serveDaemon(t, src, func() (string, bool) { return "eDP-1", true })
	if name, level, err := apply(obj, "", true, 10); err != nil || name != "eDP-1" || level != 60 {
		t.Errorf("focused +10 = %q %v %v, want eDP-1 at 60", name, level, err)
	}
	if name, level, err := apply(obj, "DP-2", false, 0); err != nil || name != "DP-2" || level != 5 {
		t.Errorf("DP-2 at 0 = %q %v %v, want DP-2 held at 5", name, level, err)
	}
}

func TestDaemonRefusesWithoutFocusOrWithABadLevel(t *testing.T) {
	obj := serveDaemon(t, laptopWithMonitor(), func() (string, bool) { return "", false })
	_, _, err := apply(obj, "", true, 10)
	de, ok := errors.AsType[dbus.Error](err)
	if !ok || de.Name != dbusx.ErrFailed || de.Body[0] != "the compositor reports no focused output; name one with --output" {
		t.Errorf("no focus: %v", err)
	}
	_, _, err = apply(obj, "eDP-1", false, 101)
	de, ok = errors.AsType[dbus.Error](err)
	if !ok || de.Name != dbusx.ErrInvalidArgs || de.Body[0] != "101 is not in 0..=100" {
		t.Errorf("level 101: %v", err)
	}
}
