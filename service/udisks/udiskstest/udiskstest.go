// Package udiskstest is a fake UDisks2 on a private bus: drives and
// their volumes a test adds and removes, mounted into directories the
// test owns, so nothing on the host is ever mounted.
package udiskstest

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/udisks"
)

const (
	errFailed = udisks.BusName + ".Error.Failed"
)

// The calls Calls records, each followed by a space and the object.
const (
	CallMount    = "Mount"
	CallUnmount  = "Unmount"
	CallUnlock   = "Unlock"
	CallLock     = "Lock"
	CallPowerOff = "PowerOff"
	CallEject    = "Eject"
)

// UDisks is the fake.
type UDisks struct {
	// Bus is the private bus the fake owns its name on.
	Bus *dbustest.Bus
	// Calls lists the method calls, "Mount <object>" and the like, in
	// order.
	Calls dbustest.Var[[]string]
	// Busy makes every Unmount fail as a filesystem in use.
	Busy dbustest.Var[bool]
	// Passphrase is the passphrase every container unlocks with.
	Passphrase string
	// MountRoot is where volumes mount, a directory each by label.
	MountRoot string
	conn      *dbus.Conn
	mu        sync.Mutex
	objs      map[dbus.ObjectPath]map[string]map[string]dbus.Variant
}

// Start runs the fake on a private bus.
func Start(t *testing.T) *UDisks {
	t.Helper()
	u := &UDisks{Bus: dbustest.Start(t), Passphrase: "correct horse", MountRoot: t.TempDir(), objs: map[dbus.ObjectPath]map[string]map[string]dbus.Variant{}}
	u.conn = u.Bus.Conn(t)
	if err := u.conn.Export(manager{u}, udisks.ManagerPath, dbusx.ObjectManagerInterface); err != nil {
		t.Fatalf("udiskstest: export the manager: %v", err)
	}
	if reply, err := u.conn.RequestName(udisks.BusName, dbus.NameFlagDoNotQueue); err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("udiskstest: own %s: %v (%v)", udisks.BusName, reply, err)
	}
	return u
}

// Client is a connection to the fake's bus, for udisks.New.
func (u *UDisks) Client(t *testing.T) *dbus.Conn { return u.Bus.Conn(t) }

// Emit sends the signal name from UDisks2's object at path, carrying
// args, and returns once the bus has routed it.
func (u *UDisks) Emit(t *testing.T, path dbus.ObjectPath, name string, args ...any) {
	t.Helper()
	if err := u.conn.Emit(path, name, args...); err != nil {
		t.Fatalf("udiskstest: emit %s at %s: %v", name, path, err)
	}
	if err := u.conn.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatalf("udiskstest: wait for the bus to route %s: %v", name, err)
	}
}

// Drive is a drive the test adds.
type Drive struct {
	Removable, Ejectable, CanPowerOff bool
}

// Volume is a block device the test adds: a filesystem, or an
// encrypted container when Encrypted.
type Volume struct {
	Label, HintName string
	Size            uint64
	Drive           dbus.ObjectPath
	Encrypted       bool
	HintSystem      bool
	HintIgnore      bool
}

// AddDrive adds a drive at path.
func (u *UDisks) AddDrive(t *testing.T, path dbus.ObjectPath, d Drive) {
	t.Helper()
	u.add(t, path, map[string]map[string]dbus.Variant{udisks.DriveIface: {
		"Removable": dbus.MakeVariant(d.Removable), "Ejectable": dbus.MakeVariant(d.Ejectable),
		"CanPowerOff": dbus.MakeVariant(d.CanPowerOff),
	}})
}

