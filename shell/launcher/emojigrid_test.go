package launcher

import (
	"fmt"
	"testing"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
)

type gridHarness struct {
	*harness
	reply <-chan launcheripc.ServerFrame
	typed []string
}

func gridEmoji() []modes.Emoji {
	out := []modes.Emoji{thumbsUp()}
	for i := range 29 {
		out = append(out, modes.Emoji{Glyph: string(rune(0x1f600 + i)), Name: fmt.Sprintf("face %02d", i), Keywords: []string{"smile"}})
	}
	return append(out, modes.Emoji{Glyph: "🚀", Name: "rocket", Keywords: []string{"launch"}})
}

// thumbsUp is the real table's thumbs up, which takes a tone.
func thumbsUp() modes.Emoji {
	for _, e := range modes.AvailableEmoji() {
		if e.Name == "thumbs up" {
			return e
		}
	}
	panic("no thumbs up in the emoji table")
}

func newGridHarness(t *testing.T, tone config.LauncherSkinTone) *gridHarness {
	t.Helper()
	h := &gridHarness{harness: newHarness(t)}
	h.cfg.Launcher.Emoji.SkinTone = tone
	h.s.d.Emoji = gridEmoji
	h.s.d.Type = func(text string) error {
		h.s.d.Invoke(func() { h.typed = append(h.typed, text) })
		return nil
	}
	mode := EmojiMode
	_, h.reply = h.open(launcheripc.SessionOptions{Mode: &mode}, false)
	h.until("the grid", func() bool { return h.s.emojiGrid != nil && h.s.emojiGrid.win != nil })
	g := h.s.emojiGrid
	g.grid.Arrange(render.Rect{W: emojiColumns * 52, H: emojiLines * 52})
	return h
}

func (h *gridHarness) press(spec string) {
	h.t.Helper()
	accel, err := app.ParseAccel(spec)
	if err != nil {
		h.t.Fatal(err)
	}
	if !h.windows[len(h.windows)-1].cfg.KeyCapture(accel) {
		h.t.Fatalf("%s went to the field", spec)
	}
}

func TestTheEmojiGridMovesInTwoDimensionsAndNamesTheSelection(t *testing.T) {
	h := newGridHarness(t, config.SkinToneNone)
	g := h.s.emojiGrid
	if len(g.shown) != 31 || g.chrome.notice.Text() != "thumbs up" {
		t.Fatalf("%d shown, footer %q", len(g.shown), g.chrome.notice.Text())
	}
	h.press("Right")
	if g.grid.Selected() != 1 || g.chrome.notice.Text() != "face 00" {
		t.Errorf("right: %d %q", g.grid.Selected(), g.chrome.notice.Text())
	}
	h.press("Down")
	if g.grid.Selected() != 1+emojiColumns || g.chrome.notice.Text() != "face 10" {
		t.Errorf("down moves a row of %d: %d %q", emojiColumns, g.grid.Selected(), g.chrome.notice.Text())
	}
	h.press("Left")
	h.press("Up")
	if g.grid.Selected() != 0 {
		t.Errorf("back to the start: %d", g.grid.Selected())
	}
}

func TestTheEmojiGridFiltersByKeywordAndTypesThePickInTone(t *testing.T) {
	h := newGridHarness(t, config.SkinToneMedium)
	g := h.s.emojiGrid
	if got := (emojiModel{g}).Row(0); got == nil {
		t.Fatal("no cell")
	}
	g.chrome.entry.SetText("launch")
	if len(g.shown) != 1 || g.chrome.notice.Text() != "rocket" {
		t.Fatalf("launch shows %d, footer %q", len(g.shown), g.chrome.notice.Text())
	}
	g.chrome.entry.SetText("nothing like it")
	if len(g.shown) != 0 || !g.chrome.message.Visible() || g.chrome.notice.Text() != "" {
		t.Errorf("no match: %d shown, message %v, footer %q", len(g.shown), g.chrome.message.Visible(), g.chrome.notice.Text())
	}
	h.press("Return")
	g.chrome.entry.SetText("thumbs")
	win := h.windows[0]
	h.press("Return")
	f := h.frame(h.reply)
	if f.Type != launcheripc.FrameResult || len(f.Selected) != 1 || f.Selected[0].Text != "👍🏽" {
		t.Fatalf("frame %+v", f)
	}
	h.until("the surface closing", func() bool { return win.closed })
	h.timers[len(h.timers)-1].fn()
	h.until("the glyph typed", func() bool { return len(h.typed) == 1 && h.typed[0] == "👍🏽" })
}

func TestTheEmojiGridCopiesWithControlEnterAndEscapeCancels(t *testing.T) {
	h := newGridHarness(t, config.SkinToneDark)
	h.press("Control+Return")
	if f := h.frame(h.reply); f.Type != launcheripc.FrameResult || len(h.copied) != 1 || h.copied[0] != "👍🏿" || len(h.typed) != 0 {
		t.Errorf("control+enter: %+v, copied %q, typed %q", f, h.copied, h.typed)
	}
	again := newGridHarness(t, config.SkinToneNone)
	again.press("Escape")
	if f := again.frame(again.reply); f.Type != launcheripc.FrameCancelled || len(again.copied) != 0 || again.s.emojiGrid != nil {
		t.Errorf("escape: %+v, copied %q", f, again.copied)
	}
}

func TestTheEmojiBindTogglesTheGrid(t *testing.T) {
	h := newGridHarness(t, config.SkinToneNone)
	mode := EmojiMode
	_, second := h.open(launcheripc.SessionOptions{Mode: &mode}, false)
	if f := h.frame(second); f.Type != launcheripc.FrameCancelled {
		t.Errorf("the second bind answered %+v", f)
	}
	if f := h.frame(h.reply); f.Type != launcheripc.FrameCancelled || h.s.emojiGrid != nil {
		t.Errorf("the grid stayed: %+v", f)
	}
}

func TestShowUnicodeOpensThePanelOnEveryCharacter(t *testing.T) {
	ph := newPanelHarness(t)
	mode := UnicodeMode
	_, reply := ph.open(launcheripc.SessionOptions{Mode: &mode}, false)
	ph.until("the panel", func() bool { return ph.s.panel != nil && ph.s.panel.win != nil })
	p := ph.s.panel
	ph.typed(p, "interrobang")
	ph.waitTitles(p, "Interrobang", "Inverted Interrobang", "Heavy Interrobang Ornament", "Sans-Serif Interrobang Ornament", "Heavy Sans-Serif Interrobang Ornament")
	ph.key("Return")
	if f := ph.frame(reply); f.Type != launcheripc.FrameResult || len(f.Selected) != 1 || f.Selected[0].Text != "‽" {
		t.Errorf("enter answered %+v", f)
	}
}
