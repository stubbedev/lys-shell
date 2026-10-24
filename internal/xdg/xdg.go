// Package xdg resolves lys-shell's XDG base directories.
package xdg

import (
	"os"
	"path/filepath"
)

// StateDir is lys-shell's state directory, $XDG_STATE_HOME/lys-shell or
// ~/.local/state/lys-shell (wayle-core paths.rs state_dir); false when
// neither variable is set.
func StateDir() (string, bool) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "lys-shell"), true
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", false
	}
	return filepath.Join(home, ".local/state/lys-shell"), true
}

// CacheDir is lys-shell's cache directory, $XDG_CACHE_HOME/lys-shell or
// ~/.cache/lys-shell; false when neither variable is set.
func CacheDir() (string, bool) {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, "lys-shell"), true
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", false
	}
	return filepath.Join(home, ".cache/lys-shell"), true
}
