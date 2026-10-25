package apptheme

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/icons"
)

// fakeIconSearch records what initIcons does to the icon search path.
type fakeIconSearch struct {
	mu        sync.Mutex
	paths     []string
	refreshes int
}

func (f *fakeIconSearch) search() iconSearch {
	return iconSearch{
		paths: func() []string { f.mu.Lock(); defer f.mu.Unlock(); return slices.Clone(f.paths) },
		set:   func(p []string) { f.mu.Lock(); defer f.mu.Unlock(); f.paths = p },
		refresh: func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.refreshes++
		},
	}
}

func (f *fakeIconSearch) state() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.paths), f.refreshes
}

func TestInitIcons(t *testing.T) {
	t.Setenv("XDG_DATA_DIRS", "")
	base := t.TempDir()
	r := icons.RegistryAt(base)
	fake := &fakeIconSearch{paths: []string{"/usr/share/icons"}}

	stop := initIcons(r, fake.search())
	defer stop()

	if !r.IsValid() {
		t.Error("the icons directory and index.theme were not set up")
	}
	paths, refreshes := fake.state()
	if !slices.Equal(paths, []string{base, "/usr/share/icons"}) || refreshes != 1 {
		t.Fatalf("after init: paths %v, %d refreshes; want the registry first, one refresh", paths, refreshes)
	}

	// An icon installed while the shell runs is picked up.
	if err := os.WriteFile(filepath.Join(r.IconsDir(), "ld-new-symbolic.svg"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, n := fake.state(); n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("installing an icon did not refresh the icon theme")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if paths, _ := fake.state(); !slices.Equal(paths, []string{base, "/usr/share/icons"}) {
		t.Errorf("a refresh changed the paths to %v", paths)
	}
}

func TestInitIconsMigratesStaleIcons(t *testing.T) {
	t.Setenv("XDG_DATA_DIRS", "")
	r := icons.RegistryAt(t.TempDir())
	if err := r.EnsureSetup(); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(r.IconsDir(), "ld-x-symbolic.svg")
	legacy := "<svg width='16' height='16'><path d='M2 8L14 8' stroke='rgb(0,0,0)' fill='none' gpa:stroke='foreground'/></svg>"
	if err := os.WriteFile(stale, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeIconSearch{}
	initIcons(r, fake.search())()
	got, _ := os.ReadFile(stale)
	if want, _ := icons.ToSymbolic(legacy); string(got) != want {
		t.Errorf("stale icon not migrated:\n%s", got)
	}
}

func TestInitIconsFailureLeavesSearchAlone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeIconSearch{paths: []string{"/usr/share/icons"}}
	initIcons(icons.RegistryAt(file), fake.search())()
	if paths, refreshes := fake.state(); !slices.Equal(paths, []string{"/usr/share/icons"}) || refreshes != 0 {
		t.Errorf("a registry that cannot be set up touched the search path: %v, %d refreshes", paths, refreshes)
	}
}

// bundledHicolorIndex declares the bundled icons' one directory, as
// the hicolor theme an installed lys-shell extends does.
const bundledHicolorIndex = "[Icon Theme]\nName=Hicolor\nDirectories=scalable/actions\n\n" +
	"[scalable/actions]\nSize=48\nMinSize=16\nMaxSize=512\nType=Scalable\n"

// useBundledIcons resolves theme icons from the repository's bundled
// set alone, laid out as the hicolor theme an installed lys-shell
// extends, so icons the machine has installed elsewhere cannot answer.
func useBundledIcons(t *testing.T) {
	t.Helper()
	bundled, err := filepath.Abs(filepath.Join("..", "..", "resources", "icons", "hicolor", "scalable"))
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	hicolor := filepath.Join(base, "hicolor")
	if err := os.Mkdir(hicolor, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hicolor, "index.theme"), []byte(bundledHicolorIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bundled, filepath.Join(hicolor, "scalable")); err != nil {
		t.Fatal(err)
	}
	prev := widget.IconSearchPaths()
	widget.SetIconSearchPaths([]string{base})
	widget.RefreshIcons()
	t.Cleanup(func() {
		widget.SetIconSearchPaths(prev)
		widget.RefreshIcons()
	})
}

func reportedIcons(cfg *config.Config) []config.Diagnostic {
	var got []config.Diagnostic
	ReportMissingIcons(cfg, func(d config.Diagnostic) { got = append(got, d) })
	return got
}

// Every icon the defaults name resolves from the bundled set, so a
// stock config warns about nothing.
func TestDefaultIconsAreBundled(t *testing.T) {
	useBundledIcons(t)
	for _, d := range reportedIcons(config.Defaults()) {
		t.Errorf("a default icon is not bundled: %s", d.Field("Icon"))
	}
}

// An icon a user config names that no theme provides, such as one from
// the Tabler and Material sets lys-shell no longer bundles, is a
// warning naming it and how to get it, not a silently blank glyph; a
// user-imported cm- icon cannot be fetched, so it points at import.
func TestAMissingConfigIconIsReported(t *testing.T) {
	useBundledIcons(t)
	cfg := config.Defaults()
	cfg.Battery.ChargingIcon = "md-battery_android_frame_bolt-symbolic"
	cfg.Network.WiredConnectedIcon = "cm-wired-symbolic"
	cfg.Clock.IconName = "ld-clock-symbolic"
	got := reportedIcons(cfg)
	want := []struct{ icon, source, hint string }{
		{"cm-wired-symbolic", "user-imported", "lys-shell icons import <file.svg> wired"},
		{"md-battery_android_frame_bolt-symbolic", "material", "lys-shell icons sync"},
	}
	if len(got) != len(want) {
		t.Fatalf("reported %d icons, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		d := got[i]
		if d.Kind != config.DiagnosticWarning || d.Field("Icon") != w.icon || d.Field("Source") != w.source || d.Hint != w.hint {
			t.Errorf("diagnostic %d = %+v, want a warning for %s from %s hinting %q", i, d, w.icon, w.source, w.hint)
		}
	}
}
