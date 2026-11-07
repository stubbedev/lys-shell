package portal

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/filechooser"
)

// openFileAsync calls OpenFile, its reply on the channel.
func openFileAsync(t *testing.T, r *rig) <-chan uint32 {
	t.Helper()
	done := make(chan uint32, 1)
	go func() {
		var code uint32
		var res map[string]dbus.Variant
		if err := r.obj.Call(fileChooserIfaceName+"OpenFile", 0, handle, "org.app", "", "Open", Vardict{}).Store(&code, &res); err != nil {
			t.Error(err)
		}
		done <- code
	}()
	return done
}

// waitingFor reports whether the backend watches service for an owner.
func (r *rig) waitingFor(t *testing.T, service string) bool {
	t.Helper()
	return slices.ContainsFunc(dbustest.MatchRules(t, r.backend.conn), func(rule string) bool {
		return strings.Contains(rule, "NameOwnerChanged") && strings.Contains(rule, "'"+service+"'")
	})
}

// awaitWaiting waits until the backend waits for service to appear.
func (r *rig) awaitWaiting(t *testing.T, service string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !r.waitingFor(t, service) {
		if time.Now().After(deadline) {
			t.Fatalf("the backend never waited for %s", service)
		}
		time.Sleep(time.Millisecond)
	}
}

// A dialog asked for while no shell owns its service waits for the
// shell, and the shell's answer goes through once it appears.
func TestADialogWaitsForTheShellToAppear(t *testing.T) {
	withShellAppearBound(t, time.Minute)
	r := newRig(t, nil)
	done := openFileAsync(t, r)
	r.awaitWaiting(t, filechooser.ServiceName)
	serveChooser(t, r, "file:///late.png")
	if code := awaitReply(t, done); code != ResponseSuccess {
		t.Errorf("OpenFile = %d once the shell appeared, want success", code)
	}
	if r.waitingFor(t, filechooser.ServiceName) {
		t.Error("the backend still watches the service after it appeared")
	}
}

// A shell that never appears fails the dialog with other once the wait
// is over, not before.
func TestADialogWithoutAShellFailsAfterTheBound(t *testing.T) {
	const bound = time.Second
	withShellAppearBound(t, bound)
	r := newRig(t, nil)
	start := time.Now()
	done := openFileAsync(t, r)
	select {
	case code := <-done:
		if code != ResponseOther {
			t.Errorf("OpenFile = %d without a shell, want other", code)
		}
	case <-time.After(bound + 5*time.Second):
		t.Fatal("the wait for the shell is unbounded")
	}
	if waited := time.Since(start); waited < bound {
		t.Errorf("OpenFile replied after %v, before the %v wait for the shell was over", waited, bound)
	}
	if r.waitingFor(t, filechooser.ServiceName) {
		t.Error("the backend still watches the service after giving up")
	}
}

// The app closing its request while the backend waits for the shell
// cancels at once, not at the bound.
func TestClosingARequestEndsTheWaitForTheShell(t *testing.T) {
	withShellAppearBound(t, time.Minute)
	r := newRig(t, nil)
	done := openFileAsync(t, r)
	r.awaitWaiting(t, filechooser.ServiceName)
	if err := r.client.Object(BusName, handle).Call(RequestIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if code := awaitReply(t, done); code != ResponseCancelled {
		t.Errorf("OpenFile = %d after Close, want cancelled", code)
	}
	if r.waitingFor(t, filechooser.ServiceName) {
		t.Error("the backend still watches the service after the Close")
	}
}

// A shell that dies with its dialog open fails the dialog with other
// at once, not after a wait.
func TestAShellDyingMidDialogFailsAtOnce(t *testing.T) {
	withShellAppearBound(t, time.Minute)
	r := newRig(t, nil)
	f := &fakeChooser{}
	f.hold.Store(true)
	shell := r.bus.Conn(t)
	if _, err := filechooser.NewDaemon(f).Export(shell); err != nil {
		t.Fatal(err)
	}
	done := openFileAsync(t, r)
	f.waitOpen(t, handle)
	if err := shell.Close(); err != nil {
		t.Fatal(err)
	}
	if code := awaitReply(t, done); code != ResponseOther {
		t.Errorf("OpenFile = %d after the shell died, want other", code)
	}
}
