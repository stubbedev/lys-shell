package credential

import (
	"image"
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/shell/treetest"
	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// TestBuildShape pins the prompt: the secret entry masks and submits
// on Enter, the clock follows ShowClock, the error line starts hidden,
// and the greeter's username entry moves focus to the secret entry.
func TestBuildShape(t *testing.T) {
	var submitted []string
	var focused []widget.Widget
	p := Build(Options{
		Font: treetest.Font(t), ShowClock: true, WithUsername: true,
		Focus: func(w widget.Widget) { focused = append(focused, w) },
	}, func(s string) { submitted = append(submitted, s) })
	if p.Entry.Echo() != widget.EchoPassword {
		t.Error("the secret entry must mask its text")
	}
	p.Entry.SetText("hunter2")
	p.Entry.KeyAction(widget.KeyEnter, 0)
	if len(submitted) != 1 || submitted[0] != "hunter2" {
		t.Errorf("submits = %q", submitted)
	}
	if p.Username == nil {
		t.Fatal("WithUsername built no username entry")
	}
	p.Username.KeyAction(widget.KeyEnter, 0)
	if len(focused) != 1 || focused[0] != p.Entry {
		t.Error("Enter in the username entry must move focus to the secret entry")
	}
	if !p.Clock.Visible() || p.Error.Visible() {
		t.Error("clock shown, error hidden at start")
	}
	p.SetMessage("Incorrect password")
	if !p.Error.Visible() || p.Error.Text() != "Incorrect password" {
		t.Error("SetMessage must show the text")
	}
	p.SetMessage("")
	if p.Error.Visible() {
		t.Error("an empty message hides the line")
	}

	lock := Build(Options{Font: treetest.Font(t)}, func(string) {})
	if lock.Username != nil || lock.Clock.Visible() {
		t.Error("the lock-screen prompt has no username entry, and ShowClock false hides the clock")
	}
}

// TestBuildTakesLysUiParts pins the prompt on lys-ui's parts: the
// column is lys-ui's card, the clock a numeric heading at the display
// scale of the theme's text size, the error line lys-ui's error text;
// only the clock is a heading.
func TestBuildTakesLysUiParts(t *testing.T) {
	p := Build(Options{Font: treetest.Font(t), ShowClock: true}, func(string) {})
	cards := treetest.WithClass(p.Reveal, "lock-center")
	if len(cards) != 1 || len(treetest.WithClass(cards[0], css.Card)) != 1 {
		t.Fatal("the credential column must be lys-ui's card")
	}
	text := widgets.TextPx(styling.Body, 1)
	if p.Clock.SizePx() != text*styling.Hero.Ratio() || !slices.Contains(p.Clock.Classes(), css.Heading) || !slices.Contains(p.Clock.Classes(), css.Numeric) {
		t.Errorf("clock = %vpx %v, want %vpx numeric heading", p.Clock.SizePx(), p.Clock.Classes(), text*styling.Hero.Ratio())
	}
	if p.Date.SizePx() != text*styling.Subtitle.Ratio() || slices.Contains(p.Date.Classes(), css.Heading) {
		t.Errorf("date = %vpx %v, want %vpx, no heading", p.Date.SizePx(), p.Date.Classes(), text*styling.Subtitle.Ratio())
	}
	if !slices.Contains(p.Error.Classes(), css.Error) || slices.Contains(p.Error.Classes(), css.Heading) {
		t.Errorf("error line classes = %v", p.Error.Classes())
	}
}

func TestBackground(t *testing.T) {
	c, _ := config.ParseHexColor("#102030")
	if got := HexFill(c); got != render.RGB(0x10, 0x20, 0x30) {
		t.Errorf("HexFill = %#x", uint32(got))
	}
	fill, ok := Background(nil, HexFill(c)).(*widget.DrawingArea)
	if !ok || !slices.Contains(fill.Classes(), "lock-bg") {
		t.Error("no image: the .lock-bg color fill")
	}
	over, ok := Background(&Backdrop{Image: render.NewStillImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))}, 0).(*widget.Overlay)
	if !ok || len(treetest.WithClass(over, "lock-bg-scrim")) != 1 {
		t.Error("an image: picture under the .lock-bg-scrim")
	}
}

// The peek icon reveals the masked secret and masks it again.
func TestPeekTogglesTheEcho(t *testing.T) {
	p := Build(Options{Font: treetest.Font(t)}, func(string) {})
	p.togglePeek()
	if !p.peeked {
		t.Fatal("the peek did not reveal")
	}
	p.togglePeek()
	if p.peeked {
		t.Error("a second peek left the secret shown")
	}
}

// The scrim starts hidden and is the .lock-scrim node.
func TestScrim(t *testing.T) {
	s := Scrim()
	if s.Visible() || !slices.Contains(s.Classes(), "lock-scrim") {
		t.Error("the scrim starts hidden as .lock-scrim")
	}
}
