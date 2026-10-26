// Package treetest walks lys-ui widget trees for the shell's surface
// tests: find the button a user would click, read what the labels say.
package treetest

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"golang.org/x/image/font/gofont/goregular"
)

// Loop stands in for the process's one UI goroutine in the surface
// harnesses: lys-ui keeps its style and layout scratch per process, so
// every harness in a test binary runs its widget work under this one
// lock, never a lock of its own (a finished test's harness can still be
// draining while the next one builds).
var Loop sync.Mutex

// Liveness is how long a surface harness waits for a surface to open or
// a request to be answered before failing: a guard against a hang, not
// a speed bound. The first surface of a test binary compiles the whole
// stylesheet bundle, which the race detector under a parallel test run
// stretches past a couple of seconds.
const Liveness = 10 * time.Second

// Walk visits w and every descendant, depth first: a hidden
// revealer's content too, which its Children leave out.
func Walk(w widget.Widget, visit func(widget.Widget)) {
	if w == nil {
		return
	}
	visit(w)
	if r, ok := w.(*widget.Revealer); ok {
		Walk(r.Child(), visit)
		return
	}
	if c, ok := w.(interface{ Children() []widget.Widget }); ok {
		for _, k := range c.Children() {
			Walk(k, visit)
		}
	}
}

// All is every widget of type T under root, in walk order.
func All[T widget.Widget](root widget.Widget) []T {
	var out []T
	Walk(root, func(w widget.Widget) {
		if t, ok := w.(T); ok {
			out = append(out, t)
		}
	})
	return out
}

// First is the first widget of type T under root.
func First[T widget.Widget](t *testing.T, root widget.Widget) T {
	t.Helper()
	all := All[T](root)
	if len(all) == 0 {
		var zero T
		t.Fatalf("no %T in the tree", zero)
		return zero
	}
	return all[0]
}

// Button is the first button whose accessible name is name.
func Button(t *testing.T, root widget.Widget, name string) *widget.Button {
	t.Helper()
	// The first in walk order; a toggle button clicks through its
	// embedded button.
	var found *widget.Button
	Walk(root, func(w widget.Widget) {
		if found != nil || widget.Describe(w).Name != name {
			return
		}
		switch b := w.(type) {
		case *widget.Button:
			found = b
		case *widget.ToggleButton:
			found = &b.Button
		}
	})
	if found == nil {
		t.Fatalf("no %q button", name)
	}
	return found
}

// WithClass is every widget under root carrying class.
func WithClass(root widget.Widget, class string) []widget.Widget {
	var out []widget.Widget
	Walk(root, func(w widget.Widget) {
		if widget.HasClass(w, class) {
			out = append(out, w)
		}
	})
	return out
}

// Labels is the text of every label under root, in walk order.
func Labels(root widget.Widget) []string {
	var out []string
	for _, l := range All[*widget.Label](root) {
		out = append(out, l.Text())
	}
	return out
}

// Font is the Go Regular face the widget tests measure and paint text
// with.
func Font(t *testing.T) render.Font {
	t.Helper()
	face, err := render.LoadFont(goregular.TTF)
	if err != nil {
		t.Fatalf("test font: %v", err)
	}
	return face
}

// IconTheme points theme icon lookups at a temp hicolor theme holding
// one solid square SVG app icon per name, for the duration of the test.
func IconTheme(t *testing.T, names ...string) {
	t.Helper()
	base := t.TempDir()
	apps := filepath.Join(base, "hicolor", "scalable", "apps")
	if err := os.MkdirAll(apps, 0o750); err != nil {
		t.Fatal(err)
	}
	index := "[Icon Theme]\nName=Hicolor\nDirectories=scalable/apps\n\n[scalable/apps]\nSize=48\nMinSize=8\nMaxSize=512\nType=Scalable\n"
	if err := os.WriteFile(filepath.Join(base, "hicolor", "index.theme"), []byte(index), 0o600); err != nil {
		t.Fatal(err)
	}
	const square = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16" fill="#e04020"/></svg>`
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(apps, n+".svg"), []byte(square), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	widget.SetIconSearchPaths([]string{base})
	widget.RefreshIcons()
	t.Cleanup(func() {
		widget.SetIconSearchPaths(nil)
		widget.RefreshIcons()
	})
}

// Ink counts the pixels w paints when laid out at its natural size.
func Ink(w widget.Widget) int {
	sz := w.Measure(widget.Constraints{Max: widget.Size{W: 256, H: 256}})
	w.Arrange(render.Rect{W: max(sz.W, 1), H: max(sz.H, 1)})
	data := make([]byte, render.Stride(256)*256)
	cv := render.NewScaled(data, render.Stride(256), 256, 256, 1, 1)
	w.Paint(cv)
	n := 0
	for i := 3; i < len(data); i += 4 {
		if data[i] > 0 {
			n++
		}
	}
	return n
}

// Pointer lays root out in a w x h rect and returns a Router over it
// with the pointer resting on target's centre, and that point: the real
// input path, hit testing and the pointer and scroll hooks included.
func Pointer(root widget.Widget, target widget.Boundser, w, h int) (*widget.Router, widget.Point) {
	root.Measure(widget.Constraints{Max: widget.Size{W: w, H: h}})
	root.Arrange(render.Rect{W: w, H: h})
	b := target.Bounds()
	p := widget.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}
	r := &widget.Router{Root: root}
	r.Move(p)
	return r, p
}
