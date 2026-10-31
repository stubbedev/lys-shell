// Package keytest drives the shell's surfaces on a private headless
// sway from a virtual keyboard whose keymap, like wtype's or a
// remote-desktop peer's, carries its keys on codes of its own: each one
// on the code a physical keyboard gives a different key. A surface that
// reads its keys through the keymap acts on them; one that matches
// evdev codes acts on the wrong ones.
package keytest

import (
	"testing"

	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/apptest"
)

// The evdev codes the keyboard types its keys on, each the code of
// another key on a physical keyboard.
const (
	// Return is on evdev 1, a physical keyboard's Escape.
	Return = 1
	// Escape is on evdev 28, a physical keyboard's Return.
	Escape = 28
	// L is on evdev 35, a physical keyboard's H.
	L = 35
	// H is on evdev 38, a physical keyboard's L.
	H = 38
	// Tab is on evdev 30, a physical keyboard's A.
	Tab = 30
	// Space is on evdev 31, a physical keyboard's S.
	Space = 31
	// Left is on evdev 32, a physical keyboard's D.
	Left = 32
	// Right is on evdev 33, a physical keyboard's F.
	Right = 33
	// Up is on evdev 34, a physical keyboard's G.
	Up = 34
	// Down is on evdev 36, a physical keyboard's J.
	Down = 36
	// PageUp is on evdev 37, a physical keyboard's K.
	PageUp = 37
	// PageDown is on evdev 44, a physical keyboard's Z.
	PageDown = 44
)

// keymap places the keys above. Control is a real modifier whatever
// key carries it, so Ctrl chords need no key of their own.
const keymap = `xkb_keymap {
	xkb_keycodes "remapped" {
		minimum = 8; maximum = 255;
		<K1> = 9; <K28> = 36; <K35> = 43; <K38> = 46;
		<K30> = 38; <K31> = 39; <K32> = 40; <K33> = 41; <K34> = 42; <K36> = 44; <K37> = 45; <K44> = 52;
	};
	xkb_types { include "complete" };
	xkb_compat { include "complete" };
	xkb_symbols "remapped" {
		key <K1> { [ Return ] };
		key <K28> { [ Escape ] };
		key <K35> { [ l, L ] };
		key <K38> { [ h, H ] };
		key <K30> { [ Tab, ISO_Left_Tab ] };
		key <K31> { [ space ] };
		key <K32> { [ Left ] };
		key <K33> { [ Right ] };
		key <K34> { [ Up ] };
		key <K36> { [ Down ] };
		key <K37> { [ Prior ] };
		key <K44> { [ Next ] };
	};
};
`

// controlMask is the Control real modifier's bit.
const controlMask = 1 << 2

// Keyboard is the remapped virtual keyboard, with a pointer, typing to
// an application.
type Keyboard struct {
	*vinput.Device
	t *testing.T
	a *app.Application
}

// Dial connects the remapped keyboard to the sway apptest.Run started
// for a.
func Dial(t *testing.T, a *app.Application) *Keyboard {
	t.Helper()
	dev, err := vinput.Dial("", vinput.Options{Keyboard: true, FallbackKeymap: []byte(keymap)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(dev.Close)
	return &Keyboard{Device: dev, t: t, a: a}
}

// Tap presses and releases the key on code and returns once the
// application has handled both, so what the key does (or does not do)
// is there to observe.
func (k *Keyboard) Tap(code uint32) {
	k.t.Helper()
	for _, pressed := range []bool{true, false} {
		if err := k.Key(code, pressed); err != nil {
			k.t.Fatal(err)
		}
	}
	if err := k.Roundtrip(); err != nil {
		k.t.Fatal(err)
	}
	apptest.Dispatched(k.t, k.a)
}

// CtrlTap taps the key on code with Control held.
func (k *Keyboard) CtrlTap(code uint32) {
	k.t.Helper()
	if err := k.Modifiers(controlMask, 0, 0, 0); err != nil {
		k.t.Fatal(err)
	}
	k.Tap(code)
	if err := k.Modifiers(0, 0, 0, 0); err != nil {
		k.t.Fatal(err)
	}
}

// AwaitFocus waits until one of a's surfaces holds the keyboard, so
// the keys tapped next reach it.
func AwaitFocus(t *testing.T, a *app.Application) {
	t.Helper()
	apptest.Until(t, a, "a surface took the keyboard", func() bool { return a.Session().KeyboardFocus() != nil })
}
