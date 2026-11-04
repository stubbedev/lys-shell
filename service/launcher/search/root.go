package search

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/desktopentry"
	"github.com/stubbedev/lys-shell/internal/version"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// Deps are the shell services a search session draws on.
type Deps struct {
	Env Env
	// Clipboard is the history; nil without data-control.
	Clipboard modes.ClipboardHistory
	// Vault builds a vault level; nil without a vault.
	Vault func() launcher.Mode
	// SettingsPages are lys-settings' pages.
	SettingsPages func() []SettingsPage
	// Windows is the compositor's; nil where none is known.
	Windows modes.WindowBackend
	// Browser reads the browsers' bookmarks and history; nil offers neither.
	Browser *BrowserIndex
	// Toggles, Media and Capture read the rows of the shell's services; a
	// nil one offers none.
	Toggles, Media, Capture func(ctx context.Context) []Control
}

// frecencyFileName and fileIndexFileName are the session's files, under
// the state and cache directories.
const (
	frecencyFileName  = "launcher-frecency.json"
	fileIndexFileName = "launcher-files.idx"
)

// FrecencyPath is where uses are remembered.
func FrecencyPath() (string, bool) {
	dir, ok := xdg.OS.StateDir(version.Name)
	if !ok {
		return "", false
	}
	return filepath.Join(dir, frecencyFileName), true
}

// expandHome resolves a leading ~.
func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return path
}

// NewRoot builds the root level from the configuration: one provider per
// enabled source, nothing loaded yet.
func NewRoot(cfg config.LauncherSearchConfig, power config.PowerConfig, d Deps, home string) *Level {
	has := func(p config.LauncherSearchProvider) bool { return slices.Contains(cfg.Providers, p) }
	root := &Level{Placeholder: i18n.T("launcher-placeholder-search")}
	add := func(p config.LauncherSearchProvider, make func() Provider) {
		if has(p) {
			root.Providers = append(root.Providers, make())
		}
	}
	add(config.SearchCalculator, func() Provider { return Calculator{} })
	add(config.SearchShell, func() Provider { return NewShell(d.Env) })
	add(config.SearchApps, func() Provider { return NewApps(d.Env, desktopentry.ApplicationDirs()) })
	add(config.SearchWindows, func() Provider { return NewWindows(d.Windows) })
	add(config.SearchSystem, func() Provider { return NewSystem(d.Env, power) })
	add(config.SearchLinks, func() Provider { return NewLinks(d.Env, cfg.Quicklinks, cfg.Snippets, cfg.Previews) })
	add(config.SearchScripts, func() Provider { return NewScripts(d.Env, commandsDir(cfg.CommandsDir, home)) })
	if has(config.SearchSettings) && d.SettingsPages != nil {
		root.Providers = append(root.Providers, NewSettings(d.Env, d.SettingsPages))
	}
	add(config.SearchFiles, func() Provider { return NewFiles(d.Env, filesConfig(cfg, home)) })
	if has(config.SearchClipboard) && d.Clipboard != nil {
		root.Providers = append(root.Providers, NewClipboard(d.Clipboard, cfg.Previews))
	}
	add(config.SearchEmoji, func() Provider { return NewEmoji(modes.AvailableEmoji) })
	for _, group := range []struct {
		provider config.LauncherSearchProvider
		load     func(ctx context.Context) []Control
	}{{config.SearchToggles, d.Toggles}, {config.SearchMedia, d.Media}, {config.SearchCapture, d.Capture}} {
		if has(group.provider) && group.load != nil {
			root.Providers = append(root.Providers, NewControls(group.load))
		}
	}
	levels := PickerLevels{}
	if has(config.SearchProcesses) {
		levels.Processes = processesLevel
	}
	if d.Browser != nil {
		if has(config.SearchBookmarks) {
			root.Providers = append(root.Providers, NewBookmarks(d.Env, d.Browser))
			levels.Bookmarks = func() *Level { return bookmarksLevel(d.Env, d.Browser) }
		}
		if has(config.SearchVisited) {
			root.Providers = append(root.Providers, NewVisitedPages(d.Env, d.Browser, has(config.SearchBookmarks)))
			levels.Visited = func() *Level { return visitedLevel(d.Env, d.Browser) }
		}
	}
	var history modes.ClipboardHistory
	if has(config.SearchClipboard) {
		history = d.Clipboard
	}
	var emoji func() []modes.Emoji
	if has(config.SearchEmoji) {
		emoji = modes.AvailableEmoji
	}
	if has(config.SearchVault) && d.Vault != nil {
		levels.Vault = func() *Level { return ModeLevel(d.Vault(), i18n.T("launcher-picker-vault")) }
	}
	if pickers := NewPickers(history, emoji, levels, cfg.Previews); len(pickers.items) > 0 {
		root.Providers = append(root.Providers, pickers)
	}
	add(config.SearchWeb, func() Provider { return NewWeb(d.Env, cfg.WebEngine, cfg.WebURL) })
	return root
}

func commandsDir(configured, home string) string {
	if configured != "" {
		return expandHome(configured, home)
	}
	if dir, ok := xdg.OS.ConfigDir(version.Name); ok {
		return filepath.Join(dir, "launcher", "commands")
	}
	return filepath.Join(home, ".config", version.Name, "launcher", "commands")
}

func filesConfig(cfg config.LauncherSearchConfig, home string) FilesConfig {
	roots := make([]string, 0, max(len(cfg.FileRoots), 1))
	for _, r := range cfg.FileRoots {
		roots = append(roots, expandHome(r, home))
	}
	if len(roots) == 0 {
		roots = []string{home}
	}
	cache := filepath.Join(home, ".cache", version.Name, fileIndexFileName)
	if dir, ok := xdg.OS.CacheDir(version.Name); ok {
		cache = filepath.Join(dir, fileIndexFileName)
	}
	recent := filepath.Join(home, ".local", "share", "recently-used.xbel")
	if dir, ok := xdg.OS.DataDir(version.Name); ok {
		recent = filepath.Join(filepath.Dir(dir), "recently-used.xbel")
	}
	return FilesConfig{
		Roots: roots, Exclude: cfg.FileExclude, CachePath: cache, RecentPath: recent, Home: home,
		Previews: cfg.Previews, Now: time.Now,
	}
}
