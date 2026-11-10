// Package udisks lists the system's volumes through UDisks2 over the
// system D-Bus, mounted or not, and mounts, unlocks and ejects them as
// the user, as GVfs's udisks2 volume monitor does for GTK's places
// sidebar. Polkit lets an active local session do all of it.
package udisks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unsafe"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
)

// The UDisks2 names a client talks to.
const (
	BusName         = "org.freedesktop.UDisks2"
	ManagerPath     = dbus.ObjectPath("/org/freedesktop/UDisks2")
	BlockIface      = BusName + ".Block"
	FilesystemIface = BusName + ".Filesystem"
	EncryptedIface  = BusName + ".Encrypted"
	DriveIface      = BusName + ".Drive"
	objectManager   = "org.freedesktop.DBus.ObjectManager"
	propertiesIface = "org.freedesktop.DBus.Properties"
	// ErrDeviceBusy is UDisks2's error for a filesystem in use.
	ErrDeviceBusy = BusName + ".Error.DeviceBusy"
)

// noObject is D-Bus's null object path, UDisks2's "none".
const noObject = dbus.ObjectPath("/")

// ErrBusy is an unmount UDisks2 refused because the filesystem is in
// use.
var ErrBusy = errors.New("udisks: the volume is in use")

// Volume is one volume the user may see: a filesystem, or an encrypted
// container still locked. Object is the block device to mount, the
// cleartext device of an unlocked container; MountPoints are where it
// is mounted now; Icon is the icon UDisks2 hints, "" for none.
// Removable volumes can be ejected.
type Volume struct {
	Object      dbus.ObjectPath
	Label       string
	Size        uint64
	Icon        string
	MountPoints []string
	Locked      bool
	Removable   bool
	// container is the encrypted device a cleartext device unlocks,
	// noObject for one that is not; drive is the drive the volume is
	// on, and powerOff whether it is ejected by powering it off (else
	// by ejecting its media).
	container dbus.ObjectPath
	drive     dbus.ObjectPath
	powerOff  bool
}

// Mounted reports a volume mounted somewhere.
func (v Volume) Mounted() bool { return len(v.MountPoints) > 0 }

// Client is a UDisks2 client on a system bus connection the caller
// owns.
type Client struct {
	conn *dbus.Conn
}

// New is a client on conn.
func New(conn *dbus.Conn) *Client { return &Client{conn: conn} }

// objects is GetManagedObjects' answer.
type objects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant

// Volumes lists the volumes UDisks2 has, by object path.
func (c *Client) Volumes(ctx context.Context) ([]Volume, error) {
	var objs objects
	if err := c.conn.Object(BusName, ManagerPath).CallWithContext(ctx, objectManager+".GetManagedObjects", 0).Store(&objs); err != nil {
		return nil, fmt.Errorf("udisks: listing objects: %w", err)
	}
	return volumesOf(objs), nil
}

// volumesOf picks the volumes a user sees from UDisks2's objects:
// block devices with a filesystem or a locked encrypted container,
// neither marked to ignore nor a system device.
func volumesOf(objs objects) []Volume {
	var vols []Volume
	for path, ifaces := range objs {
		block, ok := ifaces[BlockIface]
		if !ok || boolOf(block, "HintIgnore") || boolOf(block, "HintSystem") {
			continue
		}
		v := Volume{Object: path, Size: uint64Of(block, "Size"), container: pathOf(block, "CryptoBackingDevice"), drive: pathOf(block, "Drive")}
		_, isFS := ifaces[FilesystemIface]
		_, isCrypto := ifaces[EncryptedIface]
		switch {
		case isCrypto && !unlocked(objs, path, ifaces):
			v.Locked = true
		case isFS:
			v.MountPoints = bytesListOf(ifaces[FilesystemIface], "MountPoints")
		default:
			continue
		}
		if v.container != noObject {
			if outer, ok := objs[v.container][BlockIface]; ok {
				if boolOf(outer, "HintIgnore") || boolOf(outer, "HintSystem") {
					continue
				}
				v.drive = pathOf(outer, "Drive")
			}
		}
		if drive, ok := objs[v.drive][DriveIface]; ok {
			v.powerOff = boolOf(drive, "CanPowerOff")
			v.Removable = boolOf(drive, "Removable") && (v.powerOff || boolOf(drive, "Ejectable"))
		}
		v.Label = cmp.Or(stringOf(block, "IdLabel"), stringOf(block, "HintName"), sizeLabel(v))
		v.Icon = cmp.Or(stringOf(block, "HintSymbolicIconName"), stringOf(block, "HintIconName"))
		vols = append(vols, v)
	}
	slices.SortFunc(vols, func(a, b Volume) int { return cmp.Compare(a.Object, b.Object) })
	return vols
}

