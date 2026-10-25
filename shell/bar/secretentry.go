package bar

import (
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
)

// Reveal toggle glyphs (helpers.rs's ICON_EYE / ICON_EYE_OFF).
const (
	iconEye    = "ld-eye-symbolic"
	iconEyeOff = "ld-eye-off-symbolic"
)

// secretEntry is a masked Entry with the peek icon every secret box
// carries (attach_reveal_toggle): a mistyped key or password is
// otherwise only fixable by retyping it. The eye is the entry's
// trailing icon, the entry > image node. The entry itself goes into
// the tree; the network forms add their own class to it.
type secretEntry struct {
	entry *widget.Entry
	px    float64
	eye   bool
}

func newSecretEntry(font render.Font, px float64) *secretEntry {
	s := &secretEntry{entry: widget.NewEntry(font, px, 0), px: px}
	s.entry.SetEcho(widget.EchoPassword)
	s.entry.SetTrailingIcon(iconEyeOff, px, s.toggle)
	return s
}

// toggle flips the reveal.
func (s *secretEntry) toggle() {
	s.eye = !s.eye
	s.entry.EchoReveal(s.eye)
	if s.eye {
		s.entry.SetTrailingIcon(iconEye, s.px, s.toggle)
	} else {
		s.entry.SetTrailingIcon(iconEyeOff, s.px, s.toggle)
	}
}

// reset is reset_reveal_toggle: masked again, and emptied.
func (s *secretEntry) reset() {
	s.entry.SetText("")
	s.entry.EchoReveal(false)
	s.eye = false
	s.entry.SetTrailingIcon(iconEyeOff, s.px, s.toggle)
}
