package search

import (
	"bytes"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/identity"
)

func setXDGHomes(t *testing.T, configHome, cacheHome, dataHome string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("XDG_DATA_HOME", dataHome)
}

// The root's directories are the XDG ones: the variable where it is an
// absolute path, under home where it is unset or relative, and the
// configured commands directory before either.
func TestTheRootsDirectoriesFollowXDG(t *testing.T) {
	cfg := config.DefaultsLauncher().Search
	home, configHome, cacheHome, dataHome := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	env := rootEnv(home)
	for name, tc := range map[string]struct {
		configHome, cacheHome, dataHome string
		commands, index, recent         string
	}{
		"set": {
			configHome, cacheHome, dataHome,
			filepath.Join(configHome, identity.Name, "launcher", "commands"),
			filepath.Join(cacheHome, identity.Name, fileIndexFileName),
			filepath.Join(dataHome, "recently-used.xbel"),
		},
		"unset or relative": {
			"relative", "", "also/relative",
			filepath.Join(home, ".config", identity.Name, "launcher", "commands"),
			filepath.Join(home, ".cache", identity.Name, fileIndexFileName),
			filepath.Join(home, ".local", "share", "recently-used.xbel"),
		},
	} {
		setXDGHomes(t, tc.configHome, tc.cacheHome, tc.dataHome)
		if dir, err := commandsDir("", home, env); err != nil || dir != tc.commands {
			t.Errorf("%s: commands in %q (%v), want %q", name, dir, err, tc.commands)
		}
		fc, err := filesConfig(cfg, home, env)
		if err != nil || fc.CachePath != tc.index || fc.RecentPath != tc.recent {
			t.Errorf("%s: index %q, recent files %q (%v), want %q and %q", name, fc.CachePath, fc.RecentPath, err, tc.index, tc.recent)
		}
	}
	if dir, err := commandsDir("~/cmds", home, env); err != nil || dir != filepath.Join(home, "cmds") {
		t.Errorf("configured commands in %q (%v), want them under home", dir, err)
	}
}

// With neither an XDG directory nor a home, the sources that need one
// are left out and logged instead of reading paths relative to the
// working directory; a configured commands directory still serves.
func TestARootWithoutItsDirectoriesLeavesTheirSourcesOut(t *testing.T) {
	setXDGHomes(t, "", "", "")
	var logged bytes.Buffer
	out := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(out) })
	cfg := config.DefaultsLauncher().Search
	cfg.Providers = []config.LauncherSearchProvider{config.SearchScripts, config.SearchFiles, config.SearchWeb}
	root := NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}}, "")
	if len(root.Providers) != 1 {
		t.Errorf("%d providers without a home, want the web search alone", len(root.Providers))
	}
	for _, want := range []string{"launcher: no scripts: no commands directory", "launcher: no files: no directory for the index"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("logged %q, want %q", logged.String(), want)
		}
	}
	cfg.CommandsDir = t.TempDir()
	if root := NewRoot(cfg, config.DefaultsPower(), Deps{Env: Env{}}, ""); len(root.Providers) != 2 {
		t.Errorf("%d providers with a configured commands directory, want the scripts and the web search", len(root.Providers))
	}
}
