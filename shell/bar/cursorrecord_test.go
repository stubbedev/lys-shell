package bar

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLysLogin puts a lys-login on PATH that runs script with $out set
// to a file it may write, and returns that file's path.
func fakeLysLogin(t *testing.T, script string) string {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	body := "#!" + sh + "\nout=" + out + "\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "lys-login"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return out
}

// The record runs `lys-login record-cursor` with the shell's own
// environment, where the session's cursor is.
func TestRecordCursorRunsLysLoginWithTheSessionsCursor(t *testing.T) {
	out := fakeLysLogin(t, `printf '%s|%s|%s\n' "$*" "$XCURSOR_THEME" "$XCURSOR_SIZE" > "$out"; echo wrote /var/lib/lys-login/cursor/1000`)
	t.Setenv("XCURSOR_THEME", "Bibata-Modern-Ice")
	t.Setenv("XCURSOR_SIZE", "32")
	if err := recordCursor(context.Background()); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "record-cursor|Bibata-Modern-Ice|32\n"; string(got) != want {
		t.Errorf("lys-login ran with %q, want %q", got, want)
	}
}

// A refused record, or no display manager to take it, is an error
// naming lys-login's reason, for Run to log; it never fails the shell.
func TestRecordCursorReportsLysLoginsFailure(t *testing.T) {
	fakeLysLogin(t, `echo "lys-login's display manager is not running" >&2; exit 1`)
	err := recordCursor(context.Background())
	if err == nil || !strings.Contains(err.Error(), "exit status 1: lys-login's display manager is not running") {
		t.Errorf("failed record: %v", err)
	}
}

// Without lys-login installed the record is an error, not a panic or
// a hang.
func TestRecordCursorWithoutLysLogin(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := recordCursor(context.Background()); err == nil || !strings.Contains(err.Error(), "lys-login record-cursor") {
		t.Errorf("missing lys-login: %v", err)
	}
}
