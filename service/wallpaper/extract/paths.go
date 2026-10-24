package extract

import (
	"errors"
	"os"
	"path/filepath"
)

// errNoHome is the lookup failure when neither the XDG variable nor
// HOME is set.
var errNoHome = errors.New("neither the XDG base directory nor HOME is set")

// xdgBase resolves an XDG base directory: $env, else $HOME/fallback.
func xdgBase(env, fallback string) (string, error) {
	if dir := os.Getenv(env); dir != "" {
		return dir, nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", errNoHome
	}
	return filepath.Join(home, fallback), nil
}

// lysShellDir resolves $base/lys-shell and creates it when absent, as
// ConfigPaths::cache_dir / data_dir do.
func lysShellDir(env, fallback string) (string, error) {
	base, err := xdgBase(env, fallback)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "lys-shell")
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // matches create_dir_all's default mode
		return "", err
	}
	return dir, nil
}

// cacheDir is $XDG_CACHE_HOME/lys-shell (~/.cache/lys-shell), created on demand.
func cacheDir() (string, error) { return lysShellDir("XDG_CACHE_HOME", ".cache") }

// dataDir is $XDG_DATA_HOME/lys-shell (~/.local/share/lys-shell), created on demand.
func dataDir() (string, error) { return lysShellDir("XDG_DATA_HOME", ".local/share") }

// MatugenColorsPath is where lys-shell saves matugen's JSON output
// ($XDG_CACHE_HOME/lys-shell/matugen-colors.json); the lys-shell cache dir is
// created on demand.
func MatugenColorsPath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "matugen-colors.json"), nil
}

// WallustColorsPath is the wallust template's target
// ($XDG_CACHE_HOME/lys-shell/wallust-colors.json); the lys-shell cache dir is
// created on demand.
func WallustColorsPath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "wallust-colors.json"), nil
}

// PywalColorsPath is pywal's own output ($XDG_CACHE_HOME/wal/colors.json);
// nothing is created, pywal owns that directory.
func PywalColorsPath() (string, error) {
	base, err := xdgBase("XDG_CACHE_HOME", ".cache")
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "wal", "colors.json"), nil
}

// ColorsPath is the cache file the given tool leaves its palette in;
// false for None.
func ColorsPath(t Tool) (string, bool, error) {
	var (
		path string
		err  error
	)
	switch t {
	case Matugen:
		path, err = MatugenColorsPath()
	case Wallust:
		path, err = WallustColorsPath()
	case Pywal:
		path, err = PywalColorsPath()
	default:
		return "", false, nil
	}
	return path, err == nil, err
}