// AddVolume adds a block device at path.
func (u *UDisks) AddVolume(t *testing.T, path dbus.ObjectPath, v Volume) {
	t.Helper()
	ifaces := map[string]map[string]dbus.Variant{udisks.BlockIface: {
		"IdLabel": dbus.MakeVariant(v.Label), "HintName": dbus.MakeVariant(v.HintName),
		"Size": dbus.MakeVariant(v.Size), "Drive": dbus.MakeVariant(drivePath(v.Drive)),
		"HintSystem": dbus.MakeVariant(v.HintSystem), "HintIgnore": dbus.MakeVariant(v.HintIgnore),
		"CryptoBackingDevice": dbus.MakeVariant(dbus.ObjectPath("/")),
	}}
	if v.Encrypted {
		ifaces[udisks.EncryptedIface] = map[string]dbus.Variant{"CleartextDevice": dbus.MakeVariant(dbus.ObjectPath("/"))}
	} else {
		ifaces[udisks.FilesystemIface] = map[string]dbus.Variant{"MountPoints": dbus.MakeVariant([][]byte{})}
	}
	u.add(t, path, ifaces)
}

func drivePath(p dbus.ObjectPath) dbus.ObjectPath {
	if p == "" {
		return "/"
	}
	return p
}

// Remove removes the object at path, as unplugging it does.
func (u *UDisks) Remove(t *testing.T, path dbus.ObjectPath) {
	t.Helper()
	if err := u.remove(path); err != nil {
		t.Errorf("udiskstest: remove %s: %v", path, err)
	}
}

func (u *UDisks) remove(path dbus.ObjectPath) error {
	u.mu.Lock()
	ifaces := u.objs[path]
	delete(u.objs, path)
	u.mu.Unlock()
	names := make([]string, 0, len(ifaces))
	for name := range ifaces {
		names = append(names, name)
		if err := u.conn.Export(nil, path, name); err != nil {
			return err
		}
	}
	slices.Sort(names)
	return dbusx.InterfacesRemoved.Emit(u.conn, udisks.ManagerPath, dbusx.RemovedInterfaces{Object: path, Interfaces: names})
}

func (u *UDisks) add(t *testing.T, path dbus.ObjectPath, ifaces map[string]map[string]dbus.Variant) {
	t.Helper()
	if err := u.export(path, ifaces); err != nil {
		t.Fatalf("udiskstest: export %s: %v", path, err)
	}
}

func (u *UDisks) export(path dbus.ObjectPath, ifaces map[string]map[string]dbus.Variant) error {
	u.mu.Lock()
	u.objs[path] = ifaces
	u.mu.Unlock()
	for name := range ifaces {
		var methods any
		switch name {
		case udisks.FilesystemIface:
			methods = filesystem{u, path}
		case udisks.EncryptedIface:
			methods = encrypted{u, path}
		case udisks.DriveIface:
			methods = drive{u, path}
		default:
			continue
		}
		if err := u.conn.Export(methods, path, name); err != nil {
			return err
		}
	}
	return dbusx.InterfacesAdded.Emit(u.conn, udisks.ManagerPath, dbusx.AddedInterfaces{Object: path, Interfaces: ifaces})
}

func (u *UDisks) record(call string, path dbus.ObjectPath) {
	u.Calls.Update(func(c []string) []string { return append(c, call+" "+string(path)) })
}

// set changes a property and announces it.
func (u *UDisks) set(path dbus.ObjectPath, iface, key string, value dbus.Variant) *dbus.Error {
	u.mu.Lock()
	props, ok := u.objs[path][iface]
	if ok {
		props[key] = value
	}
	u.mu.Unlock()
	if !ok {
		return dbus.NewError(errFailed, []any{"no " + iface + " at " + string(path)})
	}
	if err := dbusx.PropertiesChanged.Emit(u.conn, path, dbusx.ChangedProperties{Interface: iface, Changed: map[string]dbus.Variant{key: value}, Invalidated: []string{}}); err != nil {
		return dbus.MakeFailedError(err)
	}
	return nil
}

type manager struct{ u *UDisks }

