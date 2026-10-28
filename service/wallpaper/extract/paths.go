package extract

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/version"
)

// errNoHome is the lookup failure when neither the XDG variable nor
// HOME is an absolute path.
var errNoHome = errors.New("neither the XDG base directory nor HOME is an absolute path")

// created creates an XDG lookup's directory when absent, as
// ConfigPaths::cache_dir / data_dir do; a failed lookup is errNoHome.
func created(dir string, ok bool) (string, error) {
	if !ok {
		return "", errNoHome
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // matches create_dir_all's default mode
		return "", err
	}
	return dir, nil
}

// cacheDir is $XDG_CACHE_HOME/lys-shell (~/.cache/lys-shell), created on demand.
func cacheDir() (string, error) { return created(xdg.OS.CacheDir(version.Name)) }

// dataDir is $XDG_DATA_HOME/lys-shell (~/.local/share/lys-shell), created on demand.
func dataDir() (string, error) { return created(xdg.OS.DataDir(version.Name)) }

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
	base, ok := xdg.OS.CacheHome()
	if !ok {
		return "", errNoHome
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
