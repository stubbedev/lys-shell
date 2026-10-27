package screen

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// shape is a widget tree as its node types and classes, depth first,
// leaving out the subtrees in skip.
func shape(root widget.Widget, skip ...widget.Widget) []string {
	var out []string
	var walk func(w widget.Widget, depth int)
	walk = func(w widget.Widget, depth int) {
		if slices.Contains(skip, w) {
			return
		}
		line := fmt.Sprintf("%s%T", strings.Repeat(" ", depth), w)
		if c, ok := w.(interface{ Classes() []string }); ok {
			line += " " + strings.Join(c.Classes(), ".")
		}
		out = append(out, line)
		if p, ok := w.(interface{ Children() []widget.Widget }); ok {
			for _, child := range p.Children() {
				walk(child, depth+1)
			}
		}
	}
	walk(root, 0)
	return out
}

func greeterRows() GreeterRows {
	row := func(class string) *widget.Box {
		b := widget.NewBox(widget.Row, 0, 0)
		b.AddClass(class)
		return b
	}
	return GreeterRows{Users: row("lock-users"), Session: row("lock-session"), Power: row("lock-power")}
}

// Both modes build the same screen; the greeter adds its username entry
// and its rows, nothing else.
func TestModesShareTheScreen(t *testing.T) {
	cfg := config.DefaultsLogin()
	rows := greeterRows()
	spec := Spec{Config: cfg, Font: treetest.Font(t), CapsLock: "Caps Lock is on", Greeter: rows, OnSubmit: func(string) {}}
	spec.Mode = Lock
	lock := Build(spec)
	spec.Mode = Greeter
	greeter := Build(spec)

	if greeter.Prompt.Username == nil || lock.Prompt.Username != nil {
		t.Fatal("the username entry is the greeter's")
	}
	for _, row := range []widget.Widget{rows.Users, rows.Session, rows.Power} {
		if !treetest.Contains(greeter.Root, row) || treetest.Contains(lock.Root, row) {
			t.Errorf("%v is a greeter row: in the greeter, not in the lock", row.(interface{ Classes() []string }).Classes())
		}
	}
	shared := shape(greeter.Root, greeter.Prompt.Username, rows.Users, rows.Session, rows.Power)
	if lockShape := shape(lock.Root); !reflect.DeepEqual(shared, lockShape) {
		t.Errorf("the shared parts differ:\ngreeter without its rows:\n%s\nlock:\n%s", strings.Join(shared, "\n"), strings.Join(lockShape, "\n"))
	}
	for _, s := range []*Surface{lock, greeter} {
		if s.Prompt.Caps.Text() != "Caps Lock is on" || s.Prompt.Caps.Visible() || s.Scrim.Visible() {
			t.Error("each mode starts with the caps warning and the scrim hidden")
		}
	}
}

// The config drives both modes alike; the greeter's rows are left out
// when it has none.
func TestBuildFollowsTheConfig(t *testing.T) {
	cfg := config.DefaultsLogin()
	cfg.ShowClock = false
	for _, mode := range []Mode{Lock, Greeter} {
		s := Build(Spec{Mode: mode, Config: cfg, Font: treetest.Font(t), OnSubmit: func(string) {}})
		if s.Prompt.Clock.Visible() || s.Prompt.Date.Visible() {
			t.Errorf("mode %d: show-clock off still shows the clock", mode)
		}
		if len(treetest.WithClass(s.Root, "lock-power")) != 0 {
			t.Errorf("mode %d: power buttons without any given", mode)
		}
	}
}

func TestCapsLockWarning(t *testing.T) {
	s := Build(Spec{Mode: Lock, Config: config.DefaultsLogin(), Font: treetest.Font(t), CapsLock: "caps", OnSubmit: func(string) {}})
	s.Prompt.SetCapsLock(true)
	if !s.Prompt.Caps.Visible() {
		t.Error("SetCapsLock(true) left the warning hidden")
	}
	s.Prompt.SetCapsLock(false)
	if s.Prompt.Caps.Visible() {
		t.Error("SetCapsLock(false) left the warning shown")
	}
}
