// Package xdg resolves lys-shell's XDG base directories.
package xdg

import (
	"os"
	"path/filepath"
)

// StateDir is lys-shell's state directory, $XDG_STATE_HOME/lys-shell or
// ~/.local/state/lys-shell (wayle-core paths.rs state_dir); false when
// neither variable is set.
func StateDir() (string, bool) { return baseDir("XDG_STATE_HOME", ".local/state") }

// CacheDir is lys-shell's cache directory, $XDG_CACHE_HOME/lys-shell or
// ~/.cache/lys-shell; false when neither variable is set.
func CacheDir() (string, bool) { return baseDir("XDG_CACHE_HOME", ".cache") }

// DataDir is lys-shell's data directory, $XDG_DATA_HOME/lys-shell or
// ~/.local/share/lys-shell; false when neither variable is set.
func DataDir() (string, bool) { return baseDir("XDG_DATA_HOME", ".local/share") }

// CacheHome is the user's cache directory itself, $XDG_CACHE_HOME or
// ~/.cache, where caches shared across apps live (the thumbnails);
// false when neither variable is set.
func CacheHome() (string, bool) { return home("XDG_CACHE_HOME", ".cache") }

// baseDir is lys-shell's directory under the base directory env names,
// else under home's fallback.
func baseDir(env, fallback string) (string, bool) {
	dir, ok := home(env, fallback)
	if !ok {
		return "", false
	}
	return filepath.Join(dir, "lys-shell"), true
}

// home is the base directory env names, else home's fallback.
func home(env, fallback string) (string, bool) {
	if dir := os.Getenv(env); dir != "" {
		return dir, true
	}
	h := os.Getenv("HOME")
	if h == "" {
		return "", false
	}
	return filepath.Join(h, fallback), true
}
