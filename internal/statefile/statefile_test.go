package statefile

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The newest write wins, in a 0700 directory with 0600 content; a file
// without a path writes nothing.
func TestTheNewestWriteWins(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "state", "view")}
	for i := range 50 {
		f.Write([]byte(strconv.Itoa(i)))
	}
	f.Wait()
	if b, err := os.ReadFile(f.Path); err != nil || string(b) != "49" {
		t.Errorf("after 50 writes the file reads %q, %v", b, err)
	}
	info, err := os.Stat(f.Path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the file is %v, %v", info.Mode(), err)
	}
	if dir, err := os.Stat(filepath.Dir(f.Path)); err != nil || dir.Mode().Perm() != 0o700 {
		t.Errorf("the directory is %v, %v", dir.Mode(), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(f.Path))
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want the file alone", len(entries))
	}
	var none File
	none.Write([]byte("x"))
	none.Wait()
}
