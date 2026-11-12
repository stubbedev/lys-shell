package dialogsurface_test

import (
	"bytes"
	"log"
	"os"
	"slices"
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

// built records the surfaces a dialog's tree was built for, in order.
type built struct {
	mu       sync.Mutex
	surfaces []dialogsurface.Surface
}

func (b *built) root(s dialogsurface.Surface) widget.Widget {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.surfaces = append(b.surfaces, s)
	return widget.NewDrawingArea(160, 120)
}

func (b *built) got() []dialogsurface.Surface {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.surfaces)
}

// spec is a modal test dialog for parent, its tree recorded by b.
func spec(parent string, b *built, failed func()) dialogsurface.Spec {
	return dialogsurface.Spec{
		Caller: portaldialogs.Caller{Handle: "/r/test", AppID: "org.example.Owner", ParentWindow: parent, Modal: true},
		Title:  "Test dialog",
		Root:   b.root,
		Layer: app.LayerConfig{
			Layer: app.LayerOverlay, ExclusiveZone: -1, Keyboard: app.KeyboardExclusive,
			Namespace: testAppID,
		},
		Failed: failed,
	}
}

// openSpec maps s on the rig's shell until the test ends.
func openSpec(t *testing.T, r *surfacetest.Rig, s dialogsurface.Spec) dialogsurface.Window {
	t.Helper()
	type opened struct {
		w   dialogsurface.Window
		err error
	}
	got := apptest.On(r.Shell, func() (result opened) {
		result.w, result.err = dialogsurface.Open(r.Shell, s)
		return result
	})
	if got.err != nil {
		t.Fatalf("open the dialog: %v", got.err)
	}
	t.Cleanup(func() { r.Shell.Invoke(got.w.Close) })
	return got.w
}

// open maps a modal test dialog for parent on the rig's shell.
func open(t *testing.T, r *surfacetest.Rig, parent string, b *built, failed func()) dialogsurface.Window {
	t.Helper()
	return openSpec(t, r, spec(parent, b, failed))
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
	b := &built{}
	win := open(t, r, r.Parent, b, nil)
	r.AwaitParented(t, testAppID)
	if got := b.got(); !slices.Equal(got, []dialogsurface.Surface{dialogsurface.OnWindow}) {
		t.Errorf("the transient's tree was built for %v, want the window alone", got)
	}
	r.AwaitOtherFocused(t)
	parented := apptest.On(r.Shell, win.HostSurface)
	r.CloseOwner(t)
	r.AwaitNoToplevel(t, testAppID)
	apptest.Until(t, r.Shell, "the dialog on the overlay", func() bool {
		s := win.HostSurface()
		return s != nil && s != parented
	})
	if got, want := b.got(), []dialogsurface.Surface{dialogsurface.OnWindow, dialogsurface.OnOverlay}; !slices.Equal(got, want) {
		t.Errorf("the tree was built for %v, want %v: rebuilt for the overlay it moved to", got, want)
	}
}

