package udisks_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal/bussignaltest"
	"github.com/stubbedev/lys-shell/service/udisks"
	"github.com/stubbedev/lys-shell/service/udisks/udiskstest"
)

const (
	stick     = dbus.ObjectPath("/org/freedesktop/UDisks2/drives/stick")
	disk      = dbus.ObjectPath("/org/freedesktop/UDisks2/drives/disk")
	stickPart = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1")
	vault     = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdc1")
	root      = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda2")
	hidden    = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda3")
)

func fixture(t *testing.T) (*udiskstest.UDisks, *udisks.Client) {
	t.Helper()
	u := udiskstest.Start(t)
	u.AddDrive(t, stick, udiskstest.Drive{Removable: true, CanPowerOff: true})
	u.AddDrive(t, disk, udiskstest.Drive{})
	u.AddVolume(t, stickPart, udiskstest.Volume{Label: "STICK", Size: 16_000_000_000, Drive: stick})
	u.AddVolume(t, vault, udiskstest.Volume{Size: 500_000_000_000, Drive: stick, Encrypted: true})
	u.AddVolume(t, root, udiskstest.Volume{Label: "root", Drive: disk, HintSystem: true})
	u.AddVolume(t, hidden, udiskstest.Volume{Label: "swap", Drive: disk, HintIgnore: true})
	return u, udisks.New(u.Client(t))
}

func volumes(t *testing.T, c *udisks.Client) []udisks.Volume {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	vols, err := c.Volumes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return vols
}

func find(vols []udisks.Volume, object dbus.ObjectPath) (udisks.Volume, bool) {
	i := slices.IndexFunc(vols, func(v udisks.Volume) bool { return v.Object == object })
	if i < 0 {
		return udisks.Volume{}, false
	}
	return vols[i], true
}

// A removable filesystem and a locked container are listed, labelled
// by their label or size; system and ignored devices never are.
func TestVolumesListWhatTheUserSees(t *testing.T) {
	_, c := fixture(t)
	vols := volumes(t, c)
	if len(vols) != 2 {
		t.Fatalf("volumes %+v, want the stick and the vault", vols)
	}
	s, _ := find(vols, stickPart)
	if s.Label != "STICK" || !s.Removable || s.Locked || s.Mounted() || s.Icon != "" {
		t.Errorf("the stick reads %+v", s)
	}
	v, _ := find(vols, vault)
	if v.Label != "500 GB Encrypted Volume" || !v.Locked {
		t.Errorf("the vault reads %+v", v)
	}
	for _, p := range []dbus.ObjectPath{root, hidden} {
		if _, ok := find(vols, p); ok {
			t.Errorf("%s is listed", p)
		}
	}
}

// Mounting answers where; unlocking with the wrong passphrase fails
// and with the right one hands over the cleartext device, the locked
// container giving way to it; the passphrase is zeroed either way.
func TestMountAndUnlock(t *testing.T) {
	u, c := fixture(t)
	ctx := context.Background()
	at, err := c.Mount(ctx, stickPart)
	if err != nil || !strings.HasPrefix(at, u.MountRoot) {
		t.Fatalf("mount = %q, %v", at, err)
	}
	if s, _ := find(volumes(t, c), stickPart); !slices.Equal(s.MountPoints, []string{at}) {
		t.Errorf("after mounting the stick reads %+v", s)
	}
	wrong := []byte("wrong horse")
	if _, err := c.Unlock(ctx, vault, wrong); err == nil || !strings.Contains(udisks.Message(err), "Operation not permitted") {
		t.Errorf("a wrong passphrase = %v", err)
	}
	right := []byte(u.Passphrase)
	inner, err := c.Unlock(ctx, vault, right)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Trim(string(wrong), "\x00") != "" || strings.Trim(string(right), "\x00") != "" {
		t.Errorf("passphrases left in memory: %q %q", wrong, right)
	}
	vols := volumes(t, c)
	if _, ok := find(vols, vault); ok {
		t.Error("the unlocked container is still listed")
	}
	if v, ok := find(vols, inner); !ok || v.Locked || !v.Removable {
		t.Errorf("the cleartext device reads %+v (%v)", v, ok)
	}
}