// unlocked reports an encrypted container whose cleartext device is
// there: its CleartextDevice, or, before UDisks2 had that property, a
// block device it backs.
func unlocked(objs objects, path dbus.ObjectPath, ifaces map[string]map[string]dbus.Variant) bool {
	if clear := pathOf(ifaces[EncryptedIface], "CleartextDevice"); clear != noObject {
		return true
	}
	for _, other := range objs {
		if b, ok := other[BlockIface]; ok && pathOf(b, "CryptoBackingDevice") == path {
			return true
		}
	}
	return false
}

// sizeLabel names a volume without a label by its size, as GVfs does.
func sizeLabel(v Volume) string {
	kind := "Volume"
	if v.Locked {
		kind = "Encrypted Volume"
	}
	if v.Size == 0 {
		return kind
	}
	return decimalSize(v.Size) + " " + kind
}

// decimalSize is a byte count in the decimal units drives are sold in.
func decimalSize(n uint64) string {
	units := [...]string{"B", "kB", "MB", "GB", "TB", "PB"}
	unit := 0
	whole, rest := n, uint64(0)
	for whole >= 1000 && unit < len(units)-1 {
		whole, rest = whole/1000, whole%1000
		unit++
	}
	if whole < 10 && unit > 0 {
		return strconv.FormatUint(whole, 10) + "." + strconv.FormatUint(rest/100, 10) + " " + units[unit]
	}
	return strconv.FormatUint(whole, 10) + " " + units[unit]
}

// Watch calls changed whenever UDisks2's objects may have changed: one
// added or removed (a hotplug, an unlock), or a property changing (a
// mount), until stop is called.
func (c *Client) Watch(changed func()) (stop func(), err error) {
	rules := [][]dbus.MatchOption{
		{dbus.WithMatchSender(BusName), dbus.WithMatchInterface(objectManager)},
		{dbus.WithMatchSender(BusName), dbus.WithMatchInterface(propertiesIface), dbus.WithMatchMember("PropertiesChanged")},
	}
	for i, rule := range rules {
		if err := c.conn.AddMatchSignal(rule...); err != nil {
			for _, added := range rules[:i] {
				_ = c.conn.RemoveMatchSignal(added...)
			}
			return nil, fmt.Errorf("udisks: watching objects: %w", err)
		}
	}
	signals := make(chan *dbus.Signal, 16)
	c.conn.Signal(signals)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if sig.Name == objectManager+".InterfacesAdded" || sig.Name == objectManager+".InterfacesRemoved" ||
					(sig.Name == propertiesIface+".PropertiesChanged" && udisksObject(sig.Path)) {
					changed()
				}
				bussignal.Discard(sig)
			}
		}
	}()
	return func() {
		for _, rule := range rules {
			_ = c.conn.RemoveMatchSignal(rule...)
		}
		bussignal.Remove(c.conn, signals)
		close(done)
	}, nil
}

// udisksObject reports a path under UDisks2's manager.
func udisksObject(path dbus.ObjectPath) bool {
	return len(path) > len(ManagerPath) && string(path[:len(ManagerPath)+1]) == string(ManagerPath)+"/"
}

// noOptions is the empty option dictionary UDisks2's methods take.
var noOptions = map[string]dbus.Variant{}