// A request without a Wayland parent, none or an X11 one, keeps the
// overlay, which holds the keyboard while it is modal.
func TestADialogWithoutAWaylandParentIsTheOverlay(t *testing.T) {
	for _, parent := range []string{"", "x11:4c00007"} {
		t.Run(parent, func(t *testing.T) {
			r := surfacetest.Start(t)
			logged := warnings(t)
			b := &built{}
			open(t, r, parent, b, nil)
			r.AssertNoToplevel(t, testAppID)
			if got := b.got(); !slices.Equal(got, []dialogsurface.Surface{dialogsurface.OnOverlay}) {
				t.Errorf("the overlay's tree was built for %v, want the overlay alone", got)
			}
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
	open(t, r, "wayland:no-such-handle", &built{}, func() { failed = true })
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

// sizes records the sizes a window dialog takes.
type sizes struct {
	mu   sync.Mutex
	seen []widget.Size
}

func (s *sizes) resized(sz widget.Size) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, sz)
}

func (s *sizes) got() []widget.Size {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.seen)
}

func (s *sizes) last() widget.Size {
	if got := s.got(); len(got) > 0 {
		return got[len(got)-1]
	}
	return widget.Size{}
}

var (
	windowInitial = widget.Size{W: 300, H: 200}
	windowMin     = widget.Size{W: 220, H: 150}
	windowMax     = widget.Size{W: 420, H: 320}
)

// openSized maps a window dialog of the test's bounds for the rig's
// owner window, recording the sizes it takes.
func openSized(t *testing.T, r *surfacetest.Rig) *sizes {
	t.Helper()
	seen := &sizes{}
	s := spec(r.Parent, &built{}, nil)
	s.Window = dialogsurface.WindowSize{Initial: windowInitial, Min: windowMin, Max: windowMax, OnResize: seen.resized}
	openSpec(t, r, s)
	r.AwaitParented(t, testAppID)
	apptest.Until(t, r.Shell, "the window sized", func() bool { return len(seen.got()) > 0 })
	return seen
}

// A window dialog opens at its initial size.
func TestAWindowDialogOpensAtItsInitialSize(t *testing.T) {
	r := surfacetest.Start(t)
	seen := openSized(t, r)
	apptest.Dispatched(t, r.Shell)
	if got := seen.got(); got[0] != windowInitial {
		t.Errorf("the window took %v, want it opened at %v", got, windowInitial)
	}
}

// The compositor resizes a window dialog within its bounds and no
// further, and the dialog hears each size it takes.
func TestAWindowDialogResizesWithinItsBounds(t *testing.T) {
	r := surfacetest.Start(t)
	seen := openSized(t, r)
	r.ResizeBy(t, testAppID, -1000, -1000)
	apptest.Until(t, r.Shell, "the window shrunk to its floor", func() bool { return seen.last() == windowMin })
	r.ResizeBy(t, testAppID, 1000, 1000)
	apptest.Until(t, r.Shell, "the window grown to its ceiling", func() bool { return seen.last() == windowMax })
	for _, sz := range seen.got() {
		if sz.W < windowMin.W || sz.H < windowMin.H || sz.W > windowMax.W || sz.H > windowMax.H {
			t.Errorf("the window took %v, outside %v to %v", sz, windowMin, windowMax)
		}
	}
}

var (
	rowSize      = widget.Size{W: 160, H: 60}
	contentStart = widget.Size{W: rowSize.W, H: 2 * rowSize.H}
)

// growing is a window dialog whose tree starts two rows tall and takes
// more rows as the test adds them.
type growing struct {
	r    *surfacetest.Rig
	seen *sizes
	rows *widget.Box
}

// openGrowing maps a growing window dialog for the rig's owner window,
// opened at its content's size and at most limit.
func openGrowing(t *testing.T, r *surfacetest.Rig, limit widget.Size) *growing {
	t.Helper()
	g := &growing{r: r, seen: &sizes{}}
	s := spec(r.Parent, &built{}, nil)
	s.Root = func(dialogsurface.Surface) widget.Widget {
		g.rows = widget.NewBox(widget.Column, 0, 0)
		return g.rows
	}
	s.Window = dialogsurface.WindowSize{Initial: contentStart, Max: limit, OnResize: g.seen.resized}
	openSpec(t, r, s)
	g.add(t, 2)
	r.AwaitParented(t, testAppID)
	g.await(t, "the window opened", contentStart)
	return g
}

// add appends n rows to the dialog's tree.
func (g *growing) add(t *testing.T, n int) {
	t.Helper()
	apptest.On(g.r.Shell, func() struct{} {
		for range n {
			g.rows.Append(widget.NewDrawingArea(rowSize.W, rowSize.H), false)
		}
		return struct{}{}
	})
}

// await waits until the window last took want.
func (g *growing) await(t *testing.T, what string, want widget.Size) {
	t.Helper()
	apptest.Until(t, g.r.Shell, what, func() bool { return g.seen.last() == want })
}

// holds fails the test if the window leaves want within a moment.
func (g *growing) holds(t *testing.T, what string, want widget.Size) {
	t.Helper()
	apptest.Dispatched(t, g.r.Shell)
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got := g.seen.last(); got != want {
			t.Fatalf("%s: the window took %v, want it kept at %v", what, got, want)
		}
	}
}

