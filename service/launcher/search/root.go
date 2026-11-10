package search

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-desktop/settingspage"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/answer"
	"github.com/stubbedev/lys-shell/service/launcher/charnames"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// Deps are the shell services a search session draws on.
type Deps struct {
	Env Env
	// Clipboard is the history; nil without data-control.
	Clipboard modes.ClipboardHistory
	// Vault builds a vault level; nil without a vault.
	Vault func() launcher.Mode
	// SettingsPages asks lys-settings for its pages.
	SettingsPages func(context.Context) ([]settingspage.Page, error)
	// Windows is the compositor's; nil where none is known.
	Windows modes.WindowBackend
	// Browser reads the browsers' bookmarks and history; nil offers neither.
	Browser *BrowserIndex
	// Toggles, Media and Capture read the rows of the shell's services; a
	// nil one offers none.
	Toggles, Media, Capture func(ctx context.Context) []Control
	// SkinTone is what person and hand emoji show in; SetSkinTone keeps a tone
	// picked from an emoji's actions as the new default (nil keeps none).
	SkinTone    modes.SkinTone
	SetSkinTone func(modes.SkinTone)
	// Rates are the currency reference rates, kept across sessions; nil
	// answers no currencies.
	Rates *answer.Rates
	// Sources keeps the stable sources' rows between sessions; nil loads
	// every source in every session.
	Sources *Sources
}

// frecencyFileName and fileIndexFileName are the session's files, under
// the state and cache directories.
const (
	frecencyFileName  = "launcher-frecency.json"
	fileIndexFileName = "launcher-files.idx"
)

