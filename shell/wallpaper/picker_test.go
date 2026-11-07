package wallpaper

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/wallpaper"
)

type recordedChange struct {
	path  string
	value any
}

func testPicker(t *testing.T, cfg *config.Config, monitors ...string) (*Picker, *wallpaper.Service, *[]recordedChange) {
	t.Helper()
	svc := newSvc(monitors...)
	var changes []recordedChange
	s := &Shell{svc: svc}
	p := s.NewPicker(func() *config.Config { return cfg }, func(path string, value any) {
		changes = append(changes, recordedChange{path, value})
	})
	return p, svc, &changes
}

func TestPickerEscapeAfterAPreviewLeavesWallpaperAndConfig(t *testing.T) {
	dir := files(t, "a.png", "b.png")
	cfg := config.Defaults()
	p, svc, changes := testPicker(t, cfg, "DP-1", "DP-2")
	if err := svc.SetWallpaper(filepath.Join(dir, "a.png"), ""); err != nil {
		t.Fatal(err)
	}
	before := svc.Monitors()
	if err := p.Preview(filepath.Join(dir, "b.png"), "DP-1"); err != nil {
		t.Fatal(err)
	}
	if got := p.Shown("DP-1"); got != filepath.Join(dir, "b.png") {
		t.Errorf("DP-1 previews %s, want b", got)
	}
	if got := p.Shown("DP-2"); got != filepath.Join(dir, "a.png") {
		t.Errorf("DP-2 shows %s during DP-1's preview, want a", got)
	}
	p.EndPreview()
	if after := svc.Monitors(); !reflect.DeepEqual(after, before) {
		t.Errorf("after the preview %v, want %v", after, before)
	}
	if len(*changes) != 0 {
		t.Errorf("a preview changed the config: %v", *changes)
	}
	if err := p.Preview(filepath.Join(dir, "missing.png"), "DP-1"); err == nil {
		t.Error("previewing a missing image succeeded")
	}
}

func TestPickerCommitWritesOnlyTheFocusedMonitorsEntry(t *testing.T) {
	dir := files(t, "a.png", "b.png")
	cfg := config.Defaults()
	cfg.Wallpaper.Monitors = []config.MonitorWallpaperConfig{
		{Name: "DP-2", FitMode: config.FitFit, Wallpaper: "/keep/dp2.png"},
		{Name: "DP-1", FitMode: config.FitCenter, Wallpaper: "/old/dp1.png"},
	}
	p, svc, changes := testPicker(t, cfg, "DP-1", "DP-2")
	b := filepath.Join(dir, "b.png")
	if err := p.Preview(b, "DP-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.Commit(b, "DP-1"); err != nil {
		t.Fatal(err)
	}
	p.EndPreview()
	if got := p.Shown("DP-1"); got != b {
		t.Errorf("DP-1 shows %s after the commit, want b", got)
	}
	want := []recordedChange{{"wallpaper.monitors", []any{
		map[string]any{"name": "DP-2", "fit-mode": "fit", "wallpaper": "/keep/dp2.png"},
		map[string]any{"name": "DP-1", "fit-mode": "center", "wallpaper": b},
	}}}
	if !reflect.DeepEqual(*changes, want) {
		t.Errorf("config changes %#v, want %#v", *changes, want)
	}
	if _, ok := svc.Wallpaper("DP-2"); ok {
		t.Error("committing DP-1 set DP-2's wallpaper")
	}
}

func TestPickerCommitAddsAnEntryForAnUnlistedMonitor(t *testing.T) {
	cfg := config.Defaults()
	key, value := commitChange(cfg.Wallpaper, "/w/x.png", "eDP-1")
	want := []any{map[string]any{"name": "eDP-1", "fit-mode": string(cfg.Wallpaper.FitMode), "wallpaper": "/w/x.png"}}
	if key != "wallpaper.monitors" || !reflect.DeepEqual(value, want) {
		t.Errorf("commitChange = %s %#v", key, value)
	}
}

func TestPickerCommitEveryMonitorSetsTheGlobalImageInOneChange(t *testing.T) {
	dir := files(t, "a.png")
	cfg := config.Defaults()
	cfg.Wallpaper.Monitors = []config.MonitorWallpaperConfig{{Name: "DP-1", FitMode: config.FitFit, Wallpaper: "/old.png"}}
	p, svc, changes := testPicker(t, cfg, "DP-1", "DP-2")
	a := filepath.Join(dir, "a.png")
	if err := p.Commit(a, ""); err != nil {
		t.Fatal(err)
	}
	want := []recordedChange{{"wallpaper", map[string]any{
		"wallpaper": a,
		"monitors":  []any{map[string]any{"name": "DP-1", "fit-mode": "fit", "wallpaper": ""}},
	}}}
	if !reflect.DeepEqual(*changes, want) {
		t.Errorf("config changes %#v, want %#v", *changes, want)
	}
	for _, m := range []string{"DP-1", "DP-2"} {
		if got, _ := svc.Wallpaper(m); got != a {
			t.Errorf("%s shows %s, want a", m, got)
		}
	}
}

func TestPickerImagesComeFromTheConfiguredFolders(t *testing.T) {
	one, two := files(t, "b.png", "notes.txt"), files(t, "a.jpg")
	cfg := config.Defaults()
	cfg.Wallpaper.PickerDirectories = []string{one, two, filepath.Join(one, "missing")}
	p, _, _ := testPicker(t, cfg)
	got, err := p.Images()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(one, "b.png"), filepath.Join(two, "a.jpg")}; !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Errorf("images %v, want %v", got, want)
	}
	cfg.Wallpaper.PickerDirectories = nil
	cfg.Wallpaper.CyclingDirectory = two
	p.pictures = func() string { return filepath.Dir(one) }
	if dirs := cfg.Wallpaper.PickerDirs(p.pictures()); !slices.Equal(dirs, []string{two, filepath.Join(filepath.Dir(one), "Wallpapers")}) {
		t.Errorf("default folders %v", dirs)
	}
}