// A window dialog whose content grows past it grows to fit, up to its
// maximum, and no further.
func TestAWindowDialogGrowsWithItsContent(t *testing.T) {
	r := surfacetest.Start(t)
	limit := widget.Size{W: 400, H: 7 * rowSize.H}
	g := openGrowing(t, r, limit)
	g.add(t, 3)
	g.await(t, "the window grown to five rows", widget.Size{W: rowSize.W, H: 5 * rowSize.H})
	g.add(t, 3)
	g.await(t, "the window grown to its ceiling", widget.Size{W: rowSize.W, H: limit.H})
	g.holds(t, "eight rows past a seven-row ceiling", widget.Size{W: rowSize.W, H: limit.H})
	for _, sz := range g.seen.got() {
		if sz.H > limit.H {
			t.Errorf("the window took %v, above its ceiling %v", sz, limit)
		}
	}
}

// A window the user enlarged keeps its size while its content still
// fits, grows only along the axis the content outgrows, and cannot be
// shrunk below its content.
func TestAUserEnlargedWindowDialogStaysEnlarged(t *testing.T) {
	r := surfacetest.Start(t)
	g := openGrowing(t, r, widget.Size{})
	enlarged := widget.Size{W: contentStart.W + 100, H: contentStart.H + 2*rowSize.H}
	r.ResizeBy(t, testAppID, 100, 2*rowSize.H)
	g.await(t, "the window enlarged by the user", enlarged)
	g.add(t, 1)
	g.holds(t, "a row that still fits", enlarged)
	g.add(t, 2)
	g.await(t, "the window grown to five rows, its width kept", widget.Size{W: enlarged.W, H: 5 * rowSize.H})
	r.ResizeBy(t, testAppID, -1000, -1000)
	g.await(t, "the window shrunk to its content", widget.Size{W: rowSize.W, H: 5 * rowSize.H})
}

// A spec Open cannot trust maps nothing and says why.
func TestAnInvalidSpecIsRefused(t *testing.T) {
	r := surfacetest.Start(t)
	for name, change := range map[string]func(*dialogsurface.Spec){
		"no root":            func(s *dialogsurface.Spec) { s.Root = nil },
		"a layer root":       func(s *dialogsurface.Spec) { s.Layer.Root = widget.NewDrawingArea(1, 1) },
		"a negative minimum": func(s *dialogsurface.Spec) { s.Window.Min.W = -1 },
		"a floor above the ceiling": func(s *dialogsurface.Spec) {
			s.Window.Min, s.Window.Max = widget.Size{W: 400, H: 300}, widget.Size{W: 300, H: 300}
		},
		"an initial size below the floor": func(s *dialogsurface.Spec) {
			s.Window.Initial, s.Window.Min = widget.Size{W: 100, H: 100}, widget.Size{W: 200, H: 100}
		},
		"an initial size above the ceiling": func(s *dialogsurface.Spec) {
			s.Window.Initial, s.Window.Max = widget.Size{W: 100, H: 400}, widget.Size{W: 100, H: 300}
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := &built{}
			s := spec(r.Parent, b, nil)
			change(&s)
			err := apptest.On(r.Shell, func() error {
				w, err := dialogsurface.Open(r.Shell, s)
				if w != nil {
					w.Close()
				}
				return err
			})
			if err == nil || !strings.Contains(err.Error(), "Test dialog") {
				t.Errorf("Open = %v, want an error naming the dialog", err)
			}
			if got := b.got(); len(got) != 0 {
				t.Errorf("built a tree for %v, want none", got)
			}
		})
	}
	r.AssertNoToplevel(t, testAppID)
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
