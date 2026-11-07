package desktopentry

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeApp installs a desktop entry whose Exec is sh, which is on PATH.
func writeApp(t *testing.T, dir, id, mimes string) {
	t.Helper()
	body := "[Desktop Entry]\nType=Application\nName=" + id + "\nExec=sh\nMimeType=" + mimes + "\n"
	if err := os.WriteFile(filepath.Join(dir, id), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ids(apps []App) []string {
	var out []string
	for _, a := range apps {
		out = append(out, a.ID)
	}
	return out
}

func TestRecommendedFor(t *testing.T) {
	apps := t.TempDir()
	writeApp(t, apps, "viewer.desktop", "image/png;")
	writeApp(t, apps, "editor.desktop", "image/png;text/plain;")
	writeApp(t, apps, "browser.desktop", "text/html;")
	writeApp(t, apps, "hidden.desktop", "image/png;")
	user, system := filepath.Join(t.TempDir(), "mimeapps.list"), filepath.Join(t.TempDir(), "mimeapps.list")
	_ = os.WriteFile(user, []byte("[Default Applications]\nimage/png=gone.desktop;editor.desktop;\n[Removed Associations]\nimage/png=hidden.desktop;\n"), 0o600)
	_ = os.WriteFile(system, []byte("[Added Associations]\nimage/png=browser.desktop;hidden.desktop;\n"), 0o600)

	got := ids(RecommendedFor([]string{apps}, []string{user, system}, "image/png"))
	// The installed default first, then the added browser, then the
	// declarers; the removed one nowhere, the uninstalled default skipped.
	if want := []string{"editor.desktop", "browser.desktop", "viewer.desktop"}; !slices.Equal(got, want) {
		t.Errorf("recommended = %v, want %v", got, want)
	}
	if got := ids(RecommendedFor([]string{apps}, nil, "text/plain")); !slices.Equal(got, []string{"editor.desktop"}) {
		t.Errorf("text/plain = %v", got)
	}
	if got := RecommendedFor([]string{apps}, nil, "video/mp4"); len(got) != 0 {
		t.Errorf("an unhandled type = %v", ids(got))
	}
}

func TestSetDefaultKeepsTheFile(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_CURRENT_DESKTOP", "")
	path := filepath.Join(cfg, "mimeapps.list")
	_ = os.WriteFile(path, []byte("# mine\n[Default Applications]\ntext/plain=old.desktop;\nimage/png=keep.desktop;\n\n[Added Associations]\nimage/png=a.desktop;b.desktop;\n"), 0o600)
	if err := SetDefault("b.desktop", "image/png"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := "# mine\n[Default Applications]\ntext/plain=old.desktop;\nimage/png=b.desktop;\n\n[Added Associations]\nimage/png=b.desktop;a.desktop;\n"
	if string(data) != want {
		t.Errorf("mimeapps.list =\n%s\nwant\n%s", data, want)
	}
	// A fresh file gets both groups.
	_ = os.Remove(path)
	if err := SetDefault("x.desktop", "video/mp4"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if s := string(data); !strings.Contains(s, "[Default Applications]\nvideo/mp4=x.desktop;\n") || !strings.Contains(s, "[Added Associations]\nvideo/mp4=x.desktop;\n") {
		t.Errorf("fresh file =\n%s", s)
	}
}

func TestMimeAppsListsOrder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/c")
	t.Setenv("XDG_CONFIG_DIRS", "/etc/xdg")
	t.Setenv("XDG_DATA_HOME", "/d")
	t.Setenv("XDG_DATA_DIRS", "/usr/share")
	t.Setenv("XDG_CURRENT_DESKTOP", "Hyprland")
	got := MimeAppsLists()
	want := []string{
		"/c/hyprland-mimeapps.list", "/c/mimeapps.list",
		"/etc/xdg/hyprland-mimeapps.list", "/etc/xdg/mimeapps.list",
		"/d/applications/hyprland-mimeapps.list", "/d/applications/mimeapps.list",
		"/usr/share/applications/hyprland-mimeapps.list", "/usr/share/applications/mimeapps.list",
	}
	if !slices.Equal(got, want) {
		t.Errorf("lists = %v", got)
	}
}

func TestDefaultAnswersAsXdgMimeQueryDefault(t *testing.T) {
	root := t.TempDir()
	home, sysCfg, data, sysData := filepath.Join(root, "home", ".config"), filepath.Join(root, "sys", "xdg"), filepath.Join(root, "data"), filepath.Join(root, "sysdata")
	put := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []string{"firefox", "chromium", "evince", "okular", "imv"} {
		put(filepath.Join(data, "applications", a+".desktop"), "[Desktop Entry]\nType=Application\nName="+a+"\nExec=sh\nMimeType=x-scheme-handler/https;application/pdf;image/png;\n")
	}
	put(filepath.Join(home, "mimeapps.list"), "[Default Applications]\nx-scheme-handler/https=gone.desktop;chromium.desktop;\napplication/pdf=okular.desktop\n")
	put(filepath.Join(home, "lys-wm-mimeapps.list"), "[Default Applications]\nx-scheme-handler/https=firefox.desktop\n")
	put(filepath.Join(sysCfg, "mimeapps.list"), "[Default Applications]\nimage/png=imv.desktop;\napplication/pdf=evince.desktop;\n")
	put(filepath.Join(data, "applications", "mimeapps.list"), "[Default Applications]\nimage/png=evince.desktop;\n")
	t.Setenv("HOME", filepath.Dir(home))
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_CONFIG_DIRS", sysCfg)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", sysData)
	for _, c := range []struct{ desktop, mimeType, want string }{
		{"", "x-scheme-handler/https", "chromium.desktop"},
		{"", "application/pdf", "okular.desktop"},
		{"", "image/png", "imv.desktop"},
		{"lys-wm", "x-scheme-handler/https", "firefox.desktop"},
		{"lys-wm", "application/pdf", "okular.desktop"},
		{"lys-wm", "image/png", "imv.desktop"},
	} {
		t.Setenv("XDG_CURRENT_DESKTOP", c.desktop)
		if got, ok := Default(ApplicationDirs(), MimeAppsLists(), c.mimeType); !ok || got.ID != c.want {
			t.Errorf("%q %s: %q %v, want %s as xdg-mime query default answers", c.desktop, c.mimeType, got.ID, ok, c.want)
		}
	}
}

func TestSetDefaultsWritesEveryTypeAndLeavesTheRestByteForByte(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("XDG_CURRENT_DESKTOP", "lys-wm")
	path := filepath.Join(cfg, "mimeapps.list")
	before := "# my associations\n[Added Associations]\nimage/png=imv.desktop;\n\n[Default Applications]\n# browsers\ntext/html=old.desktop;\nimage/png=imv.desktop;\n\n\n"
	_ = os.WriteFile(path, []byte(before), 0o600)
	if err := SetDefaults("firefox.desktop", []string{"x-scheme-handler/http", "x-scheme-handler/https", "text/html"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, keep := range []string{"# my associations\n[Added Associations]\nimage/png=imv.desktop;\n", "# browsers\ntext/html=firefox.desktop;\nimage/png=imv.desktop;\n"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q:\n%s", keep, got)
		}
	}
	for _, key := range []string{"x-scheme-handler/http=firefox.desktop;", "x-scheme-handler/https=firefox.desktop;"} {
		if strings.Count(got, key) != 2 {
			t.Errorf("%s is not both the default and the first added association:\n%s", key, got)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg, "lys-wm-mimeapps.list")); !os.IsNotExist(err) {
		t.Errorf("the desktop-specific list was written: %v", err)
	}
	entries, _ := os.ReadDir(cfg)
	if len(entries) != 1 {
		t.Errorf("a temporary file was left: %v", entries)
	}
}
