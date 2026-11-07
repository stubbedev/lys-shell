package clipboard

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func item(mime, data string) storeItem {
	return storeItem{rec: Record{Digest: DigestOf([]byte(data)), Mime: mime, Size: len(data)}, data: []byte(data)}
}

func mustStore(t *testing.T, dir string) *Store {
	t.Helper()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func readRecord(t *testing.T, s *Store, r Record) string {
	t.Helper()
	var b bytes.Buffer
	if err := s.Read(r, &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAStoreKeepsItsRecordsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	s := mustStore(t, dir)
	png := item("image/png", "\x89PNG bytes")
	text := item(`text/plain; charset="utf-8"`, "hello")
	if err := s.Sync([]storeItem{png, text}); err != nil {
		t.Fatal(err)
	}
	again := mustStore(t, dir)
	got := again.Records()
	if !slices.Equal(got, []Record{png.rec, text.rec}) {
		t.Fatalf("records = %+v", got)
	}
	if b := readRecord(t, again, got[0]); b != "\x89PNG bytes" {
		t.Errorf("payload = %q", b)
	}
	for _, name := range dirNames(t, dir) {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %o, want 0600", name, perm)
		}
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("store directory mode: %v, %v", info.Mode(), err)
	}
}

func TestASyncRemovesOnlyWhatNoRecordNames(t *testing.T) {
	dir := t.TempDir()
	s := mustStore(t, dir)
	a, b := item(textMime, "a"), item(textMime, "b")
	same := item("text/plain", "a")
	if err := s.Sync([]storeItem{a, b, same}); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync([]storeItem{{rec: same.rec}}); err != nil {
		t.Fatalf("an item without data that the store holds: %v", err)
	}
	if got := dirNames(t, dir); !slices.Equal(got, []string{a.rec.Digest.String(), storeIndex}) && !slices.Equal(got, []string{storeIndex, a.rec.Digest.String()}) {
		t.Fatalf("files = %v, want the shared payload and the index", got)
	}
	if err := s.Sync([]storeItem{{rec: b.rec}}); err == nil {
		t.Error("an item the store lacks, without data, was accepted")
	}
	if got := s.Records(); !slices.Equal(got, []Record{same.rec}) {
		t.Errorf("a refused sync changed the records: %+v", got)
	}
	if err := s.Sync(nil); err != nil {
		t.Fatal(err)
	}
	if got := dirNames(t, dir); !slices.Equal(got, []string{storeIndex}) {
		t.Errorf("an empty store keeps %v", got)
	}
}

func TestADamagedPayloadIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	s := mustStore(t, dir)
	a := item(textMime, "original")
	if err := s.Sync([]storeItem{a}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, a.rec.Digest.String()), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(a.rec, &bytes.Buffer{}); !errors.Is(err, ErrCorrupt) {
		t.Errorf("tampered payload: %v, want ErrCorrupt", err)
	}
	if err := os.Remove(filepath.Join(dir, a.rec.Digest.String())); err != nil {
		t.Fatal(err)
	}
	if err := s.Read(a.rec, &bytes.Buffer{}); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing payload: %v", err)
	}
}

func TestABadIndexIsRefusedNamingTheLine(t *testing.T) {
	dir := t.TempDir()
	good := item(textMime, "x").rec
	var b bytes.Buffer
	if err := writeIndex(&b, []Record{good}); err != nil {
		t.Fatal(err)
	}
	b.WriteString("nonsense\n")
	if err := os.WriteFile(filepath.Join(dir, storeIndex), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(dir); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("open: %v, want the bad line named", err)
	}
	for _, line := range []string{"zz\t1\t\"a\"", good.Digest.String() + "\t0\t\"a\"", good.Digest.String() + "\t1\t\"\"", good.Digest.String() + "ab\t1\t\"a\""} {
		if _, err := parseRecord(line); err == nil {
			t.Errorf("%q parsed", line)
		}
	}
}

func TestOpeningAStoreRemovesWhatAnInterruptedWriteLeft(t *testing.T) {
	dir := t.TempDir()
	s := mustStore(t, dir)
	a := item(textMime, "kept")
	if err := s.Sync([]storeItem{a}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".index.tmp123", item(textMime, "orphan").rec.Digest.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	again := mustStore(t, dir)
	if got := again.Records(); !slices.Equal(got, []Record{a.rec}) {
		t.Fatalf("records = %+v", got)
	}
	if got := dirNames(t, dir); len(got) != 2 {
		t.Errorf("files = %v, want the payload and the index", got)
	}
}

const interruptEnv = "LYS_CLIPBOARD_INTERRUPT_STORE"

// TestHelperInterruptedSync runs in a child whose files may not grow
// past a few bytes, so the kernel cuts the sync's payload write short.
func TestHelperInterruptedSync(t *testing.T) {
	dir := os.Getenv(interruptEnv)
	if dir == "" {
		t.Skip("run by TestAnInterruptedWriteLeavesTheStoreAsItWas")
	}
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 4096, Max: 4096}); err != nil {
		t.Fatal(err)
	}
	s := mustStore(t, dir)
	err := s.Sync(append(slicesOf(s.Records()), item("image/png", strings.Repeat("x", 64<<10))))
	if !errors.Is(err, syscall.EFBIG) {
		t.Fatalf("the cut-short write: %v, want EFBIG", err)
	}
}

func slicesOf(records []Record) []storeItem {
	items := make([]storeItem, len(records))
	for i, r := range records {
		items[i] = storeItem{rec: r}
	}
	return items
}

func TestAnInterruptedWriteLeavesTheStoreAsItWas(t *testing.T) {
	dir := t.TempDir()
	s := mustStore(t, dir)
	a := item(textMime, "before")
	if err := s.Sync([]storeItem{a}); err != nil {
		t.Fatal(err)
	}
	before := dirNames(t, dir)
	index, err := os.ReadFile(filepath.Join(dir, storeIndex))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperInterruptedSync$", "-test.v")
	cmd.Env = append(os.Environ(), interruptEnv+"="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestHelperInterruptedSync") {
		t.Fatalf("the interrupted child: %v\n%s", err, out)
	}
	if got := dirNames(t, dir); !slices.Equal(got, before) {
		t.Errorf("files = %v, want %v as before the write", got, before)
	}
	after, err := os.ReadFile(filepath.Join(dir, storeIndex))
	if err != nil || !bytes.Equal(after, index) {
		t.Errorf("the index changed: %q, %v", after, err)
	}
	if got := mustStore(t, dir).Records(); !slices.Equal(got, []Record{a.rec}) {
		t.Errorf("records = %+v", got)
	}
}
