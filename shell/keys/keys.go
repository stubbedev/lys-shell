// Package keys reads a surface's key presses by the symbol the seat's
// keymap gives them, never by evdev code: a client that sends its own
// keymap (wtype, a remote-desktop peer) puts its keys on codes of its
// own, and a keyboard layout moves letters, so a code names no key.
package keys

import (
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/unxed/xkb-go"
)

// Translator reads an evdev code through the seat's keymap and current
// modifier state (app.Application.KeySym).
type Translator func(code uint32) xkb.Keysym

// Handler receives one key press by symbol. Latin letters, whose
// keysyms are their code points, arrive lower case whatever Shift or
// Caps Lock did to them, so Ctrl+L is 'l' on any layout and lock state.
type Handler func(r *widget.Router, sym xkb.Keysym, mods app.Mods)

// OnKey is an OnKey hook for a window or layer that hands each press
// to h by symbol.
func OnKey(read Translator, h Handler) func(r *widget.Router, code uint32, mods app.Mods) {
	return func(r *widget.Router, code uint32, mods app.Mods) {
		h(r, fold(read(code)), mods)
	}
}

// OnEscape is an OnKey hook that runs fn on Escape, whatever modifiers
// are held, and ignores every other key.
func OnEscape(read Translator, fn func()) func(r *widget.Router, code uint32, mods app.Mods) {
	return OnKey(read, func(_ *widget.Router, sym xkb.Keysym, _ app.Mods) {
		if sym == xkb.KeyEscape {
			fn()
		}
	})
}

// fold lowers an upper-case Latin letter.
func fold(sym xkb.Keysym) xkb.Keysym {
	if sym >= 'A' && sym <= 'Z' {
		return sym + 'a' - 'A'
	}
	return sym
}
