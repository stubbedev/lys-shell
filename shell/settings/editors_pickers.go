package settings

import (
	"log"
	"math"
	"path/filepath"
	"slices"
	"strings"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/icons"
	"github.com/stubbedev/lys-shell/styling"
)

// The editors that open a picker (editors/font, editors/color).

// pickers opens what an editor picks from: a popover beside an anchor,
// the font dialog and the file chooser. The window provides it; tests
// fake it.
type pickers interface {
	// popover shows content beside anchor with focus, inside it,
	// taking the keys; close dismisses it.
	popover(anchor, content, focus widget.Widget) (close func())
	// font asks for a font family, initially family, monospace ones
	// only when monospace, calling fn with the pick.
	font(family string, monospace bool, fn func(family string))
	// openFile asks the file chooser for one file, calling fn on the
	// loop with its path; a dismissed chooser calls nothing.
	openFile(fn func(path string))
}

// fontPickerLabelChars caps the face's label (FontControl).
const fontPickerLabelChars = 20

// fontEditor is FontControl: a menu button showing the family that
// opens lys-ui's font dialog for families alone, monospace ones only
// for the monospace field.
type fontEditor struct {
	*widget.Box
	k         *kit
	slot      slot
	monospace bool
	label     *widget.Label
	btn       *widget.Button
}

func newFontEditor(k *kit, s slot, monospace bool) *fontEditor {
	c := &fontEditor{Box: widget.NewBox(widget.Row, 0, 0), k: k, slot: s, monospace: monospace}
	c.SetElement("menubutton")
	c.AddClass("font")
	c.label = k.label("", "font-picker-label")
	c.label.SetEllipsize(widget.EllipsizeEnd)
	c.label.SetMaxWidthChars(fontPickerLabelChars)
	c.btn = k.button(c.label, c.open, css.Outline)
	c.Append(c.btn, false)
	c.refresh()
	return c
}

func (c *fontEditor) refresh() {
	s, _ := c.slot.get().(string)
	c.label.SetText(s)
}

// open asks the font dialog; a pick writes the family.
func (c *fontEditor) open() {
	if c.k.pickers == nil {
		return
	}
	current, _ := c.slot.get().(string)
	c.k.pickers.font(current, c.monospace, func(family string) { _ = c.slot.set(family) })
}

// openPicker opens the popover build makes beside anchor. build gets
// the pick its tree calls (onPick, then the popover closes) and returns
// the tree and the widget in it taking the keys as it opens.
func openPicker(k *kit, anchor widget.Widget, onPick func(string), build func(pick func(string)) (root *widget.Box, focus widget.Widget)) {
	var closePicker func()
	root, focus := build(func(name string) {
		onPick(name)
		closePicker()
	})
	closePicker = k.pickers.popover(anchor, root, focus)
}

// pickerFrame is a picker popover's tree: the popover node carrying
// class over its contents, which the theme's padding insets on every
// side and its spacing separates.
func pickerFrame(class string) (root, contents *widget.Box) {
	th := widget.Current()
	root = widget.NewBox(widget.Column, 0, 0)
	root.AddClass(class)
	contents = widget.NewBox(widget.Column, th.Spacing, th.Padding)
	contents.SetElement("contents")
	root.Append(contents, true)
	return root, contents
}

// matcher reports the names containing the query (StringFilter,
// Substring, ignore case), every one for a blank one.
func matcher(query string) func(name string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	return func(name string) bool { return strings.Contains(strings.ToLower(name), q) }
}

// matching are the names matcher keeps for the query, in order.
func matching(names []string, query string) []string {
	match := matcher(query)
	var out []string
	for _, name := range names {
		if match(name) {
			out = append(out, name)
		}
	}
	return out
}

// pickerSpec shapes a searchPicker: the popover's CSS class, the
// search placeholder key, the content of the row for a name and the
// list's height cap.
type pickerSpec struct {
	class, placeholder string
	row                func(k *kit, name string) widget.Widget
	maxH               int
	// searchEntry makes the search a gtk::SearchEntry (the module
	// picker): the search icon, the clear button, Esc clearing, and the
	// filter on search-changed after GTK's default delay. The others
	// are plain gtk::Entry filtering per keystroke.
	searchEntry bool
}

