package sockdir

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestADirHoldsASocketWhateverTheTestIsCalledAndGoesAwayAfterwards(t *testing.T) {
	var dir string
	t.Run(strings.Repeat("a test name far longer than any socket path may be ", 4), func(t *testing.T) {
		dir = Dir(t)
		ln, err := net.Listen("unix", filepath.Join(dir, "lys-shell", "..", "widget.sock"))
		if err != nil {
			t.Fatalf("bind in %s: %v", dir, err)
		}
		_ = ln.Close()
		if strings.Contains(dir, "a test name") {
			t.Errorf("dir %s carries the test name", dir)
		}
	})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir %s outlived its test: %v", dir, err)
	}
}

func TestTheTempDirOfALongTestCannotHoldASocket(t *testing.T) {
	t.Run(strings.Repeat("a test name far longer than any socket path may be ", 4), func(t *testing.T) {
		if ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "widget.sock")); err == nil {
			_ = ln.Close()
			t.Fatal("a socket bound under t.TempDir() of a long test: sockdir would be unnecessary")
		}
	})
}
