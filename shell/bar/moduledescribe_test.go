package bar

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	shelllocales "github.com/stubbedev/lys-desktop/i18n/locales/shell"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-ui/widget"
)

// Every built-in module has a title in every locale the shell ships.
func TestEveryModuleHasATitle(t *testing.T) {
	assets := i18n.NewAssets(shelllocales.FS)
	for _, loc := range []string{"en-US", "fr"} {
		src, err := assets.Source(loc)
		if err != nil {
			t.Fatal(err)
		}
		for name := range factories {
			if !strings.Contains(src, "\nbar-module-"+name+" = ") {
				t.Errorf("%s: module %q has no bar-module-%s title", loc, name, name)
			}
		}
	}
}

// Every built-in module that builds without its services is named by
// its title, then its state, and shows the same as its tooltip.
func TestEveryBuiltModuleIsNamedByItsTitleAndState(t *testing.T) {
	var built []string
	for _, m := range modulesBuiltWithoutServices(t) {
		name := m.name
		built = append(built, name)
		title := i18n.T("bar-module-" + name)
		for _, w := range treetest.WithClass(m.row, "bar-button") {
			b, ok := w.(*barButton)
			if !ok {
				continue
			}
			got := onHeadlessLoop(func() string { return widget.Describe(b).Name })
			if got != title && !strings.HasPrefix(got, title+", ") {
				t.Errorf("%s is named %q, want its title %q first", name, got, title)
			}
			if tip := b.TooltipText(); tip != got {
				t.Errorf("%s shows the tooltip %q, want its name %q", name, tip, got)
			}
		}
	}
	for _, core := range modulesThatNeedNoService {
		if !slices.Contains(built, core) {
			t.Errorf("%s did not build without its services (built %v)", core, built)
		}
	}
}

// mountWithoutServices mounts name in a bar row as the bar does, with
// no service behind it; ok is false for a module that needs one, which
// refuses (an error) or reaches for it (a nil dereference).
func mountWithoutServices(t *testing.T, name string) (row *widget.Box, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	row = widget.NewBox(widget.Row, 0, 0)
	return row, appendModule(row, config.BarItem{Module: config.BarModule(name)}, newTestContext(t, config.Defaults())) == nil
}

// A module is named by its title and its state, the name changes with
// the state, the module's own description wins over its label, and the
// tooltip shows the same unless the module's tooltip key is off.
func TestABarButtonIsNamedByItsTitleAndState(t *testing.T) {
	cfg := config.Defaults()
	b := newBarButton(newTestContext(t, cfg), widget.NewThemeIcon("battery-symbolic", 16), "")
	button := batteryButton(cfg)
	button.LabelShow = false
	b.configure("Battery", button, config.ClickConfig{}, nil)
	name := func() string { return onHeadlessLoop(func() string { return widget.Describe(b).Name }) }
	if got := name(); got != "Battery" {
		t.Errorf("before any state the name is %q, want the title", got)
	}
	b.SetLabel("54%")
	if got := name(); got != "Battery, 54%" || b.TooltipText() != got || b.Label().Text() != "" {
		t.Errorf("name %q, tooltip %q, label %q: want the state named and tipped with the label hidden", got, b.TooltipText(), b.Label().Text())
	}
	b.SetLabel("55%")
	if got := name(); got != "Battery, 55%" {
		t.Errorf("a state change leaves the name %q", got)
	}
	b.SetDescription("55%, charging")
	if got := name(); got != "Battery, 55%, charging" {
		t.Errorf("the module's description gives the name %q", got)
	}
	button.Tooltip = false
	b.configure("Battery", button, config.ClickConfig{}, nil)
	if got := name(); got != "Battery, 55%, charging" || b.TooltipText() != "" {
		t.Errorf("tooltip off: name %q tooltip %q, want the name kept and no tooltip", got, b.TooltipText())
	}
}

// Setting the state a module already shows marks nothing for assistive
// technology.
func TestAnUnchangedStateMarksNothing(t *testing.T) {
	cfg := config.Defaults()
	b := newBarButton(newTestContext(t, cfg), nil, "")
	b.configure("Volume", moduleButton("volume", cfg), config.ClickConfig{}, nil)
	b.SetLabel("40%")
	b.SetDescription("40%, muted")
	before := widget.A11yGeneration()
	for range 100 {
		b.SetLabel("40%")
		b.SetDescription("40%, muted")
	}
	if moved := widget.A11yGeneration() - before; moved != 0 {
		t.Errorf("200 unchanged updates moved the accessibility generation %d times", moved)
	}
}

// Enter and Space on a focused module run its primary binding, as a
// press does.
func TestEnterAndSpaceRunAModulesPrimaryBinding(t *testing.T) {
	cfg := config.Defaults()
	b := newBarButton(newTestContext(t, cfg), nil, "")
	var ran []config.ClickAction
	open := config.ParseClickAction("dropdown:calendar")
	b.configure("Clock", moduleButton("clock", cfg), config.ClickConfig{LeftClick: open, RightClick: config.ParseClickAction("true")}, func(a config.ClickAction, _ bool) { ran = append(ran, a) })
	b.KeyAction(widget.KeyEnter, 0)
	b.InsertRune(' ')
	b.KeyAction(widget.KeyLeft, 0)
	b.InsertRune('x')
	if len(ran) != 2 || ran[0] != open || ran[1] != open {
		t.Errorf("ran %v, want the primary binding for Enter and for Space alone", ran)
	}
}

// builtModule is a built-in module mounted alone in a bar row.
type builtModule struct {
	name string
	row  *widget.Box
}

// modulesBuiltWithoutServices mounts, in name order, every built-in
// module that builds with no service behind it.
func modulesBuiltWithoutServices(t *testing.T) []builtModule {
	var built []builtModule
	for _, name := range slices.Sorted(maps.Keys(factories)) {
		if row, ok := mountWithoutServices(t, name); ok {
			built = append(built, builtModule{name: name, row: row})
		}
	}
	return built
}

// modulesThatNeedNoService are modules every bar can show, with no
// service running.
var modulesThatNeedNoService = []string{"clock", "dashboard", "idle-inhibit", "power", "privacy", "screenshot"}