// searchPicker is the list pickers' popover (the module picker, the
// theme presets): a search row over a scrolling column of every
// name's row, those not containing the query (ignoring case) hidden,
// drawn as a dropdown's list: on the popover's own background, as wide
// as its widest row, each row's content inset in a band that rounds
// and tints under the pointer. One click on a row picks it, writes and
// closes the popover.
type searchPicker struct {
	root   *widget.Box
	search *widget.Entry
	// field is the search as the tree holds it (the SearchEntry
	// around search, or search itself), the widget taking the keys.
	field  widget.Widget
	scroll *widget.Scroll
	// rows holds a row per name of all, in its order.
	rows *widget.FlowBox
	all  []string
	pick func(string)
}

// openSearchPicker builds the picker and opens it beside anchor.
func openSearchPicker(k *kit, anchor widget.Widget, names []string, spec pickerSpec, onPick func(string)) *searchPicker {
	var p *searchPicker
	openPicker(k, anchor, onPick, func(pick func(string)) (*widget.Box, widget.Widget) {
		p = newSearchPicker(k, names, spec, pick)
		return p.root, p.field
	})
	return p
}

func newSearchPicker(k *kit, names []string, spec pickerSpec, pick func(string)) *searchPicker {
	p := &searchPicker{all: names, pick: pick}
	root, contents := pickerFrame(spec.class)
	p.root = root
	if spec.searchEntry {
		se := widget.NewSearchEntry(k.face, k.textPx(), i18n.Settings().Get(spec.placeholder))
		// The stylesheet inks it, as k.entry's.
		se.SetColor(0)
		se.SetTextWidth(widget.GTKTextWidth)
		se.OnSearchChanged = p.filter
		p.search, p.field = se.Entry, se
	} else {
		p.search = k.searchField(spec.placeholder, p.filter)
		p.field = p.search
	}
	contents.Append(p.field, false)
	p.rows = widget.NewFlowBox(0, 0)
	p.rows.AddClass(searchPickerListClass)
	p.rows.SetMaxChildrenPerLine(1)
	p.rows.SetJustify(widget.JustifyFill)
	p.rows.OnActivate = func(i int) { p.pick(p.all[i]) }
	for _, name := range names {
		p.rows.Append(pickerRow(spec.row(k, name)))
	}
	p.scroll = widget.NewScroll(p.rows)
	p.scroll.VerticalOnly = true
	p.scroll.SetMaxContentHeight(spec.maxH)
	contents.Append(p.scroll, true)
	return p
}

// searchPickerListClass styles every searchPicker's rows' bands.
const searchPickerListClass = "search-picker-list"

// pickerRowInsets inset a picker row's content in its band: the
// theme's padding across, half its spacing above and below.
func pickerRowInsets() render.Insets {
	th := widget.Current()
	return render.Insets{Left: th.Padding, Right: th.Padding, Top: th.Spacing / 2, Bottom: th.Spacing / 2}
}

// pickerRow is the band holding a picker row's content.
func pickerRow(content widget.Widget) *widget.Box {
	row := widget.NewBox(widget.Row, 0, 0)
	row.SetPadding(pickerRowInsets())
	row.Append(content, true)
	return row
}

// filter shows the rows of the names matching the query alone.
func (p *searchPicker) filter(query string) {
	match := matcher(query)
	for i, name := range p.all {
		p.rows.ChildAt(i).SetVisible(match(name))
	}
}

// colorEditor is ColorControl: lys-ui's color button, its swatch
// picker's pick written as hex (rgba_to_hex: #rrggbb, #rrggbbaa with
// alpha).
type colorEditor struct {
	*widget.ColorButton
	slot slot
}

func newColorEditor(k *kit, s slot) *colorEditor {
	c := colorButton(k, s)
	c.refresh()
	return c
}

