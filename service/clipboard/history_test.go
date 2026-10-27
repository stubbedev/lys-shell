package clipboard

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stubbedev/lys-ui/transfer"
)

const textMime = "text/plain;charset=utf-8"

func entry(mime, data string) content { return content{mime: mime, data: []byte(data)} }

// mustHistory is a history that, once the test ends, is closed and
// must have freed every entry.
func mustHistory(t *testing.T, capacity, maxBytes, budget int) *History {
	t.Helper()
	h, err := NewHistory(capacity, maxBytes, budget)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Close()
		if st := h.mem.Stats(); st.Live != 0 || st.Bytes != 0 {
			t.Errorf("closed history left %d allocations of %d bytes", st.Live, st.Bytes)
		}
	})
	return h
}

func defaultHistory(t *testing.T) *History {
	t.Helper()
	return mustHistory(t, DefaultCapacity, MaxEntryBytes, DefaultBudget().Bytes())
}

// push remembers data, failing the test on an error.
func push(t *testing.T, h *History, mime string, data []byte) (uint64, bool) {
	t.Helper()
	id, ok, err := h.Push(mime, data)
	if err != nil {
		t.Fatalf("push %d bytes of %s: %v", len(data), mime, err)
	}
	return id, ok
}

func texts(h *History) []string {
	snap := h.Entries()
	defer snap.Release()
	var out []string
	for _, e := range snap.Entries() {
		if s, ok := e.content().text(); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestNewestSelectionComesFirst(t *testing.T) {
	h := defaultHistory(t)
	if _, ok := push(t, h, textMime, []byte("one")); !ok {
		t.Fatal("refused")
	}
	push(t, h, textMime, []byte("two"))

	if got := texts(h); !slices.Equal(got, []string{"two", "one"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestRecopyingMovesTheEntryWithoutDuplicatingIt(t *testing.T) {
	h := defaultHistory(t)
	first, _ := push(t, h, textMime, []byte("one"))
	push(t, h, textMime, []byte("two"))
	push(t, h, textMime, []byte("three"))

	again, ok := push(t, h, textMime, []byte("one"))

	if !ok || again != first {
		t.Fatalf("recopy id = %d, want %d", again, first)
	}
	if got := texts(h); !slices.Equal(got, []string{"one", "three", "two"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestTheSameBytesUnderADifferentMimeIsADifferentEntry(t *testing.T) {
	h := defaultHistory(t)
	push(t, h, textMime, []byte("data"))
	push(t, h, "image/png", []byte("data"))

	if h.Len() != 2 {
		t.Fatalf("len = %d, want 2", h.Len())
	}
}

func TestTheOldestEntryFallsOffTheEnd(t *testing.T) {
	h := mustHistory(t, 2, MaxEntryBytes, MaxEntryBytes)
	push(t, h, textMime, []byte("one"))
	push(t, h, textMime, []byte("two"))
	push(t, h, textMime, []byte("three"))

	if got := texts(h); !slices.Equal(got, []string{"three", "two"}) {
		t.Fatalf("entries = %v", got)
	}
}

func TestSelectionsNotWorthRememberingAreRefused(t *testing.T) {
	h := mustHistory(t, 4, 8, 16)

	for _, data := range []string{"", "   \n\t ", "123456789"} {
		if _, ok := push(t, h, textMime, []byte(data)); ok {
			t.Errorf("%q was remembered", data)
		}
	}
	if h.Len() != 0 {
		t.Fatal("a refused selection was stored")
	}
	// Exactly at the cap is fine, and whitespace-looking binary is
	// content rather than a stray drag.
	if _, ok := push(t, h, textMime, []byte("12345678")); !ok {
		t.Error("a selection exactly at the cap was refused")
	}
	if _, ok := push(t, h, "image/png", []byte("  \n  ")); !ok {
		t.Error("whitespace-looking binary was refused")
	}
	if h.Len() != 2 {
		t.Fatalf("len = %d, want 2", h.Len())
	}
}

func TestAZeroCapacityHistoryRemembersNothing(t *testing.T) {
	h := mustHistory(t, 0, MaxEntryBytes, MaxEntryBytes)

	if _, ok := push(t, h, textMime, []byte("one")); ok || h.Len() != 0 {
		t.Fatal("a zero-capacity history remembered something")
	}
}

func TestNegativeBoundsAreAnError(t *testing.T) {
	if _, err := NewHistory(-1, 8, 8); err == nil {
		t.Error("negative capacity accepted")
	}
	if _, err := NewHistory(8, -1, 8); err == nil {
		t.Error("negative entry size accepted")
	}
}

func TestAnAgedOutOrRemovedIDResolvesToNothing(t *testing.T) {
	h := mustHistory(t, 1, MaxEntryBytes, MaxEntryBytes)
	old, _ := push(t, h, textMime, []byte("one"))
	current, _ := push(t, h, textMime, []byte("two"))

	if _, _, ok := h.hold(old); ok {
		t.Error("an aged-out id resolved")
	}
	if _, _, ok := h.hold(^uint64(0)); ok {
		t.Error("an unknown id resolved")
	}
	mime, p, ok := h.hold(current)
	if !ok || mime != textMime || string(p.bytes()) != "two" {
		t.Errorf("current id = %q, %v", mime, ok)
	}
	if ok {
		p.release()
	}
	if !h.Remove(current) || h.Remove(current) {
		t.Error("Remove did not report presence exactly once")
	}
	if h.Len() != 0 {
		t.Fatal("history not empty")
	}
}

func TestClearForgetsEverything(t *testing.T) {
	h := defaultHistory(t)
	push(t, h, textMime, []byte("one"))
	push(t, h, textMime, []byte("two"))

	h.Clear()

	if h.Len() != 0 || len(texts(h)) != 0 {
		t.Fatal("Clear left entries")
	}
	// Ids keep counting: a row drawn before the clear cannot name a
	// selection made after it.
	id, _ := push(t, h, textMime, []byte("three"))
	if id != 3 {
		t.Fatalf("id after clear = %d, want 3", id)
	}
}

func TestEntriesIsASnapshot(t *testing.T) {
	h := defaultHistory(t)
	push(t, h, textMime, []byte("one"))
	snap := h.Entries()
	defer snap.Release()

	push(t, h, textMime, []byte("two"))

	if len(snap.Entries()) != 1 {
		t.Fatal("a snapshot changed under a later push")
	}
}

func TestAPasswordManagerHintKeepsTheSecretOut(t *testing.T) {
	if !IsSensitive([]string{textMime, "x-kde-passwordManagerHint"}) {
		t.Error("the KDE hint was not honoured")
	}
	// The ordinary case must not be swept up with it.
	if IsSensitive([]string{textMime, "text/html"}) || IsSensitive(nil) {
		t.Error("an ordinary selection read as sensitive")
	}
}

func best(t *testing.T, mimes ...string) string {
	t.Helper()
	m, ok := BestMime(mimes)
	if !ok {
		t.Fatalf("BestMime(%v) found nothing", mimes)
	}
	return m
}

func TestFilesBeatTheFilenameTextOfferedAlongsideThem(t *testing.T) {
	// What a file manager actually offers when you copy one file.
	if got := best(t, textMime, "text/plain", URIListMime, "text/html"); got != URIListMime {
		t.Fatalf("best = %q", got)
	}
}

func TestAnImageBeatsTextButFilesBeatAnImage(t *testing.T) {
	if got := best(t, "text/plain", "image/png"); got != "image/png" {
		t.Errorf("best = %q", got)
	}
	if got := best(t, "image/png", URIListMime); got != URIListMime {
		t.Errorf("best = %q", got)
	}
}

func TestUTF8TextIsPreferredOverTheOlderFlavours(t *testing.T) {
	if got := best(t, "STRING", "TEXT", textMime); got != textMime {
		t.Errorf("best = %q", got)
	}
	if got := best(t, "STRING", "TEXT"); got != "STRING" {
		t.Errorf("best = %q", got)
	}
	if got := best(t, "application/x-thing", "text/html"); got != "text/html" {
		t.Errorf("an unlisted text/ mime lost to a non-text one: %q", got)
	}
}

func TestAnUnknownSelectionIsStillKeptAndNothingOfferedIsNone(t *testing.T) {
	if got := best(t, "application/x-thing"); got != "application/x-thing" {
		t.Errorf("best = %q", got)
	}
	if _, ok := BestMime(nil); ok {
		t.Error("nothing offered still picked a mime")
	}
}

func TestKindsComeFromTheMime(t *testing.T) {
	cases := map[Kind]content{
		KindText:  entry(textMime, "hi"),
		KindFiles: entry(URIListMime, "file:///a"),
		KindImage: entry("image/png", "\x89PNG"),
		KindOther: entry("application/pdf", "%PDF"),
	}
	for want, e := range cases {
		if got := kindOf(e.mime); got != want {
			t.Errorf("%s: kind = %v, want %v", e.mime, got, want)
		}
	}
	if got := kindOf("text/html"); got != KindText {
		t.Errorf("text/html kind = %v", got)
	}
}

func TestTextOnlyForTextAndValidUTF8(t *testing.T) {
	if s, ok := entry(textMime, "hi").text(); !ok || s != "hi" {
		t.Errorf("text = %q, %v", s, ok)
	}
	if _, ok := entry("image/png", "hi").text(); ok {
		t.Error("an image read as text")
	}
	if _, ok := entry(textMime, "\xff\xfe").text(); ok {
		t.Error("invalid UTF-8 read as text")
	}
}

func TestURIListYieldsDecodedPaths(t *testing.T) {
	list := entry(URIListMime, "# a comment\r\nfile:///home/me/my%20photo.png\r\nfile:///tmp/notes.txt\r\n")

	if got := list.paths(); !slices.Equal(got, []string{"/home/me/my photo.png", "/tmp/notes.txt"}) {
		t.Fatalf("paths = %v", got)
	}
}

func TestANonFileURIAndANonListEntryYieldNoPaths(t *testing.T) {
	// Only file:// URIs name something on disk.
	if got := entry(URIListMime, "https://example.com/a\n").paths(); len(got) != 0 {
		t.Errorf("paths = %v", got)
	}
	// Text that happens to look like one is still text.
	if got := entry(textMime, "file:///tmp/a").paths(); len(got) != 0 {
		t.Errorf("paths = %v", got)
	}
}

func TestAStrayPercentInANameSurvivesDecoding(t *testing.T) {
	list := entry(URIListMime, "file:///tmp/100%25.txt\nfile:///tmp/50%off.txt\nfile:///tmp/end%\nfile:///tmp/end%a\n")

	want := []string{"/tmp/100%.txt", "/tmp/50%off.txt", "/tmp/end%", "/tmp/end%a"}
	if got := list.paths(); !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
}

func TestTextPreviewIsOneLine(t *testing.T) {
	text := entry(textMime, "  fn main() {\n    println!(\"hi\");\n}  ")

	if got := text.preview(80); got != `fn main() { println!("hi"); }` {
		t.Fatalf("preview = %q", got)
	}
}

func TestPreviewIsCutOnACharacterBoundary(t *testing.T) {
	text := entry(textMime, strings.Repeat("é", 40))

	got := text.preview(10)

	if utf8.RuneCountInString(got) != 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview = %q", got)
	}
	if got := entry(textMime, "short").preview(10); got != "short" {
		t.Fatalf("a short preview was cut: %q", got)
	}
}

func TestFileAndImagePreviewsSayWhatTheyAre(t *testing.T) {
	cases := map[string]content{
		"notes.txt":                  entry(URIListMime, "file:///tmp/notes.txt\n"),
		"a.txt, b.txt (2 files)":     entry(URIListMime, "file:///tmp/a.txt\nfile:///tmp/b.txt\n"),
		"No files":                   entry(URIListMime, "https://example.com\n"),
		"Image · image/png · 2.0 KB": {mime: "image/png", data: make([]byte, 2048)},
		"application/pdf · 4 B":      entry("application/pdf", "%PDF"),
		"Image · image/png · 1.5 MB": {mime: "image/png", data: make([]byte, 3<<19)},
	}
	for want, e := range cases {
		if got := e.preview(80); got != want {
			t.Errorf("preview = %q, want %q", got, want)
		}
	}
}

func TestTextIsOfferedUnderEveryTextFlavourOnce(t *testing.T) {
	mimes := offerMimes(textMime)

	if mimes[0] != textMime || !slices.Contains(mimes, "STRING") {
		t.Fatalf("mimes = %v", mimes)
	}
	// The entry's own mime is in TextMimes too, and must not be
	// offered twice — a duplicate offer is a protocol error for some
	// compositors.
	if len(mimes) != len(TextMimes) {
		t.Fatalf("mimes = %v, want each text flavour once", mimes)
	}
}

func TestNonTextIsOfferedOnlyAsItself(t *testing.T) {
	if got := offerMimes("image/png"); !slices.Equal(got, []string{"image/png"}) {
		t.Errorf("png offered as %v", got)
	}
	if got := offerMimes(URIListMime); !slices.Equal(got, []string{URIListMime}) {
		t.Errorf("uri-list offered as %v", got)
	}
}

func TestATextMimeOutsideTheKnownListStillLeadsItsOwnOffer(t *testing.T) {
	mimes := offerMimes("text/html")

	if mimes[0] != "text/html" || len(mimes) != len(TextMimes)+1 {
		t.Fatalf("mimes = %v", mimes)
	}
}

// A secret is offered as text under every text mime and carries the
// password manager hint, so the history refuses it; plain text does
// not.
func TestSecretContentIsTextTheHistoryRefuses(t *testing.T) {
	c := SecretContent("hunter2")
	if !IsSensitive(c.Mimes) {
		t.Errorf("secret mimes %v are not sensitive", c.Mimes)
	}
	for _, m := range []string{textMime, "text/plain", "UTF8_STRING"} {
		if b, err := c.Bytes(m); err != nil || string(b) != "hunter2" {
			t.Errorf("%s = %q, %v", m, b, err)
		}
	}
	if b, _ := c.Bytes("x-kde-passwordManagerHint"); string(b) != "secret" {
		t.Errorf("hint = %q", b)
	}
	if IsSensitive(transfer.Text("hunter2").Mimes) {
		t.Error("plain text counts as sensitive")
	}
}

// live is how many entries' bytes are allocated, held by anyone.
func live(h *History) int { return h.mem.Stats().Live }

func mustPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()
	fn()
}

func TestTheHistoryStaysWithinItsByteBudgetOldestFirst(t *testing.T) {
	h := mustHistory(t, DefaultCapacity, 4, 10)
	for _, s := range []string{"aaaa", "bbbb", "cc"} {
		push(t, h, textMime, []byte(s))
	}
	if got := texts(h); !slices.Equal(got, []string{"cc", "bbbb", "aaaa"}) || h.size != 10 {
		t.Fatalf("entries = %v (%d bytes), want all three at exactly the budget", got, h.size)
	}

	push(t, h, textMime, []byte("d"))
	if got := texts(h); !slices.Equal(got, []string{"d", "cc", "bbbb"}) || h.size != 7 {
		t.Fatalf("entries = %v (%d bytes), want the oldest gone", got, h.size)
	}

	// A recopy is the newest again, so it outlives older entries.
	push(t, h, textMime, []byte("bbbb"))
	push(t, h, textMime, []byte("eeee"))
	if got := texts(h); !slices.Equal(got, []string{"eeee", "bbbb", "d"}) || h.size != 9 {
		t.Fatalf("entries = %v (%d bytes), want the least recently copied gone", got, h.size)
	}

	// The largest entry evicts everything else, but never itself.
	h2 := mustHistory(t, DefaultCapacity, 4, 4)
	push(t, h2, textMime, []byte("ab"))
	if _, ok := push(t, h2, textMime, []byte("wxyz")); !ok {
		t.Fatal("an entry exactly the budget was refused")
	}
	if got := texts(h2); !slices.Equal(got, []string{"wxyz"}) {
		t.Fatalf("entries = %v, want only the largest", got)
	}
}

func TestEveryDroppedEntryIsFreed(t *testing.T) {
	h := mustHistory(t, 3, 4, 8)
	for _, s := range []string{"a", "b", "c"} {
		push(t, h, textMime, []byte(s))
	}
	if n := live(h); n != 3 {
		t.Fatalf("%d allocations for 3 entries", n)
	}

	push(t, h, textMime, []byte("d"))
	if n := live(h); n != 3 {
		t.Errorf("evicted by count: %d allocations, want 3", n)
	}
	push(t, h, textMime, []byte("eeee"))
	push(t, h, textMime, []byte("ffff"))
	if n := live(h); n != 2 || h.Len() != 2 {
		t.Errorf("evicted by budget: %d allocations for %d entries, want 2", n, h.Len())
	}
	if err := h.SetBudget(4); err != nil {
		t.Fatal(err)
	}
	if n := live(h); n != 1 || h.Len() != 1 {
		t.Errorf("evicted by a smaller budget: %d allocations for %d entries, want 1", n, h.Len())
	}
	if err := h.SetBudget(8); err != nil {
		t.Fatal(err)
	}
	id, _ := push(t, h, textMime, []byte("g"))
	if !h.Remove(id) || live(h) != 1 {
		t.Errorf("removed: %d allocations, want 1", live(h))
	}
	push(t, h, textMime, []byte("i"))
	h.Clear()
	if n := live(h); n != 0 {
		t.Errorf("cleared: %d allocations, want none", n)
	}
	// A refused or recopied selection allocates nothing.
	push(t, h, textMime, []byte("toolong"))
	push(t, h, textMime, []byte("h"))
	push(t, h, textMime, []byte("h"))
	if n := live(h); n != 1 {
		t.Errorf("%d allocations for one entry", n)
	}
}

func TestAListedEntryOutlivesItsEviction(t *testing.T) {
	h := mustHistory(t, 1, MaxEntryBytes, MaxEntryBytes)
	push(t, h, URIListMime, []byte("file:///tmp/a.png\n"))
	snap := h.Entries()

	push(t, h, textMime, []byte("newer"))
	h.Clear()

	if n := live(h); n != 1 {
		t.Fatalf("%d allocations while the snapshot holds the evicted entry, want 1", n)
	}
	e := snap.Entries()[0]
	if got := e.Paths(); !slices.Equal(got, []string{"/tmp/a.png"}) || e.Preview(80) != "a.png" {
		t.Fatalf("held entry reads %v / %q", got, e.Preview(80))
	}
	snap.Release()
	if n := live(h); n != 0 {
		t.Fatalf("%d allocations after the last holder let go", n)
	}
}

func TestAReleasedSnapshotCannotBeRead(t *testing.T) {
	h := defaultHistory(t)
	push(t, h, textMime, []byte("kept"))
	snap := h.Entries()
	e := snap.Entries()[0]
	snap.Release()

	// Still in the history, so the bytes are live: the snapshot alone
	// decides whether its entries may be read.
	mustPanic(t, "reading a released entry", func() { e.Preview(10) })
	mustPanic(t, "releasing twice", snap.Release)
	if live(h) != 1 {
		t.Fatal("the history's own entry was freed with the snapshot")
	}
}

func TestBudgetsSmallerThanAnEntryAreRefused(t *testing.T) {
	if _, err := NewHistory(8, 16, 15); err == nil {
		t.Error("a budget below the largest entry was accepted")
	}
	if _, err := NewHistory(8, 16, 16); err != nil {
		t.Errorf("a budget of exactly the largest entry was refused: %v", err)
	}
	h := mustHistory(t, 8, 4, 8)
	push(t, h, textMime, []byte("abc"))
	if err := h.SetBudget(3); err == nil {
		t.Error("SetBudget accepted a budget below the largest entry")
	}
	if h.budget != 8 || h.Len() != 1 {
		t.Errorf("a refused budget still applied: budget %d, %d entries", h.budget, h.Len())
	}
}

func TestBudgetRange(t *testing.T) {
	for _, mib := range []int64{MinBudgetMiB, DefaultBudgetMiB, MaxBudgetMiB} {
		b, err := NewBudget(mib)
		if err != nil || b.MiB() != mib || b.Bytes() != int(mib)*MiB {
			t.Errorf("%d MiB: %+v, %v", mib, b, err)
		}
	}
	if b := DefaultBudget(); b.MiB() != DefaultBudgetMiB {
		t.Errorf("default budget = %d MiB", b.MiB())
	}
	for _, mib := range []int64{MinBudgetMiB - 1, MaxBudgetMiB + 1, 0, -1} {
		if _, err := NewBudget(mib); !errors.Is(err, ErrBudgetRange) {
			t.Errorf("%d MiB: err = %v, want ErrBudgetRange", mib, err)
		}
	}
	if MinBudgetMiB*MiB != MaxEntryBytes {
		t.Errorf("the least budget, %d MiB, is not the largest entry, %d bytes", MinBudgetMiB, MaxEntryBytes)
	}
	if _, err := DefaultHistory(Budget{}); err == nil {
		t.Error("a zero budget built a history")
	}
}

func TestCloseFreesEverythingAndRefusesPushes(t *testing.T) {
	h := defaultHistory(t)
	push(t, h, textMime, []byte("one"))
	push(t, h, "image/png", []byte("\x89PNG"))

	h.Close()

	if n := live(h); n != 0 {
		t.Fatalf("%d allocations after Close", n)
	}
	if _, ok, err := h.Push(textMime, []byte("late")); err == nil || ok || live(h) != 0 {
		t.Fatalf("a push after Close: %v, %v, %d allocations", ok, err, live(h))
	}
}
