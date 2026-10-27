package bar

import (
	"testing"

	"github.com/stubbedev/lys-ui/widget"
)

// The peek icon keeps the entry's size through a reveal and a reset,
// whatever class the caller styles the entry with; the reset masks the
// box again and empties it.
func TestSecretEntryPeekKeepsItsSize(t *testing.T) {
	s := newSecretEntry(testFont(t), 14)
	con := widget.Size{W: 100, H: 100}
	size := func() widget.Size { return widget.SizeWithin(s.entry.TrailingIcon(), con) }
	want := size()
	if want.W != 14 {
		t.Fatalf("peek icon %v, want 14px", want)
	}
	s.toggle()
	if got := s.entry.TrailingIcon(); got.Name() != iconEye || size() != want {
		t.Errorf("revealed: icon %q at %v", got.Name(), size())
	}
	s.entry.SetText("hunter2")
	s.reset()
	if got := s.entry.TrailingIcon(); got.Name() != iconEyeOff || size() != want || s.eye || s.entry.Text() != "" {
		t.Errorf("reset: icon %q at %v, revealed %v, text %q", got.Name(), size(), s.eye, s.entry.Text())
	}
}