// colorButton is the editor before it read its value: the color-value
// control shows it only over a hex.
func colorButton(k *kit, s slot) *colorEditor {
	c := &colorEditor{ColorButton: widget.NewColorButton(k.face, k.textPx(), 0), slot: s}
	c.OnColorSet = func(col render.Color) { _ = c.slot.set(styling.HexRGBA(col)) }
	return c
}

// refresh shows the stored hex; a value that is no hex color is
// logged, the button keeping what it showed.
func (c *colorEditor) refresh() {
	s, _ := c.slot.get().(string)
	col, ok := styling.ParseHex(s)
	if !ok {
		log.Printf("settings: color editor: %q is no hex color", s)
		return
	}
	c.SetColor(col)
}

// pathEntryChars is a path field's width in characters: every path row
// takes the same, kept when the row narrows, its description giving
// way instead.
const pathEntryChars = 14

// fileEditor is FilePickerControl: the path entry (committing on
// Enter, with the unsaved badge) and a browse button asking the file
// chooser portal; a pick writes the path. Out of focus the field shows
// the path ellipsized in the middle, its start and its file name, over
// the entry's own text, with the whole path in the tooltip; focused it
// edits the full text.
type fileEditor struct {
	*widget.Box
	field   *widget.Overlay
	entry   *text
	display *widget.Label
	browse  *widget.Button
	focused bool
}

func newFileEditor(k *kit, s slot) *fileEditor {
	c := &fileEditor{Box: gapBox(widget.Row), field: widget.NewOverlay(), entry: newText(k, s, false)}
	c.entry.SetWidthChars(pathEntryChars, pathEntryChars)
	c.display = k.label("", "setting-path-display")
	c.display.SetEllipsize(widget.EllipsizeMiddle)
	c.display.SetMaxWidthChars(pathEntryChars)
	c.field.AddClass("setting-path")
	c.field.Append(c.entry).AppendAligned(c.display, widget.AlignFill, widget.AlignCenter)
	edited := c.entry.OnChanged
	c.entry.OnChanged = func(path string) {
		edited(path)
		c.show(path)
	}
	c.entry.SetOnFocusChanged(func(focused bool) {
		c.focused = focused
		c.show(c.entry.Text())
	})
	c.browse = k.browseButton(func(file string) {
		if s.set(file) == nil {
			c.entry.badge.SetVisible(false)
		}
	})
	c.AppendAligned(c.field, false, widget.AlignCenter)
	c.AppendAligned(c.browse, false, widget.AlignCenter)
	c.show(c.entry.Text())
	return c
}

// show puts path in the resting display and the tooltip, the display
// standing over the entry only while it is out of focus and holds a
// path, so an empty field shows its placeholder.
func (c *fileEditor) show(path string) {
	c.display.SetText(path)
	c.entry.SetTooltip(path)
	rest := path != "" && !c.focused
	c.display.SetVisible(rest)
	if rest {
		c.field.AddClass("at-rest")
	} else {
		c.field.RemoveClass("at-rest")
	}
}

func (c *fileEditor) dirtyBadge() *widget.Badge { return c.entry.badge }

func (c *fileEditor) refresh() { c.entry.refresh() }

// filePath is file_picker::file_path for a row.
var filePath = withEditor(func(k *kit, s slot, _ config.FieldMeta) control { return newFileEditor(k, s) })

// fontRow is font::font for a row; monoFontRow offers monospace
// families only.
var (
	fontRow     = withEditor(func(k *kit, s slot, _ config.FieldMeta) control { return newFontEditor(k, s, false) })
	monoFontRow = withEditor(func(k *kit, s slot, _ config.FieldMeta) control { return newFontEditor(k, s, true) })
)

// Icon picker geometry: the preview size (editors/icon's
// PREVIEW_SIZE) and the grid the popover shows at once, columns by
// rows of cells.
const (
	iconPreviewPx     = 24
	iconPickerColumns = 8
	iconPickerRows    = 8
)

