package launcher

import (
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
)

// fontPx is the body size, which the launcher's labels
// take before the stylesheet (or -font) sizes them.
func fontPx() float64 { return widgets.TextPx(styling.Body, 1) }

// sessionCSSPriority puts a session's -font/-style CSS above the
// shell's own stylesheet, as the Rust provider sits above the shell's
// (PRIORITY_USER + 200 over + 100): at anything below, a -font simply
// lost to [styling] and looked like it did nothing.
const sessionCSSPriority = 100

// views is one session's widget tree: the frame and chrome every session
// has, around the list a rofi session shows.
type views struct {
	*panelFrame
	*panelChrome
	frame *widget.Box
	list  *resultList
	pane  *previewPane
	tabs  *widget.Box
	// width is the panel's width before the output holds it in.
	width int
}

// buildViews builds and fills the tree for session a (apply_ui).
func (s *Surface) buildViews(a *active) *views {
	ui := a.ui
	cfg := s.d.Config()
	px := scaledPx(cfg)
	th := widget.Current()
	v := &views{width: s.resolveWidth(ui)}
	v.panelChrome = newPanelChrome(s.d, cfg, func(text string) {
		if s.cur == a {
			s.send(cmdSetQuery{query: text})
		}
	})
	v.searchIcon.show("ld-search-symbolic")
	if ui.filter != nil {
		v.entry.SetText(*ui.filter)
	}
	v.setMasked(ui.password)

	st := rowStyle{
		post: s.d.Invoke,
		font: s.d.Font, variants: s.d.Variants, px: fontPx(), iconPx: px(searchIconSize), showIcons: ui.showIcons,
		display: rowDisplay{columns: ui.displayColumns, separator: ui.columnSeparator, ellipsize: ui.ellipsize},
	}
	v.list = newResultList(px(searchRowHeight), rowMargin(px), func(pos int) widget.Widget { return s.makeRow(a, pos, st) })
	v.list.visibleRows = max(ui.lines, 1)
	v.list.fixed = ui.fixedNumLines
	v.list.SetOnScroll(func(e widget.ScrollEvent) bool { return s.listScrolled(e.DY, mouseMods(e.Mods)) })
	v.frame = widget.NewBox(widget.Column, 0, 0)
	v.frame.AddClass("launcher-list")
	v.frame.Append(v.list, true)
	v.pane = newPreviewPane(s.d.Font, px, previewMinRows*v.list.rowH+2*v.list.edge())
	body := widget.NewBox(widget.Row, 0, 0)
	body.Append(v.frame, true)
	body.Append(v.pane.box, false)

	v.tabs = widget.NewBox(widget.Row, 0, 0)
	v.tabs.SetElement("togglegroup")
	v.tabs.AddClass("launcher-tabs")
	v.tabs.SetMargin(render.Insets{Left: th.Padding, Right: th.Padding, Bottom: th.Padding})
	v.tabs.SetHomogeneous(true)
	v.tabs.SetVisible(ui.sidebar)

	content := widget.NewBox(widget.Column, 0, 0)
	content.Append(v.input, false)
	content.Append(v.message, false)
	content.Append(v.sep, false)
	content.Append(body, true)
	content.Append(v.tabs, false)
	content.Append(v.footer, false)

	v.panelFrame = newPanelFrame(s.d, content, panelWidthIn(render.Rect{}, v.width), px(inputRowHeight), func() Window { return a.win })
	if ui.css != "" {
		v.root.AttachStylesheet(widget.NewStylesheet(ui.css, s.d.SheetPriority+sessionCSSPriority))
	}
	v.target = v.targetIn
	v.place = func(out render.Rect, w, h int) render.Rect {
		return placePanel(out, w, h, ui.location, ui.offsetX, ui.offsetY)
	}

	v.setPrompt(deref(ui.prompt, ""))
	message := ui.errorMessage
	if message == nil {
		message = ui.mesg
	}
	v.showMessage(message)
	v.input.SetVisible(!a.dialog)
	v.frame.SetVisible(!a.dialog)
	v.setRows(0)
	return v
}

// makeRow is the row factory for list position pos; a thumbnail still
// being made lands on the row if it is still showing that file.
func (s *Surface) makeRow(a *active, pos int, st rowStyle) widget.Widget {
	it, _ := s.model.item(pos)
	index, _ := s.model.itemIndex(pos)
	row := buildRow(pos, it, pos == s.sel, st, &s.multi, index)
	row.SetOnPointer(func(e widget.PointerEvent) bool {
		s.rowPressed(pos, gdkButton(e.Button), e.Clicks, mouseMods(e.Mods))
		return true
	})
	if path := thumbnailPending(it.Icon); path != "" && st.showIcons {
		generateThumbnail(a.ctx, a.thumbs, path, func(made string) {
			s.d.Invoke(func() {
				if s.cur != a || a.views.list.rows[pos] != widget.Widget(row) {
					return
				}
				row.iconSlot.Clear()
				icon := widgets.NewAsyncFileIcon(made, "", st.iconPx, s.d.Invoke, nil)
				icon.AddClass("launcher-row-icon")
				row.iconSlot.Append(icon, false)
			})
		})
	}
	return row
}

// setMasked shows the input as dots while it is a secret.
func (v *views) setMasked(on bool) {
	echo := widget.EchoNormal
	if on {
		echo = widget.EchoPassword
	}
	v.entry.SetEcho(echo)
}

func (v *views) setPrompt(text string) { v.setChip(text) }

// showMessage shows the message line, hidden without one.
func (v *views) showMessage(markup *string) { v.setMessage(deref(markup, "")) }

// setRows shows the separator and the footer's hints while there are rows
// to act on, as the search panel does.
func (v *views) setRows(n int) {
	v.sep.SetVisible(n > 0)
	v.footer.SetVisible(n > 0)
	if n > 0 {
		v.hintBuf = append(v.hintBuf[:0], footerHint{"Enter", i18n.T("launcher-footer-select")}, closeHint())
		v.showHints()
	}
}

// targetIn is the panel's size in an output: as tall as its content, up to
// the room below where the panel sits.
func (v *views) targetIn(out render.Rect) (w, h int) {
	w = v.width
	if v.pane.box.Visible() {
		w += v.pane.width
	}
	w = panelWidthIn(out, w)
	h = v.content.Measure(widget.Column, w).Nat
	if out.H > 0 {
		h = min(h, int(float64(out.H)*(1-panelTopFraction))-panelEdgeGap)
	}
	return w, max(h, 1)
}

// showPane shows the preview pane beside the list, previewMinRows rows
// tall so a short list still has room to preview.
func (v *views) showPane(on bool) {
	if on == v.pane.box.Visible() {
		return
	}
	v.pane.box.SetVisible(on)
}
