package upower_test

import (
	"context"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/upower"
	"github.com/stubbedev/lys-shell/service/upower/upowertest"
)

const (
	mousePath  = dbus.ObjectPath("/org/freedesktop/UPower/devices/mouse_hidpp_0")
	acPath     = dbus.ObjectPath("/org/freedesktop/UPower/devices/line_power_AC")
	bat0Path   = dbus.ObjectPath("/org/freedesktop/UPower/devices/battery_BAT0")
	upsPath    = dbus.ObjectPath("/org/freedesktop/UPower/devices/ups_hiddev0")
	absentPath = dbus.ObjectPath("/org/freedesktop/UPower/devices/keyboard_1")
)

func TestPeripheralsKeepOnlyPeripherals(t *testing.T) {
	bus := dbustest.Start(t)
	fake := upowertest.Serve(t, bus.Conn(t), upowertest.Battery(80, upower.StateDischarging, upower.WarningNone))
	fake.Add(mousePath, upowertest.Peripheral(upower.KindMouse, "MX Master 3", 5, upower.WarningLow))
	fake.Add(upsPath, upowertest.Peripheral(upower.KindUPS, "", 100, upower.WarningNone))
	fake.Add(acPath, map[string]dbus.Variant{"Type": dbus.MakeVariant(uint32(upower.KindLinePower)), "IsPresent": dbus.MakeVariant(true)})
	laptop := upowertest.Peripheral(upower.KindBattery, "Internal", 80, upower.WarningNone)
	laptop["PowerSupply"] = dbus.MakeVariant(true)
	fake.Add(bat0Path, laptop)
	absent := upowertest.Peripheral(upower.KindKeyboard, "K380", 50, upower.WarningNone)
	absent["IsPresent"] = dbus.MakeVariant(false)
	fake.Add(absentPath, absent)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := upower.NewOn(bus.Conn(t)).Peripherals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []upower.Peripheral{
		{Path: mousePath, Kind: upower.KindMouse, Model: "MX Master 3", Percentage: 5, State: upower.StateDischarging, WarningLevel: upower.WarningLow},
		{Path: upsPath, Kind: upower.KindUPS, Percentage: 100, State: upower.StateDischarging, WarningLevel: upower.WarningNone},
	}
	if len(got) != len(want) {
		t.Fatalf("Peripherals = %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("peripheral %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestPeripheralsWithoutUPowerIsAnError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := upower.NewOn(dbustest.Start(t).Conn(t)).Peripherals(ctx); err == nil {
		t.Fatal("Peripherals without UPower succeeded")
	}
}

func TestSubscribePeripheralsTicksOnDeviceChangesOnly(t *testing.T) {
	bus := dbustest.Start(t)
	fake := upowertest.Serve(t, bus.Conn(t), upowertest.Battery(80, upower.StateDischarging, upower.WarningNone))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ticks, stop, err := upower.NewOn(bus.Conn(t)).SubscribePeripherals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	expect := func(what string) {
		t.Helper()
		select {
		case <-ticks:
		case <-ctx.Done():
			t.Fatalf("no tick for %s", what)
		}
	}
	fake.Add(mousePath, upowertest.Peripheral(upower.KindMouse, "M", 40, upower.WarningNone))
	expect("DeviceAdded")
	fake.Change(mousePath, upowertest.Peripheral(upower.KindMouse, "M", 39, upower.WarningNone))
	expect("a peripheral's PropertiesChanged")
	fake.Remove(mousePath)
	expect("DeviceRemoved")
	fake.SetDisplay(upowertest.Battery(79, upower.StateDischarging, upower.WarningNone))
	select {
	case <-ticks:
		t.Error("a DisplayDevice change ticked the peripherals")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestSubscribeTicksWhenUPowerAppears(t *testing.T) {
	bus := dbustest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sys := upower.NewOn(bus.Conn(t))
	display, stopDisplay, err := sys.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stopDisplay()
	peripherals, stopPeripherals, err := sys.SubscribePeripherals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer stopPeripherals()
	upowertest.Serve(t, bus.Conn(t), upowertest.Battery(50, upower.StateDischarging, upower.WarningNone))
	for name, ticks := range map[string]<-chan struct{}{"battery": display, "peripherals": peripherals} {
		select {
		case <-ticks:
		case <-ctx.Done():
			t.Fatalf("UPower appeared without a %s tick", name)
		}
	}
}

func TestKindFromUint32(t *testing.T) {
	for raw, want := range map[uint32]upower.DeviceKind{
		1: upower.KindLinePower, 2: upower.KindBattery, 5: upower.KindMouse, 12: upower.KindGamingInput,
		28: upower.KindBluetoothGeneric, 29: upower.KindUnknown, 1000: upower.KindUnknown,
	} {
		if got := upower.KindFromUint32(raw); got != want {
			t.Errorf("KindFromUint32(%d) = %v, want %v", raw, got, want)
		}
	}
}
