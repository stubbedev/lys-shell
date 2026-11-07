// Package permissions is what apps were granted through the portal:
// the frontend's permission store and lys's own shortcut store, read
// as one per-app model and revoked from it.
package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/xdg"

	"github.com/stubbedev/lys-shell/internal/version"
)

// Shortcut is one global shortcut an app bound, as lys keeps it for the
// app's later sessions.
type Shortcut struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// ShortcutsPath is the shortcut store,
// $XDG_STATE_HOME/lys-shell/portal-shortcuts.json; "" without a state
// directory.
func ShortcutsPath() string {
	dir, ok := xdg.OS.StateDir(version.Name)
	if !ok {
		return ""
	}
	return filepath.Join(dir, "portal-shortcuts.json")
}

// ReadShortcuts is the shortcuts each app bound, by app id; a store
// never written holds none.
func ReadShortcuts(path string) (map[string][]Shortcut, error) {
	all := map[string][]Shortcut{}
	data, err := os.ReadFile(path) //nolint:gosec // the shortcut store under the state directory
	if errors.Is(err, fs.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the shortcut store %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parse the shortcut store %s: %w", path, err)
	}
	return all, nil
}

// UpdateShortcuts applies edit to the store and replaces it, holding a
// lock every writer takes, in this process or another (the portal
// binding, the CLI revoking), so no update is lost.
func UpdateShortcuts(path string, edit func(map[string][]Shortcut)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create the shortcut store's directory: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // beside the shortcut store
	if err != nil {
		return fmt.Errorf("open the shortcut store's lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock the shortcut store: %w", err)
	}
	all, err := ReadShortcuts(path)
	if err != nil {
		return err
	}
	edit(all)
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the shortcut store: %w", err)
	}
	if err := atomicfile.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write the shortcut store %s: %w", path, err)
	}
	return nil
}
