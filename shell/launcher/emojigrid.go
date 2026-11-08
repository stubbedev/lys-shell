package launcher

import (
	"log"
	"math"
	"strings"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/launcheripc"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/launcher/search"
)

// EmojiMode is the mode name that opens the emoji grid rather than a list.
const EmojiMode = "emoji"

// The grid's measures, in logical pixels at scale 1.
const (
	emojiColumns = 10
	emojiLines   = 6
	emojiCell    = 52
	emojiGlyphPx = 30
)

func wantsEmojiGrid(o launcheripc.SessionOptions) bool {
	return !o.Dmenu && o.ErrorMessage == nil && o.Mode != nil && *o.Mode == EmojiMode
}

// emojiGrid is one emoji grid session on the UI loop: every emoji in the
// configured skin tone, filtered by name and keyword, arrows moving in two
// dimensions, the selected one's name in the footer.
type emojiGrid struct {
	s       *Surface
	sess    *launcheripc.Session
	win     Window
	frame   *panelFrame
	chrome  *panelChrome
	grid    *widget.GridView
	emojis  []modes.Emoji
	hay     [][]rune
	shown   []int
	tone    modes.SkinTone
	query   *engine.FuzzyQuery
	glyphPx float64
	closing bool
	// afterClose runs once the surface has gone and the keyboard with it.
	afterClose func()
}

// openEmojiGrid starts an emoji grid session.
func (s *Surface) openEmojiGrid(sess *launcheripc.Session) {
	cfg := s.d.Config()
	g := &emojiGrid{s: s, sess: sess, emojis: s.d.Emoji(), tone: search.SkinToneOf(cfg.Launcher.Emoji.SkinTone), query: engine.NewFuzzyQuery("")}
	g.hay = make([][]rune, len(g.emojis))
	for i, e := range g.emojis {
		g.hay[i] = engine.Haystack(e.Name + " " + strings.Join(e.Keywords, " "))
	}
	g.build(cfg)
	s.emojiGrid = g
	win, err := s.d.Open(panelLayer(cfg.Styling.Translucent(), s.outputFor(cfg.Launcher.Monitor), g.frame, g.keyCaptured, func() { g.finish("", false) }, func() {
		if s.emojiGrid == g {
			g.win = nil
			g.finish("", false)
		}
	}))
	if err != nil {
		log.Printf("launcher: cannot map the surface: %v", err)
		g.finish("", false)
		return
	}
	g.win = win
	if err := win.SetFocus(g.chrome.entry); err != nil {
		log.Printf("launcher: cannot focus the entry: %v", err)
		g.finish("", false)
		return
	}
	if sess.Options.Filter != nil {
		g.chrome.entry.SetText(*sess.Options.Filter)
	}
	g.filter(g.chrome.entry.Text())
	g.frame.motion.open(cfg.Animations)
}

func (g *emojiGrid) build(cfg *config.Config) {
	px := scaledPx(cfg)
	cell := px(emojiCell)
	g.glyphPx = math.Round(emojiGlyphPx * float64(cfg.Styling.Scale))
	g.chrome = newPanelChrome(g.s.d, cfg, g.filter)
	g.chrome.entry.SetPlaceholder(i18n.T("launcher-placeholder-filter"))
	g.chrome.setChip(i18n.T("launcher-picker-emoji"))
	g.grid = widget.NewGridView(emojiModel{g}, cell, cell)
	g.grid.SetMinColumns(emojiColumns)
	g.grid.SetMaxColumns(emojiColumns)
	g.grid.SetMaxHeight(emojiLines * cell)
	g.grid.AddClass("launcher-emoji-grid")
	g.grid.OnSelect = g.selected
	g.grid.OnActivate = func(int) { g.accept(false) }
	g.chrome.hintBuf = append(g.chrome.hintBuf[:0],
		footerHint{"Enter", i18n.T("launcher-action-type")},
		footerHint{"Ctrl+Enter", i18n.T("launcher-action-copy")},
		closeHint())
	g.chrome.showHints()
	g.chrome.footer.SetVisible(true)
	content := widget.NewBox(widget.Column, 0, 0)
	content.Append(g.chrome.input, false)
	content.Append(g.chrome.message, false)
	content.Append(g.grid, true)
	content.Append(g.chrome.footer, false)
	width := emojiColumns*cell + px(2*rowEdgeGap)
	height := int(math.Round(inputRowHeight*float64(cfg.Styling.Scale))) + emojiLines*cell + int(math.Round(footerHeight*float64(cfg.Styling.Scale)))
	g.frame = newPanelFrame(g.s.d, content, width, height, func() Window { return g.win })
	g.frame.target = func(out render.Rect) (int, int) { return panelWidthIn(out, width), height }
	g.frame.place = func(out render.Rect, w, h int) render.Rect { return placePanel(out, w, h, config.LauncherCenter, 0, 0) }
}

