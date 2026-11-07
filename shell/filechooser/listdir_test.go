package filechooser

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/fswatch"
)

// bigDirEntries is the folder size the listing is gated on.
const bigDirEntries = 100_000

// bigDir is a folder of bigDirEntries empty files with mixed-case
// names, a tenth of them folders.
func bigDir(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	for i := range bigDirEntries {
		path := filepath.Join(root, fmt.Sprintf("File-%06d.txt", i))
		if i%10 == 0 {
			if err := unix.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		fd, err := unix.Open(path, unix.O_CREAT|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		_ = unix.Close(fd)
	}
	return root
}

// Listing 100,000 entries costs a constant number of allocations plus
// the path chunks, one stat per entry relative to the directory fd
// (none walks a path), and getdents64 calls a large buffer bounds;
// partitioning and sorting the cached listing per keystroke allocate
// nothing.
func TestListingABigFolderIsCounted(t *testing.T) {
	root := bigDir(t)
	entries, counts, err := testReader.List(root, ModeOpenFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != bigDirEntries || counts.stats != bigDirEntries {
		t.Errorf("%d entries from %d stats, want %d of each", len(entries), counts.stats, bigDirEntries)
	}
	if counts.getdents > 40 {
		t.Errorf("%d getdents64 calls for %d entries, want at most 40", counts.getdents, bigDirEntries)
	}
	if !slices.IsSortedFunc(entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) }) {
		t.Error("the listing is not sorted by path")
	}
	dirs := 0
	for _, e := range entries {
		if e.IsDir {
			dirs++
		}
	}
	if dirs != bigDirEntries/10 {
		t.Errorf("%d folders listed, want %d", dirs, bigDirEntries/10)
	}
	allocs := testing.AllocsPerRun(3, func() {
		if _, _, err := testReader.List(root, ModeOpenFile); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 2000 {
		t.Errorf("listing %d entries made %.0f allocations, want at most 2000", bigDirEntries, allocs)
	}
	folders, _, _ := testReader.List(root, ModeFolder)
	if len(folders) != bigDirEntries/10 {
		t.Errorf("a folder pick listed %d, want the %d folders", len(folders), bigDirEntries/10)
	}

	s := newSession(testReader, ModeOpenFile, root, nil, testDB(t), "/nonexistent-home", defaultView)
	s.refresh()
	queries := []string{"f", "fi", "fil", "file-0", "file-00", "file-000", "FILE-0001"}
	i := 0
	for _, sort := range []SortColumn{SortName, SortSize, SortModified, SortKind} {
		s.view.sort = sort
		perKey := testing.AllocsPerRun(len(queries), func() {
			s.setSearch(queries[i%len(queries)])
			i++
		})
		if perKey != 0 {
			t.Errorf("a keystroke sorted by %s made %.0f allocations, want none", sortNames[sort], perKey)
		}
	}
	if s.setSearch("file-00012"); len(s.entries) != 10 {
		t.Errorf("searching file-00012 found %d, want 10", len(s.entries))
	}
}

// BenchmarkListDir lists the gated folder.
func BenchmarkListDir(b *testing.B) {
	root := bigDir(b)
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := testReader.List(root, ModeOpenFile); err != nil {
			b.Fatal(err)
		}
	}
}

// followCount is what followDir hands over for a watched folder.
type followCount struct {
	updates chan dirUpdate
}

// follow runs followDir over a named watch of dir for the test.
func follow(t *testing.T, dir string) followCount {
	t.Helper()
	w, err := fswatch.NewNamed(fswatch.Changes)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Add(dir); err != nil {
		t.Fatal(err)
	}
	f := followCount{updates: make(chan dirUpdate, 1024)}
	done := make(chan struct{})
	go func() {
		followDir(w, dir, ModeOpenFile, func(u dirUpdate) { f.updates <- u })
		close(done)
	}()
	t.Cleanup(func() {
		_ = w.Close()
		<-done
	})
	return f
}

// until takes updates until done holds for the changes taken, counting
// rescans; past the liveness bound it fails with what came.
func (f followCount) until(t *testing.T, done func([]dirChange) bool) ([]dirChange, int) {
	t.Helper()
	var changes []dirChange
	rescans := 0
	deadline := time.After(10 * time.Second)
	for !done(changes) {
		select {
		case u := <-f.updates:
			changes = append(changes, u.changes...)
			if u.rescan {
				rescans++
			}
		case <-deadline:
			t.Fatalf("after %d changes and %d rescans: never done", len(changes), rescans)
		}
	}
	return changes, rescans
}

// A file written 1,000 times while its folder is shown is stated at
// most once a write and never reads the folder again; a file created
// and removed changes its one row.
func TestChangesAreAppliedRowByRow(t *testing.T) {
	root := tree(t)
	s := open(t, ModeOpenFile, root, nil)
	f := follow(t, root)
	path := filepath.Join(root, "download.part")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 1000 {
		if _, err := file.WriteString("x"); err != nil {
			t.Fatal(err)
		}
	}
	_ = file.Close()
	changes, rescans := f.until(t, func(cs []dirChange) bool {
		return len(cs) > 0 && cs[len(cs)-1].entry.Size == 1000
	})
	if rescans != 0 || len(changes) > 1000 {
		t.Errorf("1000 writes made %d rescans and %d stats", rescans, len(changes))
	}
	before := slices.Clone(s.all)
	if !s.applyChanges(root, changes) {
		t.Fatal("the changes did not apply to the folder listed")
	}
	added := slices.IndexFunc(s.all, func(e Entry) bool { return e.Path == path })
	if len(s.all) != len(before)+1 || added < 0 || s.all[added].Size != 1000 {
		t.Fatalf("after the writes the listing is %v", names(s.all))
	}
	if !slices.Equal(slices.Delete(slices.Clone(s.all), added, added+1), before) {
		t.Error("rows other than the new file changed")
	}
	if !slices.Contains(names(s.entries), "download.part") {
		t.Errorf("the new file is not shown: %v", names(s.entries))
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	gone, _ := f.until(t, func(cs []dirChange) bool { return len(cs) > 0 && !cs[len(cs)-1].present })
	s.applyChanges(root, gone)
	if !slices.Equal(s.all, before) {
		t.Errorf("after the removal the listing is %v, want %v", names(s.all), names(before))
	}
	if s.applyChanges(filepath.Join(root, "docs"), gone) {
		t.Error("changes to a folder not listed applied")
	}
}

// Changes coming while the listing is read apply once it lands, and
// the .hidden list follows its file.
func TestChangesWaitForTheListing(t *testing.T) {
	root := tree(t)
	s := open(t, ModeOpenFile, root, nil)
	s.request = func(string) {}
	s.reload()
	touch(t, root, "late.txt")
	if s.applyChanges(root, statChanges(root, []string{"late.txt"}, s.mode)) {
		t.Error("a change applied while the listing was read")
	}
	if !s.setListing(root, listing{entries: slices.Clone(s.all), hidden: s.hiddenNames}) {
		t.Fatal("the listing did not land")
	}
	if !slices.Contains(names(s.entries), "late.txt") {
		t.Errorf("the waiting change was lost: %v", names(s.entries))
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("late.txt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.applyChanges(root, statChanges(root, []string{".hidden"}, s.mode))
	if slices.Contains(names(s.entries), "late.txt") {
		t.Errorf("a name .hidden lists is shown: %v", names(s.entries))
	}
}

// fakeSource hands over the batches it is given, a tick each.
type fakeSource struct {
	ticks   chan struct{}
	batches chan fswatch.Batch
}

func (f fakeSource) Changed() <-chan struct{} { return f.ticks }
func (f fakeSource) Take() fswatch.Batch      { return <-f.batches }

// Lost changes read the folder again exactly once; an empty batch
// does nothing.
func TestLostChangesRescanOnce(t *testing.T) {
	src := fakeSource{ticks: make(chan struct{}, 3), batches: make(chan fswatch.Batch, 3)}
	src.batches <- fswatch.Batch{Rescan: true}
	src.batches <- fswatch.Batch{}
	src.batches <- fswatch.Batch{Events: []fswatch.Event{{Name: "a.png", Mask: unix.IN_MODIFY}}}
	for range 3 {
		src.ticks <- struct{}{}
	}
	close(src.ticks)
	var updates []dirUpdate
	followDir(src, tree(t), ModeOpenFile, func(u dirUpdate) { updates = append(updates, u) })
	if len(updates) != 2 || !updates[0].rescan || updates[1].rescan || len(updates[1].changes) != 1 {
		t.Errorf("updates %+v, want one rescan then one change", updates)
	}
}