// iconCellPadding insets a grid cell's preview: half the theme's
// padding.
func iconCellPadding() int { return widget.Current().Padding / 2 }

// iconCellPx is a grid cell's side: the preview inside its padding.
func iconCellPx() int { return iconPreviewPx + 2*iconCellPadding() }

// iconCellGap is the gap between the grid's cells.
func iconCellGap() int { return widget.Current().Spacing / 2 }

// iconGridExtent is the length n cells take along an axis.
func iconGridExtent(n int) int { return n*iconCellPx() + (n-1)*iconCellGap() }

// iconPickerWidth is the trigger's width, and so the popover's: a full
// line of the grid inside the popover's padding.
func iconPickerWidth() int { return iconGridExtent(iconPickerColumns) + 2*widget.Current().Padding }

// iconNames lists the icons the picker offers: every one the icon
// theme's search path resolves from lys-shell's directory (a seam for
// tests; IconManager::list). A root that cannot be read is logged; the
// others' icons are still offered.
var iconNames = func() []string {
	names, err := icons.List(widget.IconSearchPaths())
	if err != nil {
		log.Printf("settings: icons: %v", err)
	}
	return names
}

// iconEditor is icon_picker_widget: a trigger showing the icon and its
// name that opens the searchable grid of the icons beneath it, as wide
// as the trigger.
type iconEditor struct {
	*widget.Box
	k      *kit
	slot   slot
	btn    *widget.Button
	icon   *widget.Icon
	label  *widget.Label
	picker *iconPicker
}

func newIconEditor(k *kit, s slot) *iconEditor {
	c := &iconEditor{Box: widget.NewBox(widget.Row, 0, 0), k: k, slot: s}
	c.SetElement("menubutton")
	c.AddClass("icon-picker-trigger")
	c.icon = k.icon("ld-image-symbolic")
	c.label = k.label("")
	c.label.SetEllipsize(widget.EllipsizeEnd)
	face := gapBox(widget.Row)
	face.AppendAligned(c.icon, false, widget.AlignCenter)
	face.AppendAligned(c.label, true, widget.AlignCenter)
	c.btn = k.button(face, c.open, css.Outline)
	w := iconPickerWidth()
	c.btn.SetSizeRequest(w, 0)
	c.btn.SetMaximumSize(w, 0)
	c.Append(c.btn, false)
	c.refresh()
	return c
}

// refresh is update_display: the icon and its name, or the image
// placeholder and "None".
func (c *iconEditor) refresh() {
	name := c.slot.text()
	if name == "" {
		c.icon.SetThemeName("ld-image-symbolic")
		c.label.SetText(i18n.Settings().Get("settings-icon-none"))
		return
	}
	c.icon.SetThemeName(name)
	c.label.SetText(name)
}

func (c *iconEditor) open() {
	if c.k.pickers == nil {
		return
	}
	openPicker(c.k, c.btn, func(name string) {
		_ = c.slot.set(name)
		c.refresh()
	}, func(pick func(string)) (*widget.Box, widget.Widget) {
		c.picker = newIconPicker(c.k, iconNames(), c.slot.text(), c.btn.Bounds().W, pick)
		return c.picker.root, c.picker.search
	})
}

// iconPicker is the icon popover: the search entry and the clear
// button in one row over a scrolling grid of the icons whose names
// contain the query, the current one selected. A click on an icon
// picks it, Enter in the search the typed name, the clear button none.
type iconPicker struct {
	root     *widget.Box
	contents *widget.Box
	search   *widget.Entry
	clear    *widget.Button
	grid     *widget.GridView
	all      []string
	shown    []string
	current  string
	pick     func(string)
}

