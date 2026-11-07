package fswatch

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func newWatcher(t *testing.T, mask uint32, recursive bool, root string) *Watcher {
	t.Helper()
	w, err := New(mask, recursive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Add(root); err != nil {
		t.Fatal(err)
	}
	return w
}

func expectTick(t *testing.T, w *Watcher, what string) {
	t.Helper()
	select {
	case _, ok := <-w.Changed():
		if !ok {
			t.Fatalf("%s: channel closed", what)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: no tick", what)
	}
}

func expectQuiet(t *testing.T, w *Watcher, what string) {
	t.Helper()
	select {
	case <-w.Changed():
		t.Fatalf("%s: unexpected tick", what)
	case <-time.After(150 * time.Millisecond):
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecursiveWatchSeesNestedAndLaterDirectories(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "INBOX", "cur")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t, Changes, true, root)

	write(t, filepath.Join(nested, "1:2,"), "mail")
	expectTick(t, w, "file in an existing subdirectory")

	// A maildir flag change is a rename.
	if err := os.Rename(filepath.Join(nested, "1:2,"), filepath.Join(nested, "1:2,S")); err != nil {
		t.Fatal(err)
	}
	expectTick(t, w, "rename")

	later := filepath.Join(root, "Work")
	if err := os.Mkdir(later, 0o700); err != nil {
		t.Fatal(err)
	}
	expectTick(t, w, "directory created")
	// Let the reader pick the new directory up before writing into it.
	time.Sleep(50 * time.Millisecond)
	write(t, filepath.Join(later, "2:2,"), "mail")
	expectTick(t, w, "file in a directory created after Add")
}

func TestNonRecursiveWatchIgnoresSubdirectoriesAndOtherEvents(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	w := newWatcher(t, unix.IN_MODIFY, false, root)

	write(t, filepath.Join(sub, "deep"), "x")
	expectQuiet(t, w, "write below a non-recursive watch")

	// Creating a file fires IN_CREATE (not in the mask) before the
	// write's IN_MODIFY; only the latter may tick.
	path := filepath.Join(root, "brightness")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	expectQuiet(t, w, "create outside the mask")
	write(t, path, "42")
	expectTick(t, w, "modify in the mask")
}

func TestCloseEndsTheChannelAndRefusesAdds(t *testing.T) {
	root := t.TempDir()
	w, err := New(Changes, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-w.Changed():
		if ok {
			t.Fatal("tick after Close, want a closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("Changed not closed by Close")
	}
	if err := w.Add(root); err == nil {
		t.Error("Add after Close: want an error")
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

func TestBadArgumentsError(t *testing.T) {
	if _, err := New(0, false); err == nil {
		t.Error("empty mask: want an error")
	}
	w, err := New(Changes, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if err := w.Add(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing root: want an error")
	}
}

func newNamed(t *testing.T, root string) *Watcher {
	t.Helper()
	w, err := NewNamed(Changes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	if err := w.Add(root); err != nil {
		t.Fatal(err)
	}
	return w
}

// takeUntil takes batches until done holds for everything taken, or
// fails with what was taken after the liveness bound.
func takeUntil(t *testing.T, w *Watcher, done func([]Event, bool) bool) ([]Event, bool) {
	t.Helper()
	var events []Event
	rescan := false
	deadline := time.After(5 * time.Second)
	for !done(events, rescan) {
		select {
		case <-w.Changed():
			b := w.Take()
			events = append(events, b.Events...)
			rescan = rescan || b.Rescan
		case <-deadline:
			t.Fatalf("taken %+v (rescan %v), never done", events, rescan)
		}
	}
	return events, rescan
}

func TestNamedWatchCoalescesPerName(t *testing.T) {
	root := t.TempDir()
	w := newNamed(t, root)
	path := filepath.Join(root, "download.part")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		if _, err := f.WriteString("x"); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Close()
	write(t, filepath.Join(root, "other"), "y")
	events, rescan := takeUntil(t, w, func(e []Event, _ bool) bool {
		return len(e) > 0 && e[len(e)-1].Name == "other"
	})
	if rescan {
		t.Fatal("a burst of writes asked for a rescan")
	}
	writes := 0
	for _, e := range events {
		if e.Name == "download.part" {
			writes++
		}
	}
	if writes == 0 || writes > len(events) || len(events) > 1000 {
		t.Errorf("1000 writes to one name gave %d events of %d", writes, len(events))
	}
	if events[0].Name != "download.part" || events[0].Mask&unix.IN_CREATE == 0 {
		t.Errorf("first event %+v, want the create of download.part", events[0])
	}
}

func TestNamedWatchNamesDeletesAndRenames(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a"), "x")
	w := newNamed(t, root)
	if err := os.Rename(filepath.Join(root, "a"), filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	events, rescan := takeUntil(t, w, func(e []Event, _ bool) bool {
		return len(e) >= 2 && e[len(e)-1].Mask&unix.IN_DELETE != 0
	})
	if rescan || events[0].Name != "a" || events[0].Mask&unix.IN_MOVED_FROM == 0 || events[1].Name != "b" {
		t.Errorf("rename and delete gave %+v (rescan %v)", events, rescan)
	}
}

func TestNamedWatchRescansWhenNamesAreLost(t *testing.T) {
	root := t.TempDir()
	w := newNamed(t, root)
	overflow := make([]byte, unix.SizeofInotifyEvent)
	binary.NativeEndian.PutUint32(overflow[0:], ^uint32(0))
	binary.NativeEndian.PutUint32(overflow[4:], unix.IN_Q_OVERFLOW)
	w.note("before", unix.IN_CREATE)
	if !w.handle(overflow) {
		t.Fatal("an overflow did not count as a change")
	}
	w.note("after", unix.IN_CREATE)
	if b := w.Take(); !b.Rescan || len(b.Events) != 0 {
		t.Errorf("after an overflow Take = %+v, want a rescan alone", b)
	}
	if b := w.Take(); b.Rescan || len(b.Events) != 0 {
		t.Errorf("a second Take = %+v, want nothing", b)
	}
	for i := range maxPending + 1 {
		w.note(strconv.Itoa(i), unix.IN_CREATE)
	}
	if b := w.Take(); !b.Rescan {
		t.Errorf("%d names gave %d events and no rescan", maxPending+1, len(b.Events))
	}
	for range maxPending + 1 {
		w.note("same", unix.IN_MODIFY)
	}
	if b := w.Take(); b.Rescan || len(b.Events) != 1 {
		t.Errorf("one name noted %d times gave %+v", maxPending+1, b)
	}
}

func TestNamedWatchRescansWhenTheDirectoryGoes(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "shown")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	w := newNamed(t, root)
	if err := os.Rename(root, filepath.Join(parent, "moved")); err != nil {
		t.Fatal(err)
	}
	takeUntil(t, w, func(_ []Event, rescan bool) bool { return rescan })
}

func TestTickWatcherKeepsNoNames(t *testing.T) {
	root := t.TempDir()
	w := newWatcher(t, Changes, false, root)
	write(t, filepath.Join(root, "x"), "1")
	expectTick(t, w, "create")
	if b := w.Take(); b.Rescan || len(b.Events) != 0 {
		t.Errorf("a tick watcher kept %+v", b)
	}
}
