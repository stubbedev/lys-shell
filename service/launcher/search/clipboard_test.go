package search

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

// fakeHistory is a ClipboardHistory over a real history: only putting an
// entry back on the clipboard, which needs a seat, is recorded instead.
type fakeHistory struct {
	history *clipboard.History
	copied  []uint64
	refuse  bool
}

func newFakeHistory(t *testing.T, selections ...[2]string) *fakeHistory {
	t.Helper()
	h, err := clipboard.NewHistory(clipboard.DefaultCapacity, clipboard.MaxEntryBytes, clipboard.MaxEntryBytes*4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	for _, s := range selections {
		if _, _, err := h.Push(s[0], []byte(s[1])); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeHistory{history: h}
}

func (f *fakeHistory) Entries() *clipboard.Snapshot { return f.history.Entries() }
func (f *fakeHistory) Forget(id uint64) bool        { return f.history.Remove(id) }
func (f *fakeHistory) Clear()                       { f.history.Clear() }
func (f *fakeHistory) Copy(id uint64) bool {
	f.copied = append(f.copied, id)
	return !f.refuse
}

func loaded(t *testing.T, p Provider) []Entry {
	t.Helper()
	var got []Entry
	p.Load(context.Background(), func(e []Entry) { got = e })
	return got
}

func TestClipboardRowsAreTheHistoryNewestFirst(t *testing.T) {
	h := newFakeHistory(t, [2]string{"text/plain", "older"}, [2]string{"text/plain", "first line\nsecond line"})
	c := NewClipboard(h, true)
	got := loaded(t, c)
	if len(got) != 2 || got[0].Title != "first line second line" || got[1].Title != "older" {
		t.Fatalf("rows %+v", got)
	}
	content, err := LoadPreview(ctx, got[0].Preview, nil)
	if err != nil || content.Text != "first line\nsecond line" {
		t.Errorf("the pane keeps the lines: %q, %v", content.Text, err)
	}
	if got[0].Learn != "" {
		t.Error("clipboard contents must never reach the frecency file")
	}
}

func TestClipboardActionsRestoreForgetAndClear(t *testing.T) {
	h := newFakeHistory(t, [2]string{"text/plain", "one"}, [2]string{"text/plain", "two"})
	c := NewClipboard(h, false)
	got := loaded(t, c)
	if got[0].Preview.Any() {
		t.Error("previews off")
	}
	actions := c.Actions(got[0])
	if _, ok := actions[0].Run(ctx).(Close); !ok || len(h.copied) != 1 {
		t.Errorf("restore: copied %v", h.copied)
	}
	h.refuse = true
	if s, ok := actions[0].Run(ctx).(Stay); !ok || s.Message == "" {
		t.Error("an entry that left the history must say so")
	}
	if _, ok := actions[len(actions)-2].Run(ctx).(Reload); !ok || len(loaded(t, c)) != 1 {
		t.Errorf("forget left %d", len(loaded(t, c)))
	}
	if _, ok := actions[len(actions)-1].Run(ctx).(Reload); !ok || len(loaded(t, c)) != 0 {
		t.Errorf("clear left %d", len(loaded(t, c)))
	}
}

func TestClipboardImagePreviewReadsTheEntryWhenAsked(t *testing.T) {
	h := newFakeHistory(t, [2]string{"image/png", string(pngBytes(t))})
	c := NewClipboard(h, true)
	got := loaded(t, c)
	if got[0].Preview.Load == nil {
		t.Fatalf("preview %+v", got[0].Preview)
	}
	content, err := LoadPreview(ctx, got[0].Preview, nil)
	if err != nil || content.Image == nil {
		t.Fatalf("content %+v, %v", content, err)
	}
	h.history.Clear()
	if _, err := LoadPreview(ctx, got[0].Preview, nil); err == nil {
		t.Error("an entry gone since the row was drawn is an error")
	}
}

func TestClipboardFileEntryPreviewsTheFileAndCopiesItsPath(t *testing.T) {
	path := writeTemp(t, "a.txt", []byte("x"))
	h := newFakeHistory(t, [2]string{"text/uri-list", "file://" + path + "\r\n"})
	c := NewClipboard(h, true)
	got := loaded(t, c)
	if got[0].Preview.Path != path {
		t.Errorf("preview %+v", got[0].Preview)
	}
	copied := ""
	for _, a := range c.Actions(got[0]) {
		if a.Title == i18n.T("launcher-action-copy-path") {
			if o, ok := a.Run(ctx).(Copy); ok {
				copied = o.Text
			}
		}
	}
	if copied != path {
		t.Errorf("copy path gave %q", copied)
	}
}

func TestNoHistoryOffersNothing(t *testing.T) {
	if got := loaded(t, NewClipboard(nil, true)); got != nil {
		t.Errorf("rows %+v", got)
	}
}

func TestClipboardStaysOutOfTheRootUntilItIsAskedFor(t *testing.T) {
	root := NewClipboard(newFakeHistory(t), true).Spec()
	if root.MinQuery < 3 || root.Limit == 0 {
		t.Errorf("root spec %+v", root)
	}
	flat := (&Clipboard{flat: true}).Spec()
	if flat.Section != SectionNone || flat.MinQuery != 0 {
		t.Errorf("picker spec %+v", flat)
	}
}

func fakeEmoji() []modes.Emoji {
	return []modes.Emoji{
		{Glyph: "🙂", Name: "slightly smiling face", Keywords: []string{"happy"}},
		{Glyph: "🚀", Name: "rocket", Keywords: []string{"launch", "space"}},
	}
}

func TestEmojiRowsSearchTheirKeywordsAndCopyTheGlyph(t *testing.T) {
	p := NewEmoji(fakeEmoji)
	got := loaded(t, p)
	if len(got) != 2 || got[1].Glyph != "🚀" || !strings.Contains(got[1].Keywords, "launch") {
		t.Fatalf("rows %+v", got)
	}
	actions := p.Actions(got[1])
	if c, ok := actions[0].Run(ctx).(Copy); !ok || c.Text != "🚀" {
		t.Errorf("copy %+v", c)
	}
	if c, ok := actions[1].Run(ctx).(Copy); !ok || c.Text != "rocket" {
		t.Errorf("copy name %+v", c)
	}
	if got := p.Spec(); got.MinQuery < 2 {
		t.Errorf("one letter must not list emoji: %+v", got)
	}
}

func TestEmojiInAListFindsByKeyword(t *testing.T) {
	s := &sink{}
	e := start(t, s, nil, NewEmoji(fakeEmoji))
	waitFor(t, "the table", func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return len(s.frames) >= 2
	})
	e.SetQuery("launch", 1)
	until(t, s, "#"+SectionEmoji.Title(), "rocket")
}

func pickerTitles(t *testing.T, p *Pickers) []string {
	t.Helper()
	var out []string
	for _, e := range loaded(t, p) {
		out = append(out, e.ID)
	}
	return out
}

func TestPickersOfferOnlyWhatIsThereAndOpenALevel(t *testing.T) {
	if got := pickerTitles(t, NewPickers(nil, nil, nil, true)); got != nil {
		t.Errorf("nothing available: %v", got)
	}
	h := newFakeHistory(t, [2]string{"text/plain", "one"})
	vault := &Level{Title: "V"}
	p := NewPickers(h, fakeEmoji, func() *Level { return vault }, true)
	if got := pickerTitles(t, p); !slices.Equal(got, []string{"picker:clipboard", "picker:emoji", "picker:vault"}) {
		t.Fatalf("rows %v", got)
	}
	rows := loaded(t, p)
	for i, want := range []Section{SectionNone, SectionNone} {
		push, ok := p.Actions(rows[i])[0].Run(ctx).(Push)
		if !ok || push.Level.Providers[0].Spec().Section != want {
			t.Errorf("picker %d: %+v", i, push)
		}
	}
	if push := p.Actions(rows[2])[0].Run(ctx).(Push); push.Level != vault {
		t.Errorf("the vault row opens the vault's level: %+v", push)
	}
}