// Eject unmounts, locks what was unlocked and powers the drive off; a
// busy filesystem is ErrBusy and leaves the drive powered.
func TestEject(t *testing.T) {
	u, c := fixture(t)
	ctx := context.Background()
	if _, err := c.Mount(ctx, stickPart); err != nil {
		t.Fatal(err)
	}
	s, _ := find(volumes(t, c), stickPart)
	u.Busy.Store(true)
	if err := c.Eject(ctx, s); !errors.Is(err, udisks.ErrBusy) {
		t.Errorf("a busy eject = %v, want ErrBusy", err)
	}
	if slices.Contains(u.Calls.Load(), udiskstest.CallPowerOff+" "+string(stick)) {
		t.Error("a busy volume's drive was powered off")
	}
	u.Busy.Store(false)
	u.Calls.Store(nil)
	if err := c.Eject(ctx, s); err != nil {
		t.Fatal(err)
	}
	want := []string{udiskstest.CallUnmount + " " + string(stickPart), udiskstest.CallPowerOff + " " + string(stick)}
	if got := u.Calls.Load(); !slices.Equal(got, want) {
		t.Errorf("eject called %v, want %v", got, want)
	}
	inner, err := c.Unlock(ctx, vault, []byte(u.Passphrase))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Mount(ctx, inner); err != nil {
		t.Fatal(err)
	}
	v, _ := find(volumes(t, c), inner)
	u.Calls.Store(nil)
	if err := c.Eject(ctx, v); err != nil {
		t.Fatal(err)
	}
	want = []string{udiskstest.CallUnmount + " " + string(inner), udiskstest.CallLock + " " + string(vault), udiskstest.CallPowerOff + " " + string(stick)}
	if got := u.Calls.Load(); !slices.Equal(got, want) {
		t.Errorf("ejecting the unlocked vault called %v, want %v", got, want)
	}
}

// Watch reports a device coming and going until stopped.
func TestWatchFollowsHotplug(t *testing.T) {
	u, c := fixture(t)
	changed := make(chan struct{}, 16)
	stop, err := c.Watch(func() { changed <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	wait := func(what string) {
		t.Helper()
		select {
		case <-changed:
		case <-time.After(5 * time.Second):
			t.Fatalf("no change for %s", what)
		}
	}
	u.Remove(t, stickPart)
	wait("the removal")
	if _, ok := find(volumes(t, c), stickPart); ok {
		t.Error("the removed stick is still listed")
	}
	u.AddVolume(t, stickPart, udiskstest.Volume{Label: "STICK", Drive: stick})
	wait("the addition")
	stop()
	for len(changed) > 0 {
		<-changed
	}
	u.Remove(t, stickPart)
	select {
	case <-changed:
		t.Error("a change after stop was reported")
	case <-time.After(100 * time.Millisecond):
	}
}

// The watch reads UDisks2's signals off a channel of its own and keeps
// none of the descriptors one carries: each is closed once the signal
// is handled, and those of the signals unread when it stops are closed
// with it.
func TestWatchClosesTheDescriptorsOfTheSignalsItReads(t *testing.T) {
	u := udiskstest.Start(t)
	conn := u.Client(t)
	c := udisks.New(conn)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	stop, err := c.Watch(func() {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	if err != nil {
		t.Fatal(err)
	}
	change := func(fd dbus.UnixFD) {
		u.Emit(t, stickPart, "org.freedesktop.DBus.Properties.PropertiesChanged",
			udisks.BlockIface, map[string]dbus.Variant{"Handed": dbus.MakeVariant(fd)}, []string{})
	}
	handled, handledClosed := bussignaltest.Descriptor(t)
	change(handled)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the watch did not report the change")
	}
	unread, unreadClosed := bussignaltest.Descriptor(t)
	change(unread)
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetId", 0).Err; err != nil {
		t.Fatalf("wait for the connection to read the signal: %v", err)
	}
	stop()
	unreadClosed("the watch stopped with a signal unread")
	close(release)
	handledClosed("the watch handled a property change carrying a descriptor")
}
