package cursorrecord

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

// The cursor a session records is the one the greeter reads back from
// the user's home.
func TestRecordIsReadBackFromTheHome(t *testing.T) {
	home := t.TempDir()
	written, err := Record(env(map[string]string{"HOME": home, "XCURSOR_THEME": "Bibata", "XCURSOR_SIZE": "32"}))
	if err != nil || !written {
		t.Fatalf("Record = %v, %v; want a record written", written, err)
	}
	got, err := Read(home)
	if err != nil || got != (Cursor{"Bibata", 32}) {
		t.Fatalf("Read = %+v, %v; want Bibata at 32", got, err)
	}
}

// A blank session writes nothing, and leaves an earlier record whole.
func TestBlankSessionKeepsTheRecord(t *testing.T) {
	home := t.TempDir()
	if written, err := Record(env(map[string]string{"HOME": home})); err != nil || written {
		t.Fatalf("a blank environment: Record = %v, %v; want nothing written", written, err)
	}
	if _, err := Read(home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Read after a blank session = %v, want no record", err)
	}
	if _, err := Record(env(map[string]string{"HOME": home, "XCURSOR_THEME": "Adwaita", "HYPRCURSOR_SIZE": "28"})); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(env(map[string]string{"HOME": home, "XCURSOR_SIZE": "not a size"})); err != nil {
		t.Fatal(err)
	}
	if got, err := Read(home); err != nil || got != (Cursor{"Adwaita", 28}) {
		t.Fatalf("Read = %+v, %v; want the HYPRCURSOR_SIZE fallback kept whole", got, err)
	}
}

// One known side is recorded alone, and read back without the other.
func TestHalfACursor(t *testing.T) {
	home := t.TempDir()
	if _, err := Record(env(map[string]string{"HOME": home, "XCURSOR_SIZE": "48"})); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".local/state/lys-shell", fileName))
	if err != nil || string(data) != "size=48\n" {
		t.Fatalf("record = %q, %v", data, err)
	}
	if got, _ := Read(home); got != (Cursor{Size: 48}) {
		t.Fatalf("Read = %+v", got)
	}
}

// Without a state directory Record fails naming why, and Read refuses a
// relative home.
func TestNoStateDirectory(t *testing.T) {
	if _, err := Record(env(map[string]string{"XCURSOR_THEME": "Bibata"})); err == nil {
		t.Fatal("Record without HOME or XDG_STATE_HOME succeeded")
	}
	if _, err := Read("relative/home"); err == nil {
		t.Fatal("Read of a relative home succeeded")
	}
}
