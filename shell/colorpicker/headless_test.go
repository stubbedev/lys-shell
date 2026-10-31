package colorpicker

import (
	"image"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/internal/keytest"
)

// Escape cancels the picker on a real compositor by the symbol the
// keyboard's keymap gives it; the code a physical keyboard gives Escape,
// Return in that keymap, leaves the picker open.
func TestHeadlessEscapeCancelsByItsSymbol(t *testing.T) {
	a := apptest.Run(t)
	font, err := app.Font("monospace", 13)
	if err != nil {
		t.Fatal(err)
	}
	p := New(a, a.Session().Outputs, font, "")
	t.Cleanup(p.Cancel)
	frames := map[string]render.StillImage{}
	apptest.Until(t, a, "an output appeared", func() bool { return len(a.Session().Outputs()) > 0 })
	a.Invoke(func() {
		for _, out := range a.Session().Outputs() {
			frames[out.Name] = render.NewStillImage(image.NewRGBA(image.Rect(0, 0, 64, 64)))
		}
	})
	answers := make(chan result, 1)
	go func() {
		c, ok, err := p.Request(frames)
		answers <- result{color: c, ok: ok, err: err}
	}()
	kb := keytest.Dial(t, a)
	keytest.AwaitFocus(t, a)
	kb.Tap(keytest.Return)
	apptest.Never(t, "Return on a physical keyboard's Escape code", answers)
	kb.Tap(keytest.Escape)
	if r := apptest.Answer(t, "the picker after Escape", answers); r.ok || r.err != nil {
		t.Fatalf("Escape answered %+v, want a cancel", r)
	}
}