// GetManagedObjects answers every object and its properties.
func (m manager) GetManagedObjects() (map[dbus.ObjectPath]map[string]map[string]dbus.Variant, *dbus.Error) {
	m.u.mu.Lock()
	defer m.u.mu.Unlock()
	out := make(map[dbus.ObjectPath]map[string]map[string]dbus.Variant, len(m.u.objs))
	for path, ifaces := range m.u.objs {
		copied := make(map[string]map[string]dbus.Variant, len(ifaces))
		for name, props := range ifaces {
			copied[name] = maps.Clone(props)
		}
		out[path] = copied
	}
	return out, nil
}

type filesystem struct {
	u    *UDisks
	path dbus.ObjectPath
}

// Mount "mounts" the volume on a directory under MountRoot.
func (f filesystem) Mount(map[string]dbus.Variant) (string, *dbus.Error) {
	f.u.record(CallMount, f.path)
	f.u.mu.Lock()
	label, _ := f.u.objs[f.path][udisks.BlockIface]["IdLabel"].Value().(string)
	f.u.mu.Unlock()
	at := filepath.Join(f.u.MountRoot, label+filepath.Base(string(f.path)))
	if err := os.MkdirAll(at, 0o700); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return at, f.u.set(f.path, udisks.FilesystemIface, "MountPoints", dbus.MakeVariant([][]byte{append([]byte(at), 0)}))
}

// Unmount unmounts it, unless Busy.
func (f filesystem) Unmount(map[string]dbus.Variant) *dbus.Error {
	f.u.record(CallUnmount, f.path)
	if f.u.Busy.Load() {
		return dbus.NewError(udisks.ErrDeviceBusy, []any{"Error unmounting: target is busy"})
	}
	return f.u.set(f.path, udisks.FilesystemIface, "MountPoints", dbus.MakeVariant([][]byte{}))
}

type encrypted struct {
	u    *UDisks
	path dbus.ObjectPath
}

// cleartext is the cleartext device an unlock makes for path.
func cleartext(path dbus.ObjectPath) dbus.ObjectPath { return path + "_clear" }

// Unlock makes the cleartext device, with Passphrase only.
func (e encrypted) Unlock(passphrase string, _ map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	e.u.record(CallUnlock, e.path)
	if passphrase != e.u.Passphrase {
		return "", dbus.NewError(errFailed, []any{"Error unlocking " + string(e.path) + ": Failed to activate device: Operation not permitted"})
	}
	e.u.mu.Lock()
	outer := e.u.objs[e.path][udisks.BlockIface]
	label, _ := outer["IdLabel"].Value().(string)
	e.u.mu.Unlock()
	inner := cleartext(e.path)
	if err := e.u.export(inner, map[string]map[string]dbus.Variant{
		udisks.BlockIface: {
			"IdLabel": dbus.MakeVariant(label + " (unlocked)"), "Drive": dbus.MakeVariant(dbus.ObjectPath("/")),
			"CryptoBackingDevice": dbus.MakeVariant(e.path),
		},
		udisks.FilesystemIface: {"MountPoints": dbus.MakeVariant([][]byte{})},
	}); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return inner, e.u.set(e.path, udisks.EncryptedIface, "CleartextDevice", dbus.MakeVariant(inner))
}

// Lock removes the cleartext device.
func (e encrypted) Lock(map[string]dbus.Variant) *dbus.Error {
	e.u.record(CallLock, e.path)
	if err := e.u.remove(cleartext(e.path)); err != nil {
		return dbus.MakeFailedError(err)
	}
	return e.u.set(e.path, udisks.EncryptedIface, "CleartextDevice", dbus.MakeVariant(dbus.ObjectPath("/")))
}

type drive struct {
	u    *UDisks
	path dbus.ObjectPath
}

// PowerOff records the call.
func (d drive) PowerOff(map[string]dbus.Variant) *dbus.Error {
	d.u.record(CallPowerOff, d.path)
	return nil
}

// Eject records the call.
func (d drive) Eject(map[string]dbus.Variant) *dbus.Error {
	d.u.record(CallEject, d.path)
	return nil
}
