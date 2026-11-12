package dialogsurface_test

import (
	"bytes"
	"testing"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/apptheme"
	"github.com/stubbedev/lys-desktop/shell/treetest"
	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/shell/dialogsurface"
)

const cardMargin = 40

type cardShot struct {
	px   []byte
	w    int
	card render.Rect
}

func (s cardShot) at(x, y int) []byte {
	i := y*render.Stride(s.w) + x*4
	return s.px[i : i+4]
}

func paintCardRoot(t *testing.T, surfaces ...dialogsurface.Surface) (cardShot, widget.Widget) {
	t.Helper()
	treetest.Loop.Lock()
	defer treetest.Loop.Unlock()
	card := widgets.DialogCard(treetest.Font(t), "Print", "print-surface", 360)
	card.SetDescription("Choose a printer.")
	sheet := apptheme.New(config.Defaults()).Sheet
	var (
		root *widget.Box
		rev  *widget.Revealer
	)
	for _, s := range surfaces {
		root, rev = dialogsurface.CardRoot(s, card, "print-window", sheet)
	}
	rev.SetRevealed(true)
	rev.Finish()
	size := widget.SizeWithin(root, widget.Size{W: 1000, H: 1000})
	shot := cardShot{w: size.W + 2*cardMargin, card: render.Rect{X: cardMargin, Y: cardMargin, W: size.W, H: size.H}}
	h := size.H + 2*cardMargin
	shot.px = make([]byte, render.Stride(shot.w)*h)
	root.Arrange(shot.card)
	root.Paint(render.New(shot.px, render.Stride(shot.w), shot.w, h))
	return shot, root
}

func TestACardInAWindowDrawsNoFrame(t *testing.T) {
	shot, root := paintCardRoot(t, dialogsurface.OnWindow)
	if widget.HasClass(root, "print-window") {
		t.Error("the window carries the floating card's transparent class")
	}
	c := shot.card
	inner := shot.at(c.X+6, c.Y+c.H/2)
	for name, px := range map[string][]byte{
		"top left corner":     shot.at(c.X, c.Y),
		"bottom right corner": shot.at(c.X+c.W-1, c.Y+c.H-1),
		"bottom left corner":  shot.at(c.X, c.Y+c.H-1),
		"left edge":           shot.at(c.X, c.Y+c.H/2),
		"right edge":          shot.at(c.X+c.W-1, c.Y+c.H/2),
	} {
		if px[3] != 0xff || !bytes.Equal(px, inner) {
			t.Errorf("the %s is %v, want the opaque fill %v: no border, rounding or shadow", name, px, inner)
		}
	}
}

func TestACardOnTheOverlayFloatsInItsFrame(t *testing.T) {
	for name, surfaces := range map[string][]dialogsurface.Surface{
		"opened there":        {dialogsurface.OnOverlay},
		"moved from a window": {dialogsurface.OnWindow, dialogsurface.OnOverlay},
	} {
		t.Run(name, func(t *testing.T) {
			shot, root := paintCardRoot(t, surfaces...)
			if !widget.HasClass(root, "print-window") {
				t.Error("the overlay's window lacks the floating card's transparent class")
			}
			c := shot.card
			if px := shot.at(c.X, c.Y+c.H-1); px[3] == 0 || px[3] == 0xff {
				t.Errorf("the card's bottom left corner is %v, want it rounded over its shadow", px)
			}
			if edge, inner := shot.at(c.X, c.Y+c.H/2), shot.at(c.X+6, c.Y+c.H/2); bytes.Equal(edge, inner) {
				t.Errorf("the card's left edge is its fill %v, want its border", edge)
			}
		})
	}
}