// FrecencyPath is where uses are remembered.
func FrecencyPath() (string, bool) {
	dir, ok := xdg.OS.StateDir(identity.Name)
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

// rootEnv is the environment the root's directories are found in: the
// process's, with home for its HOME.
func rootEnv(home string) xdg.Env {
	return func(name string) string {
		if name == "HOME" {
			return home
		}
		return os.Getenv(name)
	}
}

// NewRoot builds the root level from the configuration: one provider per
// enabled source, nothing loaded yet. A source whose directories neither
// the configuration nor the environment names is left out, and logged.
func NewRoot(cfg config.LauncherSearchConfig, power config.PowerConfig, d Deps, home string) *Level {
	has := func(p config.LauncherSearchProvider) bool { return slices.Contains(cfg.Providers, p) }
	env := rootEnv(home)
	root := &Level{Placeholder: i18n.T("launcher-placeholder-search")}
	add := func(p config.LauncherSearchProvider, make func() Provider) {
		if has(p) {
			root.Providers = append(root.Providers, make())
		}
	}
	add(config.SearchCalculator, func() Provider {
		return NewCalculator(d.Rates, answer.NewCurrencyParser(sessionLanguage()), answer.NewZones(time.LoadLocation), time.Now)
	})
	add(config.SearchShell, func() Provider { return NewShell(d.Env) })
	var keys []string
	keep := func(key string, p source, spec keepSpec) Provider {
		if d.Sources == nil {
			return p.Provider
		}
		keys = append(keys, key)
		return d.Sources.keep(key, p, spec)
	}
	add(config.SearchApps, func() Provider {
		dirs := desktopentry.ApplicationDirs()
		return keep("apps\x00"+strings.Join(dirs, "\x00"), wholeSource(NewApps(d.Env, dirs)), keepSpec{paths: dirs, recursive: true})
	})
	add(config.SearchWindows, func() Provider { return NewWindows(d.Windows) })
	add(config.SearchSystem, func() Provider { return NewSystem(d.Env, power) })
	add(config.SearchLinks, func() Provider { return NewLinks(d.Env, cfg.Quicklinks, cfg.Snippets, cfg.Previews) })
	if has(config.SearchScripts) {
		if dir, err := commandsDir(cfg.CommandsDir, home, env); err != nil {
			log.Printf("launcher: no scripts: %v", err)
		} else {
			root.Providers = append(root.Providers, keep("scripts\x00"+dir, wholeSource(NewScripts(d.Env, dir)), keepSpec{paths: []string{dir}}))
		}
	}
	if has(config.SearchSettings) && d.SettingsPages != nil {
		root.Providers = append(root.Providers, keep("settings", wholeSource(NewSettings(d.Env, d.SettingsPages)), keepSpec{}))
	}
	if has(config.SearchFiles) {
		if fc, err := filesConfig(cfg, home, env); err != nil {
			log.Printf("launcher: no files: %v", err)
		} else {
			key := fmt.Sprintf("files\x00%q\x00%q\x00%s\x00%s\x00%t", fc.Roots, fc.Exclude, fc.CachePath, fc.RecentPath, fc.Previews)
			root.Providers = append(root.Providers, keep(key, stagedSource(NewFiles(d.Env, fc)), keepSpec{paths: []string{filepath.Dir(fc.RecentPath), filepath.Dir(fc.CachePath)}, maxAge: fileIndexMaxAge}))
		}
	}
	if has(config.SearchClipboard) && d.Clipboard != nil {
		root.Providers = append(root.Providers, NewClipboard(d.Clipboard, cfg.Previews))
	}
	add(config.SearchEmoji, func() Provider {
		return keep(fmt.Sprintf("emoji\x00%d", d.SkinTone), wholeSource(NewEmoji(modes.AvailableEmoji, d.SkinTone, d.SetSkinTone)), keepSpec{})
	})
	add(config.SearchCharacters, func() Provider { return NewCharacters(charnames.Shared) })
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
	if has(config.SearchEmoji) {
		levels.Emoji = func() *Level { return EmojiLevel(modes.AvailableEmoji, d.SkinTone, d.SetSkinTone) }
	}
	if has(config.SearchCharacters) {
		levels.Characters = func() *Level { return CharactersLevel(charnames.Shared) }
	}
	if has(config.SearchVault) && d.Vault != nil {
		levels.Vault = func() *Level { return ModeLevel(d.Vault(), i18n.T("launcher-picker-vault")) }
	}
	if pickers := NewPickers(history, levels, cfg.Previews); len(pickers.items) > 0 {
		root.Providers = append(root.Providers, pickers)
	}
	add(config.SearchWeb, func() Provider { return NewWeb(d.Env, cfg.WebEngine, cfg.WebURL) })
	if d.Sources != nil {
		d.Sources.retain(keys)
	}
	return root
}

// commandsDir is where the scripts are: the configured directory, else
// launcher/commands under the shell's XDG config directory.
func commandsDir(configured, home string, env xdg.Env) (string, error) {
	if configured != "" {
		return expandHome(configured, home), nil
	}
	dir, ok := env.ConfigDir(identity.Name)
	if !ok {
		return "", errors.New("no commands directory: neither XDG_CONFIG_HOME nor HOME is an absolute path")
	}
	return filepath.Join(dir, "launcher", "commands"), nil
}

// filesConfig is what the files source reads: the configured roots, home
// without any, the index under the shell's XDG cache directory and the
// desktop's recent files under the XDG data home.
func filesConfig(cfg config.LauncherSearchConfig, home string, env xdg.Env) (FilesConfig, error) {
	roots := make([]string, 0, max(len(cfg.FileRoots), 1))
	for _, r := range cfg.FileRoots {
		roots = append(roots, expandHome(r, home))
	}
	if len(roots) == 0 {
		roots = []string{home}
	}
	cache, ok := env.CacheDir(identity.Name)
	if !ok {
		return FilesConfig{}, errors.New("no directory for the index: neither XDG_CACHE_HOME nor HOME is an absolute path")
	}
	data, ok := env.DataHome()
	if !ok {
		return FilesConfig{}, errors.New("no directory of recent files: neither XDG_DATA_HOME nor HOME is an absolute path")
	}
	return FilesConfig{
		Roots: roots, Exclude: cfg.FileExclude, CachePath: filepath.Join(cache, fileIndexFileName), RecentPath: filepath.Join(data, "recently-used.xbel"), Home: home,
		Previews: cfg.Previews, Now: time.Now,
	}, nil
}

func sessionLanguage() string {
	if langs := i18n.RequestedLanguages(); len(langs) > 0 {
		return langs[0].Language()
	}
	return ""
}