// Mount mounts the filesystem on object as the user and reports where.
func (c *Client) Mount(ctx context.Context, object dbus.ObjectPath) (string, error) {
	var at string
	if err := c.conn.Object(BusName, object).CallWithContext(ctx, FilesystemIface+".Mount", 0, noOptions).Store(&at); err != nil {
		return "", fmt.Errorf("udisks: mounting %s: %w", object, err)
	}
	return at, nil
}

// Unlock unlocks the encrypted container on object with passphrase and
// reports its cleartext device. The passphrase goes to the bus straight
// from the slice, which is zeroed once the call is answered.
func (c *Client) Unlock(ctx context.Context, object dbus.ObjectPath, passphrase []byte) (dbus.ObjectPath, error) {
	defer clear(passphrase)
	var secret string
	if len(passphrase) > 0 {
		secret = unsafe.String(&passphrase[0], len(passphrase)) //nolint:gosec // borrowed for the call only, then zeroed
	}
	var clearText dbus.ObjectPath
	if err := c.conn.Object(BusName, object).CallWithContext(ctx, EncryptedIface+".Unlock", 0, secret, noOptions).Store(&clearText); err != nil {
		return "", fmt.Errorf("udisks: unlocking %s: %w", object, err)
	}
	return clearText, nil
}

// Eject unmounts the volume everywhere, locks the container it was
// unlocked from, and powers its drive off or ejects its media. A
// filesystem in use is ErrBusy and leaves the drive as it is.
func (c *Client) Eject(ctx context.Context, v Volume) error {
	if v.Mounted() {
		err := c.conn.Object(BusName, v.Object).CallWithContext(ctx, FilesystemIface+".Unmount", 0, noOptions).Err
		if dbusErr, ok := errors.AsType[dbus.Error](err); ok && dbusErr.Name == ErrDeviceBusy {
			return fmt.Errorf("udisks: unmounting %s: %w: %w", v.Object, ErrBusy, err)
		}
		if err != nil {
			return fmt.Errorf("udisks: unmounting %s: %w", v.Object, err)
		}
	}
	if v.container != noObject {
		if err := c.conn.Object(BusName, v.container).CallWithContext(ctx, EncryptedIface+".Lock", 0, noOptions).Err; err != nil {
			return fmt.Errorf("udisks: locking %s: %w", v.container, err)
		}
	}
	if v.drive == noObject {
		return nil
	}
	method := DriveIface + ".Eject"
	if v.powerOff {
		method = DriveIface + ".PowerOff"
	}
	if err := c.conn.Object(BusName, v.drive).CallWithContext(ctx, method, 0, noOptions).Err; err != nil {
		return fmt.Errorf("udisks: ejecting %s: %w", v.drive, err)
	}
	return nil
}

// Message is what UDisks2 said went wrong in err, for the user; err's
// own text when it was not UDisks2's answer.
func Message(err error) string {
	if dbusErr, ok := errors.AsType[dbus.Error](err); ok && len(dbusErr.Body) > 0 {
		if msg, ok := dbusErr.Body[0].(string); ok && msg != "" {
			return msg
		}
	}
	return err.Error()
}

func boolOf(props map[string]dbus.Variant, key string) bool {
	b, _ := props[key].Value().(bool)
	return b
}

func stringOf(props map[string]dbus.Variant, key string) string {
	s, _ := props[key].Value().(string)
	return s
}

func uint64Of(props map[string]dbus.Variant, key string) uint64 {
	n, _ := props[key].Value().(uint64)
	return n
}

func pathOf(props map[string]dbus.Variant, key string) dbus.ObjectPath {
	if p, ok := props[key].Value().(dbus.ObjectPath); ok && p != "" {
		return p
	}
	return noObject
}

// bytesListOf reads an aay of NUL-terminated paths, as MountPoints is.
func bytesListOf(props map[string]dbus.Variant, key string) []string {
	raw, _ := props[key].Value().([][]byte)
	out := make([]string, 0, len(raw))
	for _, b := range raw {
		if i := slices.Index(b, 0); i >= 0 {
			b = b[:i]
		}
		out = append(out, string(b))
	}
	return out
}
