package bar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	shelllocales "github.com/stubbedev/lys-desktop/i18n/locales/shell"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-desktop/swaytest"
	"github.com/stubbedev/lys-ui/atspitest"
	"github.com/stubbedev/lys-ui/widget"
)

// atspiLocaleEnv carries the locale a re-executed screen-reader test
// reads the bar in: the shell negotiates its locale once per process.
const atspiLocaleEnv = "LYS_SHELL_TEST_ATSPI_LOCALE"

const atspiRunBound = 3 * time.Minute

// A screen reader on real at-spi2-core reads every built-in module of a
// bar on a headless sway by its title in the shell's locale, then its
// state, and hears a module's state change; once in each shipped locale,
// each in a process of its own.
func TestHeadlessScreenReaderReadsEveryModule(t *testing.T) {
	if loc := os.Getenv(atspiLocaleEnv); loc != "" {
		readEveryModuleOverAtSpi(t, loc)
		return
	}
	for _, loc := range []string{"en-US", "fr"} {
		t.Run(loc, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), atspiRunBound)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHeadlessScreenReaderReadsEveryModule$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), atspiLocaleEnv+"="+loc, "LANGUAGE="+strings.ReplaceAll(loc, "-", "_"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the screen reader in %s: %v\n%s", loc, err, out)
			}
			if strings.Contains(string(out), "--- SKIP: TestHeadlessScreenReaderReadsEveryModule") {
				t.Skipf("the screen reader in %s skipped:\n%s", loc, out)
			}
		})
	}
}

func readEveryModuleOverAtSpi(t *testing.T, loc string) {
	swaytest.Start(t)
	core := atspitest.Start(t)
	core.Enable(t)
	if langs := i18n.Shell().Languages(); len(langs) == 0 || langs[0].String() != loc {
		t.Fatalf("the shell negotiated %v, want %s first", langs, loc)
	}
	titles, err := i18n.Select(i18n.NewAssets(shelllocales.FS), []i18n.LangID{i18n.MustLangID(loc)})
	if err != nil {
		t.Fatal(err)
	}

	built := modulesBuiltWithoutServices(t)
	layout := config.BarLayout{Monitor: "*"}
	for _, m := range built {
		layout.Left = append(layout.Left, config.BarItem{Module: config.BarModule(m.name)})
	}
	h := openHeadlessBar(t, config.Defaults(), layout)
	var items []widget.Widget
	h.wait("a box for every layout item", func() error {
		h.onLoop(func() {
			if h.bar != nil {
				items = h.bar.bar.Start().Children()
			}
		})
		if len(items) != len(built) {
			return fmt.Errorf("%d item boxes for %d items", len(items), len(built))
		}
		return nil
	})

	shown := map[string]bool{}
	var clock *barButton
	expected := func() (exact map[string]int, titled []string) {
		exact = map[string]int{}
		h.onLoop(func() {
			for i, m := range built {
				if len(items[i].(*widget.Box).Children()) == 0 {
					continue
				}
				shown[m.name] = true
				title := titles.Get("bar-module-" + m.name)
				buttons := treetest.WithClass(items[i], "bar-button")
				if len(buttons) == 0 {
					titled = append(titled, title)
					continue
				}
				for _, w := range buttons {
					b := w.(*barButton)
					if b.title != title {
						t.Errorf("%s's button is titled %q, want its %s title %q", m.name, b.title, loc, title)
					}
					if m.name == "clock" {
						clock = b
					}
					exact[moduleDescription(title, b.detail, b.text)]++
				}
			}
		})
		return exact, titled
	}

	app := core.Application(t, "lys-ui")
	heard := func(exact map[string]int, titled []string) error {
		names, err := app.Names()
		if err != nil {
			return err
		}
		got := map[string]int{}
		for _, n := range names {
			got[n]++
		}
		var missing []string
		for name, n := range exact {
			if got[name] < n {
				missing = append(missing, fmt.Sprintf("%q x%d (read %d)", name, n, got[name]))
			}
		}
		for _, title := range titled {
			if !slices.ContainsFunc(names, func(n string) bool { return n == title || strings.HasPrefix(n, title+", ") }) {
				missing = append(missing, fmt.Sprintf("a name titled %q", title))
			}
		}
		if len(missing) > 0 {
			return errors.New("the screen reader misses " + strings.Join(missing, ", "))
		}
		return nil
	}
	h.wait("every shown module read by its title and state", func() error { return heard(expected()) })
	for _, name := range modulesThatNeedNoService {
		if !shown[name] {
			t.Errorf("the bar does not show %s (shown %v)", name, shown)
		}
	}
	if clock == nil {
		t.Fatal("the bar shows no clock")
	}

	state := "state " + loc
	h.onLoop(func() { clock.SetDescription(state) })
	h.wait("the clock's new state read", func() error {
		return heard(map[string]int{titles.Get("bar-module-clock") + ", " + state: 1}, nil)
	})
}
