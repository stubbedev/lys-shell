package clipboard

import (
	"errors"
	"slices"
	"testing"

	"github.com/stubbedev/lys-shell/service/clipboard/historybudget"
)

// pinnedTexts are the history's texts, pinned ones marked with a star.
func pinnedTexts(h *History) []string {
	snap := h.Entries()
	defer snap.Release()
	var out []string
	for _, e := range snap.Entries() {
		s, _ := e.content().text()
		if e.Pinned {
			s = "*" + s
		}
		out = append(out, s)
	}
	return out
}

func mustPin(t *testing.T, h *History, id uint64) {
	t.Helper()
	if err := h.Pin(id); err != nil {
		t.Fatalf("pin %d: %v", id, err)
	}
}

func TestPinnedEntriesComeFirstAndOutliveTheCapacity(t *testing.T) {
	h := mustHistory(t, 2, historybudget.MaxEntryBytes, historybudget.MaxEntryBytes)
	a, _ := push(t, h, textMime, []byte("a"))
	mustPin(t, h, a)
	for _, s := range []string{"b", "c", "d"} {
		push(t, h, textMime, []byte(s))
	}
	if got := pinnedTexts(h); !slices.Equal(got, []string{"*a", "d", "c"}) {
		t.Fatalf("entries = %v, want the pin first and two unpinned", got)
	}
}

func TestTheByteBudgetEvictsUnpinnedEntriesOnly(t *testing.T) {
	h := mustHistory(t, DefaultCapacity, 4, 8)
	a, _ := push(t, h, textMime, []byte("aaaa"))
	mustPin(t, h, a)
	for _, s := range []string{"bbbb", "cccc", "dddd"} {
		push(t, h, textMime, []byte(s))
	}
	if got := pinnedTexts(h); !slices.Equal(got, []string{"*aaaa", "dddd", "cccc"}) {
		t.Fatalf("entries = %v", got)
	}
	if err := h.SetBudget(4); err != nil {
		t.Fatal(err)
	}
	if got := pinnedTexts(h); !slices.Equal(got, []string{"*aaaa", "dddd"}) {
		t.Fatalf("after shrinking the budget: %v", got)
	}
}

func TestAPinPastThePinnedBudgetIsRefused(t *testing.T) {
	h, err := NewHistory(DefaultCapacity, 4, 64, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	a, _ := push(t, h, textMime, []byte("aaaa"))
	b, _ := push(t, h, textMime, []byte("bbbb"))
	c, _ := push(t, h, textMime, []byte("cccc"))
	mustPin(t, h, a)
	mustPin(t, h, b)
	if err := h.Pin(c); !errors.Is(err, ErrPinnedFull) {
		t.Fatalf("third pin: %v, want ErrPinnedFull", err)
	}
	if got := pinnedTexts(h); !slices.Equal(got, []string{"*bbbb", "*aaaa", "cccc"}) {
		t.Fatalf("a refused pin changed the history: %v", got)
	}
	if err := h.Pin(99); !errors.Is(err, ErrEntryGone) {
		t.Errorf("pin of a missing entry: %v", err)
	}
	if !h.Unpin(a) || h.Unpin(a) {
		t.Error("Unpin did not report the pin exactly once")
	}
	mustPin(t, h, c)
}

func TestUnpinningReturnsTheEntryToTheBudget(t *testing.T) {
	h := mustHistory(t, 1, historybudget.MaxEntryBytes, historybudget.MaxEntryBytes)
	a, _ := push(t, h, textMime, []byte("a"))
	mustPin(t, h, a)
	push(t, h, textMime, []byte("b"))
	h.Unpin(a)
	if got := pinnedTexts(h); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("entries = %v, want the unpinned, older entry evicted", got)
	}
}

func TestClearUnpinnedKeepsThePins(t *testing.T) {
	h := defaultHistory(t)
	a, _ := push(t, h, textMime, []byte("a"))
	push(t, h, textMime, []byte("b"))
	mustPin(t, h, a)
	h.ClearUnpinned()
	if got := pinnedTexts(h); !slices.Equal(got, []string{"*a"}) {
		t.Fatalf("entries = %v", got)
	}
	if h.size != 0 || h.count != 0 || h.pinnedSize != 1 {
		t.Errorf("accounting: %d bytes in %d unpinned, %d pinned bytes", h.size, h.count, h.pinnedSize)
	}
}

func TestRecopyingAPinnedEntryKeepsItsPinAndID(t *testing.T) {
	h := defaultHistory(t)
	a, _ := push(t, h, textMime, []byte("a"))
	mustPin(t, h, a)
	push(t, h, textMime, []byte("b"))
	again, _ := push(t, h, textMime, []byte("a"))
	if again != a || !h.pinned(a) {
		t.Fatalf("recopy: id %d (was %d), pinned %v", again, a, h.pinned(a))
	}
}

func TestARestoredEntryIsMatchedByItsDigest(t *testing.T) {
	h := defaultHistory(t)
	data := []byte("restored")
	h.restore([]Record{{Digest: DigestOf(data), Mime: textMime, Size: len(data)}}, true)
	if got := pinnedTexts(h); len(got) != 0 {
		t.Fatalf("an entry without its bytes is listed: %v", got)
	}
	todo := h.unloaded(0)
	if len(todo) != 1 || !todo[0].pinned {
		t.Fatalf("unloaded = %+v", todo)
	}
	id, kept := push(t, h, textMime, data)
	if !kept || id != todo[0].id || h.Len() != 1 {
		t.Fatalf("recopying restored bytes made entry %d (restored %d), %d entries", id, todo[0].id, h.Len())
	}
	if got := pinnedTexts(h); !slices.Equal(got, []string{"*restored"}) || len(h.unloaded(0)) != 0 {
		t.Fatalf("the recopy did not give the restored entry its bytes: %v", got)
	}
	other, _ := push(t, h, textMime, []byte("restorex"))
	if other == id {
		t.Error("bytes of the same size but another digest matched")
	}
}

func TestAPinnedBudgetBelowAnEntryIsRefused(t *testing.T) {
	if _, err := NewHistory(8, 16, 16, 15); err == nil {
		t.Error("a pinned budget below the largest entry was accepted")
	}
	if _, err := DefaultHistory(historybudget.DefaultBudget(), historybudget.Budget{}); err == nil {
		t.Error("a zero pinned budget built a history")
	}
	h := defaultHistory(t)
	if err := h.SetPinnedBudget(historybudget.MaxEntryBytes - 1); err == nil {
		t.Error("SetPinnedBudget accepted a budget below the largest entry")
	}
}
