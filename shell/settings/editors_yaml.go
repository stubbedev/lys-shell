package settings

import (
	"strconv"

	"github.com/stubbedev/lys-ui/highlight"
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
)

// yamlEditor is the field as a YAML document under key, edited in a highlighted code view and applied as a whole; a
// document that does not parse, or a value the config refuses, marks
// the view instead of writing.
type yamlEditor struct {
	*widget.Box
	k      *kit
	slot   slot
	key    string
	scroll *widget.Scroll
	area   *widget.TextArea
	badge  *widget.Badge
	// shown is the document the stored value last rendered as: a
	// refresh replaces the text only when the value moved.
	shown string
}

func newYAMLEditor(k *kit, s slot, key string, minLines int) *yamlEditor {
	c := &yamlEditor{Box: widget.NewBox(widget.Column, 0, 0), k: k, slot: s, key: key}
	c.badge = k.badge(i18n.Settings().Get("settings-source-unsaved"), "warning")
	c.badge.SetVisible(false)
	c.area = widget.NewTextArea(k.mono, k.textPx(), 0)
	c.area.SetLineNumbers(true)
	c.area.SetIndent(2)
	c.area.SetAutoIndent(true)
	c.area.SetVariants(k.monoVariants)
	c.area.OnChanged = func() { c.badge.SetVisible(true) }
	c.scroll = widget.NewScroll(c.area)
	c.scroll.VerticalOnly, c.scroll.FillX, c.scroll.ShowBars = true, true, true
	c.scroll.AddClass("yaml-editor")
	if minLines > 0 {
		c.scroll.AddClass("yaml-editor-lines-" + strconv.Itoa(minLines))
	}
	apply := k.button(k.label(i18n.Settings().Get("settings-apply")), c.apply, css.SuggestedAction)
	c.Append(c.scroll, true)
	c.AppendAligned(apply, false, widget.AlignEnd)
	c.refresh()
	return c
}

func (c *yamlEditor) dirtyBadge() *widget.Badge { return c.badge }

// document renders the stored value under the key.
func (c *yamlEditor) document() string {
	text, err := config.YAMLDocument(map[string]any{c.key: c.slot.get()})
	if err != nil {
		return ""
	}
	return text
}

// refresh (on_refresh) shows the stored value when it moved, and
// recolors from the palette.
func (c *yamlEditor) refresh() {
	c.area.SetHighlighter(highlight.YAML{}, paletteScheme(c.k.store.svc.Config().Styling.Palette))
	doc := c.document()
	if doc == c.shown {
		return
	}
	c.shown = doc
	c.area.SetText(doc)
	c.badge.SetVisible(false)
}

// apply (on_save) parses the document and writes the key's value.
func (c *yamlEditor) apply() {
	doc, err := config.ParseYAML(c.area.Text())
	v, ok := doc[c.key]
	if err != nil || !ok || c.slot.set(v) != nil {
		c.scroll.AddClass("error")
		return
	}
	c.scroll.RemoveClass("error")
	c.badge.SetVisible(false)
}

// yamlRow is the YAML editor for a row: the document under key, at
// least minLines tall.
func yamlRow(key string, minLines int) rowOpt {
	return func(r *rowSpec) {
		r.fullWidth = true
		r.editor = func(k *kit, s slot, _ config.FieldMeta) control { return newYAMLEditor(k, s, key, minLines) }
	}
}

// paletteScheme is build_scheme_xml: the code view's styles from the
// palette.
func paletteScheme(p config.PaletteConfig) widget.TextScheme {
	col := func(h config.HexColor) render.Color {
		r, g, b, a := h.RGBA()
		return render.RGBA(r, g, b, a)
	}
	bg, surface, fg, muted := col(p.Bg), col(p.Surface), col(p.Fg), col(p.FgMuted)
	primary, red, green, yellow, blue := col(p.Primary), col(p.Red), col(p.Green), col(p.Yellow), col(p.Blue)
	return widget.TextScheme{
		"text":             {Color: fg},
		"cursor":           {Color: primary},
		"selection":        {Color: fg, Background: surface},
		"line-numbers":     {Color: muted, Background: bg},
		"def:keyword":      {Color: blue, Bold: true},
		"def:string":       {Color: green},
		"def:number":       {Color: primary},
		"def:boolean":      {Color: primary},
		"def:comment":      {Color: muted, Italic: true},
		"def:type":         {Color: yellow},
		"def:constant":     {Color: primary},
		"def:identifier":   {Color: fg},
		"def:special-char": {Color: red},
		"def:heading":      {Color: blue, Bold: true},
	}
}