// filter narrows the grid to the emoji whose name or keywords match text.
func (g *emojiGrid) filter(text string) {
	if g.closing {
		return
	}
	g.query.Set(text)
	g.shown = g.shown[:0]
	for i := range g.emojis {
		if g.query.Empty() {
			g.shown = append(g.shown, i)
			continue
		}
		if _, ok := g.query.Score(g.hay[i]); ok {
			g.shown = append(g.shown, i)
		}
	}
	g.grid.Reset()
	switch {
	case len(g.emojis) == 0:
		g.chrome.setMessage(i18n.T("launcher-emoji-none"))
	case len(g.shown) == 0:
		g.chrome.setMessage(i18n.T("launcher-emoji-no-match"))
	default:
		g.chrome.setMessage("")
	}
	if len(g.shown) == 0 {
		g.chrome.notice.SetText("")
		return
	}
	g.grid.Select(0)
	g.selected(0)
}

// emoji is the emoji at grid position pos.
func (g *emojiGrid) emoji(pos int) (modes.Emoji, bool) {
	if pos < 0 || pos >= len(g.shown) {
		return modes.Emoji{}, false
	}
	return g.emojis[g.shown[pos]], true
}

// selected names the selection in the footer.
func (g *emojiGrid) selected(pos int) {
	if e, ok := g.emoji(pos); ok {
		g.chrome.notice.SetText(e.Name)
	}
}

// accept enters the selection in the window the grid leaves focused, or
// copies it.
func (g *emojiGrid) accept(copyIt bool) {
	e, ok := g.emoji(g.grid.Selected())
	if !ok {
		return
	}
	g.finish(e.WithTone(g.tone), copyIt)
}

func (g *emojiGrid) keyCaptured(press app.Accel) bool {
	if g.closing {
		return false
	}
	action, ok := pickerKeys[press]
	if !ok {
		return false
	}
	switch action {
	case pickerCommit:
		g.accept(false)
	case pickerCommitAll:
		g.accept(true)
	case pickerCancel:
		g.finish("", false)
	default:
		g.grid.KeyAction(widget.KeyAction(action), 0)
	}
	return true
}

// finish ends the session: glyph "" cancels, otherwise it is entered once
// the surface is gone, or copied.
func (g *emojiGrid) finish(glyph string, copyIt bool) {
	if g.closing {
		return
	}
	g.closing = true
	frame := launcheripc.ServerFrame{Type: launcheripc.FrameCancelled, Code: 1}
	if glyph != "" {
		frame = launcheripc.ServerFrame{Type: launcheripc.FrameResult, Selected: []launcheripc.Selected{{Index: 0, Text: glyph}}}
		if copyIt {
			if g.s.d.Copy != nil {
				g.s.d.Copy(glyph)
			}
		} else {
			g.afterClose = func() { g.s.typeOnceClosed(glyph) }
		}
	}
	g.teardown()
	g.sess.Reply(frame)
}

// release ends the session for a client that went away.
func (g *emojiGrid) release() {
	if g.closing {
		return
	}
	g.closing = true
	g.teardown()
	g.sess.Release()
}

func (g *emojiGrid) teardown() {
	if g.s.emojiGrid == g {
		g.s.emojiGrid = nil
	}
	if g.win == nil {
		return
	}
	win := g.win
	g.frame.motion.close(g.s.d.Config().Animations, func() {
		win.Close()
		if g.afterClose != nil {
			g.afterClose()
		}
	})
}

// emojiModel is the grid's cells: the shown emoji, each its glyph.
type emojiModel struct{ g *emojiGrid }

func (m emojiModel) Len() int { return len(m.g.shown) }

func (m emojiModel) Row(pos int) widget.Widget {
	g := m.g
	label := widget.NewLabel(g.s.d.Font, g.glyphPx, g.emojis[g.shown[pos]].WithTone(g.tone), 0)
	label.SetAlignment(render.AlignCenter)
	label.AddClass("launcher-emoji-cell")
	return label
}
