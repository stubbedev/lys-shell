package filechooser

import (
	"slices"
	"strings"
	"testing"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/udisks"
	"github.com/stubbedev/lys-shell/service/udisks/udiskstest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

const (
	stickDrive = dbus.ObjectPath("/org/freedesktop/UDisks2/drives/stick")
	diskDrive  = dbus.ObjectPath("/org/freedesktop/UDisks2/drives/disk")
	stickFS    = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1")
	vaultFS    = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdc1")
	rootFS     = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda2")
	swapFS     = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sda3")
)

// drivesHarness is a chooser whose Locations list a fake UDisks2's
// volumes: a USB stick, a locked encrypted volume on it, and a system
// disk's root and swap.
func drivesHarness(t *testing.T) (*harness, *udiskstest.UDisks) {
	t.Helper()
	hs := newHarness(t, "")
	u := udiskstest.Start(t)
	u.AddDrive(t, stickDrive, udiskstest.Drive{Removable: true, CanPowerOff: true})
	u.AddDrive(t, diskDrive, udiskstest.Drive{})
	u.AddVolume(t, stickFS, udiskstest.Volume{Label: "STICK", Drive: stickDrive})
	u.AddVolume(t, vaultFS, udiskstest.Volume{Label: "Vault", Drive: stickDrive, Encrypted: true})
	u.AddVolume(t, rootFS, udiskstest.Volume{Label: "root", Drive: diskDrive, HintSystem: true})
	u.AddVolume(t, swapFS, udiskstest.Volume{Label: "swap", Drive: diskDrive, HintIgnore: true})
	client := udisks.New(u.Client(t))
	hs.c.d.Drives = func() *udisks.Client { return client }
	return hs, u
}

// waitButton waits until a button named name is there, or gone.
func (hs *harness) waitButton(t *testing.T, name string, there bool) {
	t.Helper()
	for deadline := time.Now().Add(treetest.Liveness); hs.hasButton(name) != there; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the %q button never became there=%v", name, there)
		}
	}
}

// An unmounted volume is listed, and activating it mounts it and opens
// where; system and ignored volumes never are; a removed device's row
// goes.
func TestDrivesMountOnActivation(t *testing.T) {
	hs, u := drivesHarness(t)
	got := hs.open(t, OpenRequest{})
	hs.waitButton(t, "STICK", true)
	for _, name := range []string{"root", "swap"} {
		if hs.hasButton(name) {
			t.Errorf("the %s volume is listed", name)
		}
	}
	hs.click(t, "STICK")
	if calls := u.Calls.Load(); !slices.Equal(calls, []string{udiskstest.CallMount + " " + string(stickFS)}) {
		t.Errorf("activating the stick called %v", calls)
	}
	if dir := hs.dir(); !strings.HasPrefix(dir, u.MountRoot) {
		t.Errorf("after mounting the chooser shows %s, want the mount under %s", dir, u.MountRoot)
	}
	u.Remove(t, stickFS)
	hs.waitButton(t, "STICK", false)
	hs.click(t, "Cancel")
	answer(t, got)
}

// A locked volume asks for its passphrase: a wrong one shows UDisks2's
// answer and keeps the prompt open, the right one unlocks, mounts and
// opens it.
func TestDrivesUnlockWithAPassphrase(t *testing.T) {
	hs, u := drivesHarness(t)
	got := hs.open(t, OpenRequest{})
	hs.waitButton(t, "Vault", true)
	hs.click(t, "Vault")
	var prompt bool
	hs.run(func() { prompt = hs.c.ui.unlock.box.Visible() && hs.windows[0].focus == hs.c.ui.unlock.entry })
	if !prompt {
		t.Fatal("the locked volume asked no passphrase")
	}
	hs.do(func() { hs.c.ui.unlock.entry.SetText("wrong horse") })
	hs.click(t, "Unlock")
	var errText string
	var open bool
	hs.run(func() {
		errText = hs.c.ui.unlock.err.Text()
		open = hs.c.ui.unlock.box.Visible() && hs.c.ui.unlock.entry.Text() == ""
	})
	if !strings.Contains(errText, "Operation not permitted") || !open {
		t.Errorf("a wrong passphrase: error %q, prompt open and emptied %v", errText, open)
	}
	pending(t, got)
	hs.do(func() { hs.c.ui.unlock.entry.SetText(u.Passphrase) })
	hs.click(t, "Unlock")
	want := []string{udiskstest.CallUnlock + " " + string(vaultFS), udiskstest.CallUnlock + " " + string(vaultFS), udiskstest.CallMount + " " + string(vaultFS) + "_clear"}
	if calls := u.Calls.Load(); !slices.Equal(calls, want) {
		t.Errorf("unlocking called %v, want %v", calls, want)
	}
	if dir := hs.dir(); !strings.HasPrefix(dir, u.MountRoot) {
		t.Errorf("after unlocking the chooser shows %s", dir)
	}
	hs.click(t, "Cancel")
	answer(t, got)
}

// Eject unmounts and powers the drive off, leaving the folder it
// showed; a busy volume says so and stays powered.
func TestDrivesEject(t *testing.T) {
	hs, u := drivesHarness(t)
	got := hs.open(t, OpenRequest{})
	hs.waitButton(t, "STICK", true)
	hs.click(t, "STICK")
	eject := "Eject “STICK”"
	hs.waitButton(t, eject, true)
	u.Calls.Store(nil)
	u.Busy.Store(true)
	hs.click(t, eject)
	var notice string
	hs.run(func() { notice = hs.c.ui.noticeLabel.Text() })
	if !strings.Contains(notice, "in use") || slices.Contains(u.Calls.Load(), udiskstest.CallPowerOff+" "+string(stickDrive)) {
		t.Errorf("a busy eject: notice %q, calls %v", notice, u.Calls.Load())
	}
	u.Busy.Store(false)
	u.Calls.Store(nil)
	hs.click(t, eject)
	want := []string{udiskstest.CallUnmount + " " + string(stickFS), udiskstest.CallPowerOff + " " + string(stickDrive)}
	if calls := u.Calls.Load(); !slices.Equal(calls, want) {
		t.Errorf("eject called %v, want %v", calls, want)
	}
	if dir := hs.dir(); dir != hs.root {
		t.Errorf("after ejecting the chooser shows %s, want home", dir)
	}
	hs.waitButton(t, eject, false)
	hs.click(t, "Cancel")
	answer(t, got)
}
