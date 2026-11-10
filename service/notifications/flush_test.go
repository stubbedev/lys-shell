package notifications

import (
	"bytes"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// captureLog collects the process log until the test ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	out := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(out) })
	return buf
}

// FlushWrites waits for every detached change still running and for the
// write it queues, however late the change gets to it: the flag a
// detached SetDND saves is on disk when the flush returns.
func TestFlushWritesWaitsForDetachedChangesAndTheirWrites(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	synctest.Test(t, func(t *testing.T) {
		s := NewService()
		first, second := make(chan struct{}), make(chan struct{})
		s.Detach(func() { <-first })
		s.Detach(func() {
			<-second
			s.SetDND(true)
		})
		flushed := make(chan struct{})
		go func() {
			defer close(flushed)
			s.FlushWrites(time.Hour)
		}()
		for _, release := range []chan struct{}{first, second} {
			synctest.Wait()
			select {
			case <-flushed:
				t.Error("FlushWrites returned under a detached change still running")
			default:
			}
			close(release)
		}
		<-flushed
		if !NewService().DND() {
			t.Error("the write the detached change queued had not landed when FlushWrites returned")
		}
	})
}

// A detached change that hangs holds FlushWrites for its timeout and no
// longer, and the log says what did not finish.
func TestFlushWritesGivesUpOnADetachedChangeThatHangs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	synctest.Test(t, func(t *testing.T) {
		logged := captureLog(t)
		s := NewService()
		hung := make(chan struct{})
		defer close(hung)
		s.Detach(func() { <-hung })
		start := time.Now()
		s.FlushWrites(time.Minute)
		if waited := time.Since(start); waited != time.Minute {
			t.Errorf("FlushWrites waited %v for a hung change, want its timeout of %v", waited, time.Minute)
		}
		if want := "notifications: a detached change did not finish in 1m0s"; !strings.Contains(logged.String(), want) {
			t.Errorf("logged %q, want %q", logged.String(), want)
		}
	})
}

// A save replaces the file whole: a reader that opened it before reads
// the flag before to its end, the new flag is what loads, and no
// temporary is left beside it.
func TestADNDSaveReplacesTheFileWhole(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	f := dndFile(filepath.Join(dir, "dnd"))
	if err := f.save(false); err != nil {
		t.Fatal(err)
	}
	before, err := os.Open(string(f))
	if err != nil {
		t.Fatal(err)
	}
	defer before.Close()
	if err := f.save(true); err != nil {
		t.Fatal(err)
	}
	old, err := io.ReadAll(before)
	if err != nil || string(old) != "off\n" {
		t.Errorf("a reader from before the save read %q (%v), want the flag before, whole", old, err)
	}
	if !f.load() {
		t.Error("the saved flag does not load")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "dnd" {
		t.Errorf("the state dir holds %v (%v), want the flag alone", entries, err)
	}
}

// A save that cannot write reports what it was saving and where, and
// leaves the flag before in place.
func TestAFailedDNDSaveIsReportedAndLeavesTheFlagBefore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only directory")
	}
	dir := t.TempDir()
	f := dndFile(filepath.Join(dir, "dnd"))
	if err := f.save(true); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	err := f.save(false)
	if err == nil || !strings.HasPrefix(err.Error(), "save do-not-disturb off: ") || !strings.Contains(err.Error(), string(f)) {
		t.Errorf("a save into a read-only directory: %v, want an error naming the flag and %s", err, f)
	}
	if !f.load() {
		t.Error("the failed save changed the flag")
	}
}

// The service logs a save that failed, as nothing waits on it, and
// keeps the session's flag.
func TestAFailedDNDSaveIsLogged(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", notADir)
	s := restartedService(t)
	logged := captureLog(t)
	s.SetDND(true)
	settle(s)
	if want := "notifications: save do-not-disturb on: "; !strings.Contains(logged.String(), want) || !strings.Contains(logged.String(), notADir) {
		t.Errorf("logged %q, want %q naming %s", logged.String(), want, notADir)
	}
	if !s.DND() {
		t.Error("the failed save turned do-not-disturb off for the session")
	}
}
