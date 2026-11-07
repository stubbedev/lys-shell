package permissions

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

// Concurrent writers, each with its own lock file handle as two
// processes would have, lose no update; a store never written is empty
// and a corrupt one is an error, never an empty store.
func TestUpdateShortcutsLosesNoUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "portal-shortcuts.json")
	if all, err := ReadShortcuts(path); err != nil || len(all) != 0 {
		t.Fatalf("unwritten store = %v, %v", all, err)
	}
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			app := "org.app" + strconv.Itoa(i)
			if err := UpdateShortcuts(path, func(all map[string][]Shortcut) { all[app] = []Shortcut{{ID: "toggle"}} }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	all, err := ReadShortcuts(path)
	if err != nil || len(all) != 16 {
		t.Fatalf("after 16 writers: %d apps, %v", len(all), err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if all, err := ReadShortcuts(path); err == nil {
		t.Errorf("a corrupt store read as %v", all)
	}
	if err := UpdateShortcuts(path, func(map[string][]Shortcut) {}); err == nil {
		t.Error("an update over a corrupt store succeeded")
	}
}