// newIconPicker builds the picker over names, its contents width wide
// (its trigger's width, the popover's border falling just outside it
// as a dropdown's does), with current selected.
func newIconPicker(k *kit, names []string, current string, width int, pick func(string)) *iconPicker {
	p := &iconPicker{all: names, current: current, pick: pick}
	root, contents := pickerFrame("icon-picker-popover")
	contents.SetSizeRequest(width, 0)
	contents.SetMaximumSize(width, 0)
	p.root, p.contents = root, contents
	p.search = k.searchField("settings-icon-search", p.filter)
	p.search.OnActivate = func(text string) {
		if text != "" {
			p.pick(text)
		}
	}
	p.clear = k.button(k.icon("ld-x-circle-symbolic"), func() { p.pick("") }, "icon-picker-clear", css.Ghost, css.Icon)
	p.clear.SetTooltip(i18n.Settings().Get("settings-icon-clear"))
	searchRow := gapBox(widget.Row)
	searchRow.AppendAligned(p.search, true, widget.AlignCenter)
	searchRow.AppendAligned(p.clear, false, widget.AlignCenter)
	contents.Append(searchRow, false)
	p.grid = newIconGrid(iconCells{p})
	p.grid.OnActivate = func(i int) { p.pick(p.shown[i]) }
	contents.Append(p.grid, true)
	p.filter("")
	return p
}

// newIconGrid is the picker's virtualized grid over model: only the
// cells in view exist, the full column count of them per line, the cell
// gap apart, at most the picker's rows tall before it scrolls.
func newIconGrid(model iconCells) *widget.GridView {
	gap := iconCellGap()
	g := widget.NewGridView[widget.Widget](model, iconCellPx(), iconCellPx())
	g.AddClass("icon-picker-grid")
	g.SetColumnSpacing(gap)
	g.SetRowSpacing(gap)
	g.SetMinColumns(iconPickerColumns)
	g.SetMaxColumns(iconPickerColumns)
	g.SetMaxHeight(iconGridExtent(iconPickerRows))
	g.SetSingleClickActivate(true)
	return g
}

// filter shows the icons matching the query, the grid scrolled back to
// its top and the current icon selected and scrolled into view.
func (p *iconPicker) filter(query string) {
	p.shown = matching(p.all, query)
	p.grid.Reset()
	p.grid.Select(slices.Index(p.shown, p.current))
}

// iconCells is the grid's model over the shown icons.
type iconCells struct{ p *iconPicker }

func (m iconCells) Len() int { return len(m.p.shown) }

func (m iconCells) Row(i int) widget.Widget { return iconCell(m.p.shown[i]) }

// iconCell is a grid cell: the icon's preview, centred inside the
// cell's padding, with its name as the tooltip (a seam for tests).
var iconCell = func(name string) widget.Widget {
	cell := widget.NewBox(widget.Row, 0, iconCellPadding())
	cell.SetTooltip(name)
	cell.AppendAligned(widget.NewThemeIcon(name, iconPreviewPx), true, widget.AlignCenter)
	return cell
}

// iconRow is icon::icon for a row.
var iconRow = withEditor(func(k *kit, s slot, _ config.FieldMeta) control {
	return newIconEditor(k, s)
})

// Theme selector sizes (theme_selector: BASE_MAX_HEIGHT at the
// styling scale).
const themeListPx = 384

// paletteKeys are the [styling.palette] keys a theme sets, in the
// palette's order.
var paletteKeys = []string{"bg", "surface", "elevated", "fg", "fg-muted", "primary", "red", "yellow", "green", "blue"}

// availableThemes lists the selectable themes (styling.available):
// the built-ins and the config's themes/ files (a seam for tests).
var availableThemes = func(s store) []config.ThemeEntry {
	themes, skipped := config.LoadThemes(filepath.Join(s.svc.Dir(), "themes"))
	for _, err := range skipped {
		log.Printf("settings: theme: %v", err)
	}
	return themes
}

// themeSelector is ThemeSelectorControl: a button opening the preset
// list (each theme's swatches and name); applying one names it the
// palette's base theme and writes its colors, the badge showing it.
type themeSelector struct {
	*widget.Box
	k      *kit
	btn    *widget.Button
	badge  *widget.Badge
	themes []config.ThemeEntry
	picker *searchPicker
}

