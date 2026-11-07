// Package upowertest serves a scriptable UPower on a test bus: the
// DisplayDevice and enumerated devices, each change announced with the
// signal the real daemon sends, and every GetAll counted.
package upowertest

import (
	"maps"
	"slices"
	"sync"
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/upower"
)

const (
	managerPath   = dbus.ObjectPath("/org/freedesktop/UPower")
	properties    = "org.freedesktop.DBus.Properties"
	deviceIface   = "org.freedesktop.UPower.Device"
	displayDevice = dbus.ObjectPath(upower.DisplayDevicePath)
)

// Fake is UPower as the test scripts it.
type Fake struct {
	t       testing.TB
	conn    *dbus.Conn
	mu      sync.Mutex
	devices map[dbus.ObjectPath]map[string]dbus.Variant
	reads   map[dbus.ObjectPath]int
}

// Serve claims UPower's name on conn with a DisplayDevice holding
// display and no other device.
func Serve(t testing.TB, conn *dbus.Conn, display map[string]dbus.Variant) *Fake {
	t.Helper()
	f := &Fake{t: t, conn: conn, devices: map[dbus.ObjectPath]map[string]dbus.Variant{}, reads: map[dbus.ObjectPath]int{}}
	if err := conn.ExportMethodTable(map[string]any{"EnumerateDevices": f.enumerate}, managerPath, upower.BusName); err != nil {
		t.Fatalf("upowertest: export the manager: %v", err)
	}
	f.export(displayDevice, display)
	if reply, err := conn.RequestName(upower.BusName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("upowertest: claim %s: %v %v", upower.BusName, reply, err)
	}
	return f
}

func (f *Fake) enumerate() ([]dbus.ObjectPath, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	paths := slices.Sorted(maps.Keys(f.devices))
	return slices.DeleteFunc(paths, func(p dbus.ObjectPath) bool { return p == displayDevice }), nil
}

func (f *Fake) export(path dbus.ObjectPath, props map[string]dbus.Variant) {
	f.t.Helper()
	f.mu.Lock()
	f.devices[path] = maps.Clone(props)
	f.mu.Unlock()
	getAll := func(iface string) (map[string]dbus.Variant, *dbus.Error) {
		if iface != deviceIface {
			return nil, dbus.MakeFailedError(dbus.ErrMsgUnknownInterface)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reads[path]++
		return maps.Clone(f.devices[path]), nil
	}
	if err := f.conn.ExportMethodTable(map[string]any{"GetAll": getAll}, path, properties); err != nil {
		f.t.Fatalf("upowertest: export %s: %v", path, err)
	}
}

// SetDisplay replaces the DisplayDevice's properties and announces the
// change.
func (f *Fake) SetDisplay(props map[string]dbus.Variant) { f.Change(displayDevice, props) }

// Change replaces an existing device's properties and emits its
// PropertiesChanged.
func (f *Fake) Change(path dbus.ObjectPath, props map[string]dbus.Variant) {
	f.t.Helper()
	f.mu.Lock()
	_, known := f.devices[path]
	f.devices[path] = maps.Clone(props)
	f.mu.Unlock()
	if !known {
		f.t.Fatalf("upowertest: change of unknown device %s", path)
	}
	if err := f.conn.Emit(path, properties+".PropertiesChanged", deviceIface, props, []string{}); err != nil {
		f.t.Fatalf("upowertest: emit PropertiesChanged on %s: %v", path, err)
	}
}

// Add exports a device and emits DeviceAdded.
func (f *Fake) Add(path dbus.ObjectPath, props map[string]dbus.Variant) {
	f.t.Helper()
	f.export(path, props)
	if err := f.conn.Emit(managerPath, upower.BusName+".DeviceAdded", path); err != nil {
		f.t.Fatalf("upowertest: emit DeviceAdded %s: %v", path, err)
	}
}

// Remove withdraws a device and emits DeviceRemoved.
func (f *Fake) Remove(path dbus.ObjectPath) {
	f.t.Helper()
	f.mu.Lock()
	delete(f.devices, path)
	f.mu.Unlock()
	if err := f.conn.Export(nil, path, properties); err != nil {
		f.t.Fatalf("upowertest: unexport %s: %v", path, err)
	}
	if err := f.conn.Emit(managerPath, upower.BusName+".DeviceRemoved", path); err != nil {
		f.t.Fatalf("upowertest: emit DeviceRemoved %s: %v", path, err)
	}
}

// Reads is how many GetAll calls path has answered.
func (f *Fake) Reads(path dbus.ObjectPath) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[path]
}

// Battery is a DisplayDevice's properties.
func Battery(percentage float64, state upower.DeviceState, warning upower.WarningLevel) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"IsPresent":    dbus.MakeVariant(true),
		"Percentage":   dbus.MakeVariant(percentage),
		"State":        dbus.MakeVariant(uint32(state)),
		"WarningLevel": dbus.MakeVariant(uint32(warning)),
	}
}

// Peripheral is an enumerated device's properties.
func Peripheral(kind upower.DeviceKind, model string, percentage float64, warning upower.WarningLevel) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"Type":         dbus.MakeVariant(uint32(kind)),
		"IsPresent":    dbus.MakeVariant(true),
		"PowerSupply":  dbus.MakeVariant(false),
		"Model":        dbus.MakeVariant(model),
		"Percentage":   dbus.MakeVariant(percentage),
		"State":        dbus.MakeVariant(uint32(upower.StateDischarging)),
		"WarningLevel": dbus.MakeVariant(uint32(warning)),
	}
}
