package typetext

import (
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
)

func TestAKeymapBindsEachCharacterOnce(t *testing.T) {
	keymap, codes, err := Keymap("🧑‍🚀🧑")
	if err != nil {
		t.Fatal(err)
	}
	if want := []uint32{1, 2, 3, 1}; !slices.Equal(codes, want) {
		t.Errorf("codes %v, want %v", codes, want)
	}
	for _, sym := range []string{"U1F9D1", "U200D", "U1F680"} {
		if strings.Count(string(keymap), sym) != 1 {
			t.Errorf("%s not bound exactly once:\n%s", sym, keymap)
		}
	}
	if strings.Contains(string(keymap), "include") {
		t.Errorf("a keymap including files a client may not have:\n%s", keymap)
	}
}

func TestAKeymapRefusesWhatItCannotHold(t *testing.T) {
	if _, _, err := Keymap(""); err == nil {
		t.Error("no text must be an error")
	}
	var b strings.Builder
	for r := rune(0x4e00); r < 0x4e00+maxKeys+1; r++ {
		b.WriteRune(r)
	}
	if _, _, err := Keymap(b.String()); err == nil || !strings.Contains(err.Error(), "do not fit") {
		t.Errorf("too many characters: %v", err)
	}
}

// On a real compositor, what Type enters reaches the focused entry, and
// the keyboard's own keys still mean what they did afterwards.
func TestTypeEntersTextInTheFocusedWindow(t *testing.T) {
	a := apptest.Run(t)
	font, err := app.Font("sans", 14)
	if err != nil {
		t.Fatal(err)
	}
	entry := widget.NewEntry(font, 14, 0)
	if err := apptest.On(a, func() error {
		win, err := a.NewWindow(app.WindowConfig{Title: "typetext", Width: 300, Height: 80, Root: entry})
		if err != nil {
			return err
		}
		return win.SetFocus(entry)
	}); err != nil {
		t.Fatal(err)
	}
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	const want = "😆🧑‍🚀é"
	if err := Type(want); err != nil {
		t.Fatal(err)
	}
	apptest.Until(t, a, "the entry to hold the text", func() bool { return entry.Text() == want })
	kb.Tap(keytest.H)
	apptest.Until(t, a, "the seat's own key after it", func() bool { return entry.Text() == want+"h" })
}