func newThemeSelector(k *kit) *themeSelector {
	t := i18n.Settings()
	c := &themeSelector{Box: widget.NewBox(widget.Row, 0, 0), k: k}
	content := gapBox(widget.Row)
	content.AppendAligned(k.icon("ld-palette-symbolic", "theme-preset-button-icon"), false, widget.AlignCenter)
	content.AppendAligned(k.label(t.Get("settings-theme-preset-apply"), "theme-preset-button-label"), false, widget.AlignCenter)
	content.AppendAligned(k.icon("ld-chevron-down-symbolic", "theme-preset-button-chevron", css.DimLabel), false, widget.AlignCenter)
	c.btn = k.button(content, c.open, "theme-preset-button", css.Outline)
	c.Append(c.btn, false)
	c.badge = k.badge("")
	c.refresh()
	return c
}

func (c *themeSelector) dirtyBadge() *widget.Badge { return c.badge }

// refresh shows the base theme on the badge.
func (c *themeSelector) refresh() {
	name, _ := c.k.store.value("styling.palette_base_theme").(string)
	c.badge.SetText(name)
	c.badge.SetVisible(name != "")
}

func (c *themeSelector) open() {
	if c.k.pickers == nil {
		return
	}
	c.themes = availableThemes(c.k.store)
	names := make([]string, len(c.themes))
	for i, th := range c.themes {
		names[i] = th.Name
	}
	scale, _ := c.k.store.value("styling.scale").(float64)
	c.picker = openSearchPicker(c.k, c.btn, names, pickerSpec{
		class: "theme-preset-popover", placeholder: "settings-theme-preset", maxH: int(math.Round(themeListPx * max(scale, 0.25))),
		row: func(k *kit, name string) widget.Widget { return c.row(name) },
	}, c.apply)
}

// row is build_theme_row: the swatches (bg, primary, red, yellow,
// green, blue) and the name, ellipsized past the row's width.
func (c *themeSelector) row(name string) widget.Widget {
	content := gapBox(widget.Row)
	swatches := widget.NewBox(widget.Row, widget.Current().Spacing/2, 0)
	swatches.AddClass("theme-preset-swatches")
	if th, ok := c.theme(name); ok {
		p := th.Palette
		for _, hex := range []string{p.Bg, p.Primary, p.Red, p.Yellow, p.Green, p.Blue} {
			col, ok := styling.ParseHex(hex)
			if !ok {
				log.Printf("settings: theme %s: %q is no hex color", name, hex)
				continue
			}
			swatches.AppendAligned(widget.NewColorSwatch(col), false, widget.AlignCenter)
		}
	}
	content.AppendAligned(swatches, false, widget.AlignCenter)
	label := c.k.label(name, "theme-preset-name")
	label.SetEllipsize(widget.EllipsizeEnd)
	content.AppendAligned(label, true, widget.AlignCenter)
	return content
}

func (c *themeSelector) theme(name string) (config.ThemeEntry, bool) {
	for _, th := range c.themes {
		if th.Name == name {
			return th, true
		}
	}
	return config.ThemeEntry{}, false
}

// apply is on_apply: the base theme, then each valid palette color.
func (c *themeSelector) apply(name string) {
	th, ok := c.theme(name)
	if !ok {
		return
	}
	_ = c.k.store.set("styling.palette_base_theme", name)
	p := th.Palette
	for i, hex := range []string{p.Bg, p.Surface, p.Elevated, p.Fg, p.FgMuted, p.Primary, p.Red, p.Yellow, p.Green, p.Blue} {
		if _, err := config.ParseHexColor(hex); err == nil {
			_ = c.k.store.set("styling.palette."+paletteKeys[i], hex)
		}
	}
	c.refresh()
}

// themePreset is theme_selector's row: an action row under the preset
// key.
func themePreset() rowSpec {
	return rowSpec{
		path: "styling.palette_base_theme", key: "settings-theme-preset", action: true,
		editor: func(k *kit, _ slot, _ config.FieldMeta) control { return newThemeSelector(k) },
	}
}
