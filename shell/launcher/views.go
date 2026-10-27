package launcher

import (
	"math"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/shell/widgets"
	"github.com/stubbedev/lys-shell/styling"
)

// fontPx is the body size, which the launcher's labels
// take before the stylesheet (or -font) sizes them.
func fontPx() float64 { return widgets.TextPx(styling.Body, 1) }

// sessionCSSPriority puts a session's -font/-style CSS above the
// shell's own stylesheet, as the Rust provider sits above the shell's
// (PRIORITY_USER + 200 over + 100): at anything below, a -font simply
// lost to [styling] and looked like it did nothing.
const sessionCSSPriority = 100

// views is one session's widget tree (the view! of mod.rs).
type views struct {
	root     *widget.Box
	rev      *widget.Revealer
	surface  *widget.Box
	inputRow *widget.Box
	prompt   *widget.Label
	entry    *widget.Entry
	message  *widget.RichLabel
	frame    *widget.Box
	list     *resultList
	tabs     *widget.Box
	// width is the surface width it was mapped at; lastH the height it
	// was last sized to.
	width, lastH int
}

// buildViews builds and fills the tree for session a (apply_ui).
func (s *Surface) buildViews(a *active) *views {
	ui := a.ui
	v := &views{}
	v.root = widget.NewBox(widget.Column, 0, 0)
	v.root.SetElement("window")
	v.root.AddClass("launcher-window")
	if s.d.Sheet != nil {
		v.root.AttachStylesheet(s.d.Sheet)
	}
	if ui.css != "" {
		v.root.AttachStylesheet(widget.NewStylesheet(ui.css, s.d.SheetPriority+sessionCSSPriority))
	}
	// The surface is lys-ui's command palette: the command element and
	// its .input and .item parts take the palette's look.
	th := widget.Current()
	v.surface = widget.NewBox(widget.Column, 0, 0)
	v.surface.SetElement("command")
	v.surface.AddClass("launcher-surface")
	v.rev = widget.NewRevealer(v.surface)
	v.root.Append(v.rev, true)

	v.inputRow = widget.NewBox(widget.Row, th.Spacing, 0)
	v.inputRow.SetPadding(render.Insets{Left: th.Padding + th.Padding/2, Right: th.Padding + th.Padding/2})
	v.inputRow.AddClass("input", "launcher-input")
	v.prompt = widget.NewLabel(s.d.Font, fontPx(), "", 0)
	v.prompt.AddClass("launcher-prompt")
	v.inputRow.Append(v.prompt, false)
	v.entry = widget.NewEntry(s.d.Font, fontPx(), 0)
	v.entry.AddClass("launcher-entry")
	if ui.filter != nil {
		v.entry.SetText(*ui.filter)
	}
	v.setMasked(ui.password)
	v.entry.OnChanged = func(text string) {
		if s.cur == a {
			s.send(cmdSetQuery{query: text})
		}
	}
	v.inputRow.Append(v.entry, true)
	v.surface.Append(v.inputRow, false)

	v.message = widget.NewRichLabel(s.d.Font, fontPx(), "", 0)
	v.message.AddClass("launcher-message", css.DimLabel)
	v.message.SetMargin(render.Insets{Top: th.Padding, Left: th.Padding + th.Padding/2, Right: th.Padding + th.Padding/2})
	v.message.SetVariants(s.d.Variants)
	v.message.SetWrap(true)
	v.surface.Append(v.message, false)

	st := rowStyle{
		font: s.d.Font, variants: s.d.Variants, px: fontPx(), iconPx: iconPx(s.d), showIcons: ui.showIcons,
		display: rowDisplay{columns: ui.displayColumns, separator: ui.columnSeparator, ellipsize: ui.ellipsize},
	}
	v.list = newResultList(func(pos int) widget.Widget { return s.makeRow(a, pos, st) })
	v.list.visibleRows = max(ui.lines, 1)
	v.list.fixed = ui.fixedNumLines
	v.list.SetOnScroll(func(e widget.ScrollEvent) bool { return s.listScrolled(e.DY, mouseMods(e.Mods)) })
	v.frame = widget.NewBox(widget.Column, 0, th.Padding/2)
	v.frame.AddClass("launcher-list")
	v.frame.Append(v.list, true)
	v.surface.Append(v.frame, true)

	v.tabs = widget.NewBox(widget.Row, 0, 0)
	v.tabs.SetElement("togglegroup")
	v.tabs.AddClass("launcher-tabs")
	v.tabs.SetMargin(render.Insets{Left: th.Padding, Right: th.Padding, Bottom: th.Padding})
	v.tabs.SetHomogeneous(true)
	v.tabs.SetVisible(ui.sidebar)
	v.surface.Append(v.tabs, false)

	v.setPrompt(deref(ui.prompt, ""))
	message := ui.errorMessage
	if message == nil {
		message = ui.mesg
	}
	v.setMessage(message)
	v.inputRow.SetVisible(!a.dialog)
	v.frame.SetVisible(!a.dialog)
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
				icon := widget.NewFileIcon(made, st.iconPx)
				icon.AddClass("launcher-row-icon")
				row.iconSlot.Append(icon, false)
			})
		})
	}
	return row
}

// setPrompt shows the prompt, hidden when empty.
// setMasked shows the input as dots while it is a secret.
func (v *views) setMasked(on bool) {
	echo := widget.EchoNormal
	if on {
		echo = widget.EchoPassword
	}
	v.entry.SetEcho(echo)
}

func (v *views) setPrompt(text string) {
	v.prompt.SetText(text)
	v.prompt.SetVisible(text != "")
}

// setMessage shows the message line, hidden without one.
func (v *views) setMessage(markup *string) {
	if markup == nil {
		v.message.SetVisible(false)
		return
	}
	v.message.SetMarkup(messageMarkup(*markup))
	v.message.SetVisible(true)
}

// height is the tree's natural height at width.
func (v *views) height(width int) int {
	return v.surface.Measure(widget.Column, width).Nat
}

// iconPx is -gtk-icon-size: 1.5rem at the styling scale.
func iconPx(d Deps) int {
	return int(math.Round(1.5 * styling.RemBase * float64(d.Config().Styling.Scale)))
}
