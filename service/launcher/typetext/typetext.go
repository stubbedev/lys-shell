// Package typetext enters text in the focused window through a virtual
// keyboard, whatever the seat's layout can reach: the keyboard carries a
// keymap of its own with one key per character.
package typetext

import (
	"bytes"
	"errors"
	"fmt"
	"slices"

	"github.com/stubbedev/lys-lib/vinput"
)

// keycodeOffset is what xkb adds to an evdev code; evdev 0 is reserved,
// so the first key is evdev 1.
const keycodeOffset = 8

// maxKeys is how many evdev codes fit below xkb's largest keycode.
const maxKeys = 255 - keycodeOffset

// Keymap is xkb_v1 text binding one key to each of the distinct
// characters of text, and those keys' evdev codes in text's order. It
// spells out every section: it includes nothing a client's xkb would
// have to find.
func Keymap(text string) (keymap []byte, codes []uint32, err error) {
	var distinct []rune
	for _, r := range text {
		at := slices.Index(distinct, r)
		if at < 0 {
			at = len(distinct)
			distinct = append(distinct, r)
		}
		codes = append(codes, uint32(at)+1) //nolint:gosec // at is below maxKeys once checked, and small either way
	}
	if len(distinct) == 0 {
		return nil, nil, errors.New("typetext: no text to enter")
	}
	if len(distinct) > maxKeys {
		return nil, nil, fmt.Errorf("typetext: %d distinct characters do not fit a keymap's %d keys", len(distinct), maxKeys)
	}
	var b bytes.Buffer
	b.WriteString("xkb_keymap {\nxkb_keycodes \"typetext\" {\n\tminimum = 8;\n\tmaximum = 255;\n")
	for i := range distinct {
		fmt.Fprintf(&b, "\t<K%d> = %d;\n", i, i+1+keycodeOffset)
	}
	b.WriteString("};\nxkb_types \"typetext\" {\n\ttype \"ONE_LEVEL\" {\n\t\tmodifiers = none;\n\t\tlevel_name[Level1] = \"Any\";\n\t};\n};\n")
	b.WriteString("xkb_compat \"typetext\" {\n\tinterpret Any + AnyOf(all) {\n\t\taction = NoAction();\n\t};\n};\n")
	b.WriteString("xkb_symbols \"typetext\" {\n")
	for i, r := range distinct {
		fmt.Fprintf(&b, "\tkey <K%d> { type = \"ONE_LEVEL\", [ U%04X ] };\n", i, r)
	}
	b.WriteString("};\n};\n")
	return b.Bytes(), codes, nil
}

// Type enters text in the focused window, each character a key press and
// release, the compositor having taken them all when it returns.
func Type(text string) error {
	keymap, codes, err := Keymap(text)
	if err != nil {
		return err
	}
	dev, err := vinput.Dial("", vinput.Options{Keyboard: true, Keymap: keymap})
	if err != nil {
		return fmt.Errorf("typetext: a virtual keyboard to enter %q with: %w", text, err)
	}
	defer dev.Close()
	for _, code := range codes {
		if err := errors.Join(dev.Key(code, true), dev.Key(code, false)); err != nil {
			return fmt.Errorf("typetext: enter %q: %w", text, err)
		}
	}
	if err := dev.Roundtrip(); err != nil {
		return fmt.Errorf("typetext: enter %q: %w", text, err)
	}
	return nil
}
