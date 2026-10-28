package keys

import (
	"testing"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/unxed/xkb-go"
)

// remapped reads codes the way a keymap like wtype's does: Escape on
// evdev 28, Return on evdev 1, the reverse of a physical keyboard.
func remapped(code uint32) xkb.Keysym {
	switch code {
	case 1:
		return xkb.KeyReturn
	case 28:
		return xkb.KeyEscape
	case 38:
		return 'L'
	}
	return xkb.KeyNoSymbol
}

func TestOnEscapeFollowsTheSymbolNotTheCode(t *testing.T) {
	escaped := 0
	hook := OnEscape(remapped, func() { escaped++ })
	hook(nil, 1, 0)
	if escaped != 0 {
		t.Fatal("the code a physical keyboard gives Escape escaped, though this keymap puts Return there")
	}
	hook(nil, 28, app.ModCtrl)
	if escaped != 1 {
		t.Fatalf("Escape on evdev 28 ran the hook %d times, want once", escaped)
	}
}

func TestOnKeyFoldsLetters(t *testing.T) {
	var got []xkb.Keysym
	hook := OnKey(remapped, func(_ *widget.Router, sym xkb.Keysym, mods app.Mods) {
		if mods != app.ModCtrl {
			t.Errorf("mods = %v, want Ctrl passed through", mods)
		}
		got = append(got, sym)
	})
	hook(nil, 38, app.ModCtrl)
	hook(nil, 1, app.ModCtrl)
	if len(got) != 2 || got[0] != 'l' || got[1] != xkb.KeyReturn {
		t.Fatalf("symbols = %#x, want 'l' folded from 'L', then Return untouched", got)
	}
}
