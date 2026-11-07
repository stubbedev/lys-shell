package dialogsurface_test

import (
	"bytes"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/shell/dialogsurface"
	"github.com/stubbedev/lys-shell/shell/dialogsurface/surfacetest"
)

const testAppID = "lys-shell-test-dialog"

func TestParentHandle(t *testing.T) {
	for _, tc := range []struct {
		parent, handle string
		ok             bool
	}{
		{"wayland:abc123", "abc123", true},
		{"", "", false},
		{"wayland:", "", false},
		{"x11:4c00007", "", false},
		{"abc123", "", false},
	} {
		if handle, ok := dialogsurface.ParentHandle(tc.parent); handle != tc.handle || ok != tc.ok {
			t.Errorf("ParentHandle(%q) = %q, %v; want %q, %v", tc.parent, handle, ok, tc.handle, tc.ok)
		}
	}
}

// open maps a modal test dialog for parent on the rig's shell.
func open(t *testing.T, r *surfacetest.Rig, parent string, failed func()) dialogsurface.Window {
	t.Helper()
	type opened struct {
		w   dialogsurface.Window
		err error
	}
	got := apptest.On(r.Shell, func() (result opened) {
		result.w, result.err = dialogsurface.Open(r.Shell, dialogsurface.Spec{
			Caller: portaldialogs.Caller{Handle: "/r/test", AppID: "org.example.Owner", ParentWindow: parent, Modal: true},
			Title:  "Test dialog",
			Overlay: app.LayerConfig{
				Layer: app.LayerOverlay, ExclusiveZone: -1, Keyboard: app.KeyboardExclusive,
				Namespace: testAppID, Root: widget.NewDrawingArea(160, 120),
			},
			Failed: failed,
		})
		return result
	})
	if got.err != nil {
		t.Fatalf("open the dialog: %v", got.err)
	}
	t.Cleanup(func() { r.Shell.Invoke(got.w.Close) })
	return got.w
}

// warnings collects what the package logs for the rest of the test.
func warnings(t *testing.T) func() []string {
	t.Helper()
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		return buf.Write(p)
	}))
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for line := range strings.Lines(buf.String()) {
			if strings.Contains(line, "portal dialog:") {
				out = append(out, line)
			}
		}
		return out
	}
}

// A dialog for a Wayland window maps as that window's transient, on its
// output, and leaves the keyboard to whatever window the user picks;
// once the window closes the dialog moves to the overlay.
func TestADialogForAWaylandWindowIsItsTransient(t *testing.T) {
	r := surfacetest.Start(t)
	win := open(t, r, r.Parent, nil)
	r.AwaitParented(t, testAppID)
	r.AwaitOtherFocused(t)
	parented := apptest.On(r.Shell, win.HostSurface)
	r.CloseOwner(t)
	r.AwaitNoToplevel(t, testAppID)
	apptest.Until(t, r.Shell, "the dialog on the overlay", func() bool {
		s := win.HostSurface()
		return s != nil && s != parented
	})
}

// A request without a Wayland parent, none or an X11 one, keeps the
// overlay, which holds the keyboard while it is modal.
func TestADialogWithoutAWaylandParentIsTheOverlay(t *testing.T) {
	for _, parent := range []string{"", "x11:4c00007"} {
		t.Run(parent, func(t *testing.T) {
			r := surfacetest.Start(t)
			logged := warnings(t)
			open(t, r, parent, nil)
			r.AssertNoToplevel(t, testAppID)
			other := r.MapOther(t)
			deadline := time.Now().Add(300 * time.Millisecond)
			for time.Now().Before(deadline) {
				if other() {
					t.Fatal("another window took the keyboard from the modal overlay")
				}
				time.Sleep(20 * time.Millisecond)
			}
			if got := logged(); len(got) != 0 {
				t.Errorf("the overlay path warned: %q", got)
			}
		})
	}
}

// A handle that names no window falls back to the overlay, with one
// warning.
func TestAnUnknownHandleFallsBackToTheOverlay(t *testing.T) {
	r := surfacetest.Start(t)
	logged := warnings(t)
	failed := false
	open(t, r, "wayland:no-such-handle", func() { failed = true })
	r.AwaitNoToplevel(t, testAppID)
	apptest.Until(t, r.Shell, "the fallback warned", func() bool { return len(logged()) > 0 })
	apptest.Dispatched(t, r.Shell)
	if got := logged(); len(got) != 1 {
		t.Errorf("warnings %q, want one", got)
	}
	if apptest.On(r.Shell, func() bool { return failed }) {
		t.Error("the overlay fallback reported the dialog failed")
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
