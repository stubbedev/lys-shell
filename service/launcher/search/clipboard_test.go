package search

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/clipboard"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/charnames"
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
	h, err := clipboard.NewHistory(clipboard.DefaultCapacity, clipboard.MaxEntryBytes, clipboard.MaxEntryBytes*4, clipboard.MaxEntryBytes*4)
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
func (f *fakeHistory) ClearUnpinned()               { f.history.ClearUnpinned() }
func (*fakeHistory) CanPin() bool                   { return true }
func (f *fakeHistory) Pin(id uint64) error          { return f.history.Pin(id) }
func (f *fakeHistory) Unpin(id uint64) error {
	f.history.Unpin(id)
	return nil
}

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
	content, err := launcher.LoadPreview(ctx, got[0].Preview, nil)
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
	content, err := launcher.LoadPreview(ctx, got[0].Preview, nil)
	if err != nil || content.Image == nil {
		t.Fatalf("content %+v, %v", content, err)
	}
	h.history.Clear()
	if _, err := launcher.LoadPreview(ctx, got[0].Preview, nil); err == nil {
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
	if root.MinQuery < 3 || root.Section != SectionClipboard {
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

func TestEmojiRowsSearchTheirKeywordsAndEnterTheGlyph(t *testing.T) {
	p := NewEmoji(fakeEmoji, modes.ToneNone, nil)
	got := loaded(t, p)
	if len(got) != 2 || got[1].Glyph != "🚀" || !strings.Contains(got[1].Keywords, "launch") {
		t.Fatalf("rows %+v", got)
	}
	actions := p.Actions(got[1])
	if c, ok := actions[0].Run(ctx).(Type); !ok || c.Text != "🚀" {
		t.Errorf("the default action %+v, want the glyph entered", actions[0].Run(ctx))
	}
	if c, ok := actions[1].Run(ctx).(Copy); !ok || c.Text != "🚀" {
		t.Errorf("copy %+v", actions[1].Run(ctx))
	}
	if c, ok := actions[2].Run(ctx).(Copy); !ok || c.Text != "rocket" {
		t.Errorf("copy name %+v", actions[2].Run(ctx))
	}
	if got := p.Spec(); got.MinQuery < 2 {
		t.Errorf("one letter must not list emoji: %+v", got)
	}
}

func TestEmojiInAListFindsByKeyword(t *testing.T) {
	s := &sink{}
	e := start(t, s, nil, NewEmoji(fakeEmoji, modes.ToneNone, nil))
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
	if got := pickerTitles(t, NewPickers(nil, PickerLevels{}, true)); got != nil {
		t.Errorf("nothing available: %v", got)
	}
	h := newFakeHistory(t, [2]string{"text/plain", "one"})
	vault := &Level{Title: "V"}
	p := NewPickers(h, PickerLevels{
		Emoji:      func() *Level { return EmojiLevel(fakeEmoji, modes.ToneNone, nil) },
		Characters: func() *Level { return CharactersLevel(charnames.Shared) },
		Vault:      func() *Level { return vault },
	}, true)
	if got := pickerTitles(t, p); !slices.Equal(got, []string{"picker:clipboard", "picker:emoji", "picker:characters", "picker:vault"}) {
		t.Fatalf("rows %v", got)
	}
	rows := loaded(t, p)
	for i, want := range []Section{SectionNone, SectionNone, SectionNone} {
		push, ok := p.Actions(rows[i])[0].Run(ctx).(Push)
		if !ok || push.Level.Providers[0].Spec().Section != want {
			t.Errorf("picker %d: %+v", i, push)
		}
	}
	if push := p.Actions(rows[3])[0].Run(ctx).(Push); push.Level != vault {
		t.Errorf("the vault row opens the vault's level: %+v", push)
	}
}

var ctx = context.Background()

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func actionTitled(t *testing.T, actions []Action, title string) Action {
	t.Helper()
	for _, a := range actions {
		if a.Title == title {
			return a
		}
	}
	t.Fatalf("no %q action in %d actions", title, len(actions))
	return Action{}
}

func TestClipboardPinsListFirstAndSurviveClearUnpinned(t *testing.T) {
	h := newFakeHistory(t, [2]string{"text/plain", "keep me"}, [2]string{"text/plain", "newer"})
	c := NewClipboard(h, false)
	got := loaded(t, c)
	if got[1].Hint != "" {
		t.Errorf("an unpinned row is marked %q", got[1].Hint)
	}
	actions := c.Actions(got[1])
	for _, a := range actions {
		if a.Title == i18n.T("launcher-action-unpin") {
			t.Error("an unpinned row offers Unpin")
		}
	}
	if _, ok := actionTitled(t, actions, i18n.T("launcher-action-pin")).Run(ctx).(Reload); !ok {
		t.Fatal("pin did not reload")
	}
	got = loaded(t, c)
	if got[0].Title != "keep me" || got[0].Hint != i18n.T("launcher-clipboard-pinned") {
		t.Fatalf("rows %+v, want the pin first and marked", got)
	}
	actions = c.Actions(got[0])
	actionTitled(t, actions, i18n.T("launcher-action-clear-unpinned")).Run(ctx)
	if got = loaded(t, c); len(got) != 1 || got[0].Title != "keep me" {
		t.Fatalf("clear unpinned left %+v", got)
	}
	actionTitled(t, c.Actions(got[0]), i18n.T("launcher-action-unpin")).Run(ctx)
	if got = loaded(t, c); got[0].Hint != "" {
		t.Error("unpinned row still marked")
	}
}

func TestClipboardForgetIsCtrlBackspace(t *testing.T) {
	h := newFakeHistory(t, [2]string{"text/plain", "one"})
	c := NewClipboard(h, false)
	actions := c.Actions(loaded(t, c)[0])
	var keyed []string
	for _, a := range actions {
		if a.Key == ActionKeyDelete {
			keyed = append(keyed, a.Title)
		}
	}
	if !slices.Equal(keyed, []string{i18n.T("launcher-action-forget")}) {
		t.Errorf("Ctrl+Backspace runs %v, want Forget alone", keyed)
	}
	if ActionKeyDelete.Label() != "Ctrl+Backspace" || ActionKeyNone.Label() != "" {
		t.Error("key labels")
	}
}

func TestARefusedPinIsSaidInTheFooter(t *testing.T) {
	h := newFakeHistory(t, [2]string{"text/plain", "x"})
	c := NewClipboard(h, false)
	row := loaded(t, c)[0]
	pin := actionTitled(t, c.Actions(row), i18n.T("launcher-action-pin"))
	h.history.Remove(row.Data.(clipboardData).id)
	if s, ok := pin.Run(ctx).(Stay); !ok || s.Message == "" {
		t.Error("a refused pin must say why")
	}
}

func TestNoPinActionWithoutAStore(t *testing.T) {
	h := &noPinHistory{newFakeHistory(t, [2]string{"text/plain", "x"})}
	c := NewClipboard(h, false)
	for _, a := range c.Actions(loaded(t, c)[0]) {
		if a.Title == i18n.T("launcher-action-pin") {
			t.Error("Pin offered where nothing can keep it")
		}
	}
}

type noPinHistory struct{ *fakeHistory }

func (noPinHistory) CanPin() bool { return false }
