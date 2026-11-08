// Package surfacetest runs a portal dialog host against a real lys-wm,
// with an app window it can be parented to, and reads back where lys-wm
// put the dialog. The app windows run in processes of their own
// (./probe), as real apps do, since a process runs one lys-ui loop.
package surfacetest

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/service/lyswm"
	"github.com/stubbedev/lys-desktop/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-lib/wmipc"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/toplevels"
)

// The app ids of the windows the rig maps.
const (
	OwnerAppID = "org.example.Owner"
	otherAppID = "org.example.Other"
)

// Rig is a headless lys-wm with an app window exported for the dialogs
// to name, and the shell's application to show them from.
type Rig struct {
	// Shell runs the dialog host.
	Shell *app.Application
	// Parent is the owner window's parent_window, "wayland:<handle>".
	Parent string
	probe  string
	owner  *window
	list   *toplevels.Watcher
}

// Start runs lys-wm headless, maps the owner window in a process of its
// own, and runs the shell's application, for the rest of the test.
func Start(t *testing.T) *Rig {
	t.Helper()
	lyswmtest.Headless(t)
	r := &Rig{probe: buildProbe(t)}
	r.owner = r.mapWindow(t, OwnerAppID)
	until(t, "the owner window exported", func() error {
		if r.owner.state().handle == "" {
			return fmt.Errorf("%s reported no handle", OwnerAppID)
		}
		return nil
	})
	r.Parent = "wayland:" + r.owner.state().handle
	list, err := toplevels.Watch(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(list.Close)
	r.list = list
	r.Shell = apptest.Loop(t)
	return r
}

// buildProbe builds ./probe for the test.
func buildProbe(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "probe")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest/probe") //nolint:gosec // go from PATH, into the test's temp dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the probe: %v\n%s", err, out)
	}
	return bin
}

// window is a probe process and what it last reported.
type window struct {
	cmd    *exec.Cmd
	exited chan struct{}
	mu     sync.Mutex
	last   probeState
}

type probeState struct {
	handle   string
	keyboard bool
}

func (w *window) state() probeState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

// kill ends the probe and waits for it.
func (w *window) kill() {
	_ = w.cmd.Process.Kill()
	<-w.exited
}

// mapWindow maps a window of appID in a probe process until the test
// ends.
func (r *Rig) mapWindow(t *testing.T, appID string) *window {
	t.Helper()
	cmd := exec.Command(r.probe, "-app-id", appID) //nolint:gosec // the probe this test built
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := &window{cmd: cmd, exited: make(chan struct{})}
	go func() {
		lines := bufio.NewScanner(out)
		for lines.Scan() {
			key, value, _ := strings.Cut(lines.Text(), " ")
			w.mu.Lock()
			switch key {
			case "handle":
				w.last.handle = value
			case "keyboard":
				w.last.keyboard = value == "true"
			}
			w.mu.Unlock()
		}
		_ = cmd.Wait()
		close(w.exited)
	}()
	t.Cleanup(w.kill)
	return w
}

// lysWMWindow is lys-wm's window of appID.
func lysWMWindow(appID string) (wmipc.Window, error) {
	windows, err := lyswm.Windows()
	if err != nil {
		return wmipc.Window{}, err
	}
	i := slices.IndexFunc(windows, func(w wmipc.Window) bool { return w.AppID == appID })
	if i < 0 {
		return wmipc.Window{}, fmt.Errorf("lys-wm shows no %s window", appID)
	}
	return windows[i], nil
}

// until polls check until it passes, failing the test with its last
// error after ten seconds.
func until(t *testing.T, what string, check func() error) {
	t.Helper()
	var err error
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err = check(); err == nil {
			return
		}
	}
	t.Fatalf("never: %s: %v", what, err)
}

// AwaitParented waits until lys-wm shows a toplevel of appID transient
// for the owner window and on its output.
func (r *Rig) AwaitParented(t *testing.T, appID string) {
	t.Helper()
	until(t, appID+" parented to the owner window on its output", func() error {
		listed := r.list.Windows()
		i := slices.IndexFunc(listed, func(w toplevels.Window) bool { return w.AppID == appID })
		if i < 0 {
			return fmt.Errorf("no %s toplevel in %+v", appID, listed)
		}
		if parent := listed[i].Parent; parent != OwnerAppID {
			return fmt.Errorf("%s is transient for %q", appID, parent)
		}
		dialog, err := lysWMWindow(appID)
		if err != nil {
			return err
		}
		owner, err := lysWMWindow(OwnerAppID)
		if err != nil {
			return err
		}
		if dialog.Output != owner.Output {
			return fmt.Errorf("%s is on %s, its parent on %s", appID, dialog.Output, owner.Output)
		}
		return nil
	})
}

// AssertNoToplevel fails the test if a toplevel of appID shows: the
// dialog is an overlay.
func (r *Rig) AssertNoToplevel(t *testing.T, appID string) {
	t.Helper()
	apptest.Dispatched(t, r.Shell)
	if _, err := lysWMWindow(appID); err == nil {
		t.Errorf("lys-wm shows a %s toplevel, want the overlay", appID)
	}
}

// AwaitNoToplevel waits until no toplevel of appID shows: the dialog
// moved to the overlay.
func (r *Rig) AwaitNoToplevel(t *testing.T, appID string) {
	t.Helper()
	until(t, "no "+appID+" toplevel", func() error {
		if _, err := lysWMWindow(appID); err == nil {
			return fmt.Errorf("lys-wm still shows a %s toplevel", appID)
		}
		return nil
	})
}

// CloseOwner ends the owner window's app.
func (r *Rig) CloseOwner(t *testing.T) {
	t.Helper()
	r.owner.kill()
	r.AwaitNoToplevel(t, OwnerAppID)
}

// MapOther maps one more app window in a process of its own, asks
// lys-wm to focus it as the user would, and returns whether it holds
// the keyboard focus.
func (r *Rig) MapOther(t *testing.T) (focused func() bool) {
	t.Helper()
	other := r.mapWindow(t, otherAppID)
	var id uint64
	until(t, "the other window on lys-wm", func() error {
		w, err := lysWMWindow(otherAppID)
		id = w.ID
		return err
	})
	if err := lyswm.Dispatch("focuswindow " + strconv.FormatUint(id, 10)); err != nil {
		t.Fatal(err)
	}
	return func() bool { return other.state().keyboard }
}

// AwaitOtherFocused maps one more app window, focuses it, and waits
// until it holds the keyboard: the dialog showing does not keep it.
func (r *Rig) AwaitOtherFocused(t *testing.T) {
	t.Helper()
	focused := r.MapOther(t)
	until(t, "another window holds the keyboard while the dialog shows", func() error {
		if !focused() {
			return fmt.Errorf("%s has no keyboard focus", otherAppID)
		}
		return nil
	})
}
