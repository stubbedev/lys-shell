package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// alive reports whether pid is a running process (not a zombie).
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	return err == nil && !strings.Contains(string(stat), ") Z ")
}

// Ending an owned command's context stops the children its script
// started, not only the shell; until then they run.
func TestOwnedStopsTheScriptsChildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := Owned(ctx, "sleep 30 & echo $! > "+pidFile+"; wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	waitFor(t, "the script's child", func() bool {
		data, _ := os.ReadFile(pidFile)
		child, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		return child > 0
	})
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	if !alive(child) {
		t.Fatal("the script's child is not running before the context ends")
	}
	cancel()
	_ = cmd.Wait()
	waitFor(t, "the child to stop with its shell", func() bool { return !alive(child) })
}
