package settings

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
)

// The bar layout editor (editors/bar_layout): a card per monitor
// layout, its three zones holding module and group chips that drag
// between zones and cards, add from a searchable module picker, and
// write the whole layout list on every change.

// Layout zones, in the card's order.
const (
	zoneLeft = iota
	zoneCenter
	zoneRight
	zoneCount
)

// zoneKeys are the zones' labels.
var zoneKeys = [zoneCount]string{"settings-layout-zone-left", "settings-layout-zone-center", "settings-layout-zone-right"}

// Group name entry width (chip/helpers: GROUP_NAME_WIDTH_CHARS,
// GROUP_NAME_MAX_WIDTH_CHARS).
const (
	groupNameChars    = 6
	groupNameMaxChars = 10
)

// modulePickerListPx caps the module picker's list.
const modulePickerListPx = 300

// layoutEditor is BarLayoutControl over bar.layout.
type layoutEditor struct {
	*widget.Box
	k       *kit
	slot    slot
	list    *widget.Box
	layouts []config.BarLayout
	// zones are the cards' chip boxes and the zone each one shows,
	// rebuilt with the cards.
	zones map[*widget.ChipBox]zoneRef
}

// zoneRef names layout card's zone.
type zoneRef struct{ card, zone int }

func newLayoutEditor(k *kit, s slot) *layoutEditor {
	c := &layoutEditor{Box: widget.NewBox(widget.Column, 0, 0), k: k, slot: s}
	c.AddClass("bar-layout-control")
	c.list = widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	c.list.AddClass("bar-layout-list")
	c.Append(c.list, false)
	add := listButton(k, "ld-plus-symbolic", "settings-layout-add", "list-control-add", func() {
		c.layouts = append(c.layouts, config.BarLayout{Show: true})
		c.commit()
		c.rebuild()
	})
	c.AppendAligned(add, false, widget.AlignStart)
	c.layouts = c.stored()
	c.rebuild()
	return c
}

// stored is the configured layout list, copied for editing.
func (c *layoutEditor) stored() []config.BarLayout {
	out := slices.Clone(c.k.store.svc.Config().Bar.Layout)
	for i := range out {
		out[i].Left = cloneItems(out[i].Left)
		out[i].Center = cloneItems(out[i].Center)
		out[i].Right = cloneItems(out[i].Right)
	}
	return out
}

func cloneItems(items []config.BarItem) []config.BarItem {
	out := slices.Clone(items)
	for i := range out {
		if g := out[i].Group; g != nil {
			out[i].Group = &config.BarGroup{Name: g.Name, Modules: slices.Clone(g.Modules)}
		}
	}
	return out
}

// commit writes the layouts.
func (c *layoutEditor) commit() { _ = c.slot.set(config.Encode(c.layouts)) }

// refresh (on_refresh) rebuilds when the stored layouts moved away
// from the cards.
func (c *layoutEditor) refresh() {
	if reflect.DeepEqual(config.Encode(c.layouts), config.Plain(c.slot.get())) {
		return
	}
	c.layouts = c.stored()
	c.rebuild()
}

// zone is layout i's zone z.
func (c *layoutEditor) zone(i, z int) *[]config.BarItem {
	l := &c.layouts[i]
	switch z {
	case zoneLeft:
		return &l.Left
	case zoneCenter:
		return &l.Center
	}
	return &l.Right
}

func (c *layoutEditor) rebuild() {
	c.list.Clear()
	c.zones = map[*widget.ChipBox]zoneRef{}
	for i := range c.layouts {
		c.list.Append(c.card(i), false)
	}
}

// card is LayoutCard: the monitor and extends entries, the show switch
// and the delete button over the zones (or the hidden note).
func (c *layoutEditor) card(i int) widget.Widget {
	t := i18n.Settings()
	l := &c.layouts[i]
	th := widget.Current()
	band := render.Insets{Top: th.Padding, Right: 2 * th.Padding, Bottom: th.Padding, Left: 2 * th.Padding}
	root := widget.NewBox(widget.Column, 0, 0)
	root.AddClass(css.Card, "layout-card")
	header := widget.NewBox(widget.Row, th.Spacing, 0)
	header.SetPadding(band)
	header.AddClass("layout-card-header")
	monitor := c.k.entry()
	monitor.AddClass("layout-monitor-input")
	monitor.SetPlaceholder("DP-1")
	monitor.SetText(l.Monitor)
	monitor.OnChanged = func(text string) {
		if c.layouts[i].Monitor != text {
			c.layouts[i].Monitor = text
			c.commit()
		}
	}
	extends := c.k.entry()
	extends.AddClass("layout-extends-input")
	extends.SetPlaceholder(t.Get("settings-layout-extends-none"))
	if l.Extends != nil {
		extends.SetText(*l.Extends)
	}
	extends.OnChanged = func(text string) {
		var v *string
		if text != "" {
			v = &text
		}
		if !reflect.DeepEqual(c.layouts[i].Extends, v) {
			c.layouts[i].Extends = v
			c.commit()
		}
	}
	show := widget.NewSwitch(l.Show)
	show.AddClass("layout-show-toggle")
	show.OnChanged = func(on bool) {
		if c.layouts[i].Show != on {
			c.layouts[i].Show = on
			c.commit()
			c.rebuild()
		}
	}
	remove := c.k.button(c.k.icon("ld-trash-2-symbolic"), func() {
		c.layouts = slices.Delete(c.layouts, i, i+1)
		c.commit()
		c.rebuild()
	}, css.Ghost, css.Icon)
	header.AppendAligned(c.k.label(t.Get("settings-layout-monitor-label"), "layout-extends-label", css.DimLabel), false, widget.AlignCenter)
	header.AppendAligned(monitor, false, widget.AlignCenter)
	header.AppendAligned(c.k.label(t.Get("settings-layout-extends-label"), "layout-extends-label", css.DimLabel), false, widget.AlignCenter)
	header.AppendAligned(extends, false, widget.AlignCenter)
	header.Append(widget.NewSpacer(0, 0), true)
	header.AppendAligned(show, false, widget.AlignCenter)
	header.AppendAligned(remove, false, widget.AlignCenter)
	body := widget.NewBox(widget.Column, th.Spacing, 0)
	body.SetPadding(band)
	body.AddClass("layout-card-body")
	if !l.Show {
		hidden := c.k.label(t.Get("settings-layout-hidden"), "layout-hidden-label", css.DimLabel)
		hidden.SetAlignment(render.AlignCenter)
		body.Append(hidden, false)
	} else {
		labels := widget.NewSizeGroup(widget.SizeGroupHorizontal)
		for z := range zoneCount {
			body.Append(c.zoneRow(zoneRef{i, z}, labels), false)
		}
	}
	root.Append(header, false)
	root.Append(widget.NewSeparator(widget.Horizontal), false)
	root.Append(body, false)
	return root
}

// zoneRow is build_zone_row: the label, sized with the card's other
// zone labels, the zone's chips (the drop target), the add-module
// picker and the add-group button.
func (c *layoutEditor) zoneRow(at zoneRef, labels *widget.SizeGroup) widget.Widget {
	t := i18n.Settings()
	th := widget.Current()
	row := widget.NewBox(widget.Row, th.Spacing, 0)
	row.AddClass("layout-zone")
	chips := widget.NewChipBox()
	c.zones[chips] = at
	for _, item := range *c.zone(at.card, at.zone) {
		chips.Append(c.chip(item))
	}
	chips.OnMoved = func(src *widget.ChipBox, from, to int) { c.moved(src, from, chips, to) }
	frame := widget.NewBox(widget.Row, 0, th.Padding)
	frame.AddClass("layout-zone-items")
	frame.AppendAligned(chips, true, widget.AlignCenter)
	var addModule *widget.Button
	addModule = c.k.button(c.k.icon("ld-plus-symbolic"), func() {
		c.pickModule(addModule, func(m config.BarModule) {
			items := c.zone(at.card, at.zone)
			*items = append(*items, config.BarItem{Module: m})
			c.commit()
			c.rebuild()
		})
	}, "zone-add-btn", css.Outline, css.Icon)
	addModule.SetTooltip(t.Get("settings-layout-add-module"))
	addGroup := c.k.button(c.k.icon("ld-layers-symbolic"), func() {
		items := c.zone(at.card, at.zone)
		*items = append(*items, config.BarItem{Group: &config.BarGroup{Name: t.Get("settings-layout-default-group")}})
		c.commit()
		c.rebuild()
	}, "zone-add-btn", css.Outline, css.Icon)
	addGroup.SetTooltip(t.Get("settings-layout-add-group"))
	zoneLabel := c.k.label(t.Get(zoneKeys[at.zone]), "layout-zone-label", css.CaptionHeading, css.DimLabel)
	row.AppendAligned(labels.Add(zoneLabel), false, widget.AlignCenter)
	row.Append(frame, true)
	row.AppendAligned(addModule, false, widget.AlignCenter)
	row.AppendAligned(addGroup, false, widget.AlignCenter)
	return row
}

// chip is Chip: a module's name, or a group's name entry with its
// modules' chips, either dismissible and dragged by its body.
func (c *layoutEditor) chip(item config.BarItem) *widget.Chip {
	if item.Group == nil {
		chip := widget.NewChip(c.k.face, string(item.Module))
		chip.SetOnDismiss(func() { c.removeItem(chip) })
		return chip
	}
	th := widget.Current()
	chip := widget.NewChip(c.k.face, item.Group.Name)
	content := widget.NewBox(widget.Row, th.Spacing/2, 0)
	name := c.k.entry()
	name.AddClass("group-name-entry")
	name.SetWidthChars(groupNameChars, groupNameMaxChars)
	name.SetText(item.Group.Name)
	name.OnChanged = func(text string) {
		if g := c.group(chip); g.Name != text {
			g.Name = text
			chip.SetText(text)
			c.commit()
		}
	}
	content.AppendAligned(name, false, widget.AlignCenter)
	for m, mod := range item.Group.Modules {
		sub := widget.NewChip(c.k.face, string(mod.Module))
		sub.SetOnDismiss(func() {
			g := c.group(chip)
			g.Modules = slices.Delete(g.Modules, m, m+1)
			c.commit()
			c.rebuild()
		})
		content.AppendAligned(sub, false, widget.AlignCenter)
	}
	var add *widget.Button
	add = c.k.button(c.k.icon("ld-plus-symbolic"), func() {
		c.pickModule(add, func(mod config.BarModule) {
			g := c.group(chip)
			g.Modules = append(g.Modules, config.BarItem{Module: mod})
			c.commit()
			c.rebuild()
		})
	}, "chip-add", css.Ghost, css.Icon, css.Small)
	add.SetTooltip(i18n.Settings().Get("settings-layout-add-module"))
	content.AppendAligned(add, false, widget.AlignCenter)
	chip.SetContent(content)
	chip.SetOnDismiss(func() { c.removeItem(chip) })
	return chip
}

// locate is where chip sits now: its zone and index there. A chip of
// no card's zone is a caller bug.
func (c *layoutEditor) locate(chip *widget.Chip) (zoneRef, int) {
	for box, at := range c.zones {
		if i := box.IndexOf(chip); i >= 0 {
			return at, i
		}
	}
	panic(fmt.Sprintf("settings: layout chip %q is in no zone", chip.Text()))
}

// group is the group a group chip shows.
func (c *layoutEditor) group(chip *widget.Chip) *config.BarGroup {
	at, i := c.locate(chip)
	return (*c.zone(at.card, at.zone))[i].Group
}

// removeItem takes chip's item out of its zone.
func (c *layoutEditor) removeItem(chip *widget.Chip) {
	at, i := c.locate(chip)
	items := c.zone(at.card, at.zone)
	*items = slices.Delete(*items, i, i+1)
	c.commit()
	c.rebuild()
}

// moved is handle_drop after the chip boxes moved the chip: the item
// leaves src's zone at from and enters dst's at to, where the chip now
// is. A box of no card is a caller bug.
func (c *layoutEditor) moved(src *widget.ChipBox, from int, dst *widget.ChipBox, to int) {
	srcAt, ok := c.zones[src]
	if !ok {
		panic("settings: a chip moved in from a box no layout card holds")
	}
	dstAt := c.zones[dst]
	items := c.zone(srcAt.card, srcAt.zone)
	item := (*items)[from]
	*items = slices.Delete(*items, from, from+1)
	items = c.zone(dstAt.card, dstAt.zone)
	*items = slices.Insert(*items, to, item)
	c.commit()
}

// moduleNames are the picker's modules: the built-ins, then a
// custom-<id> per custom module (all_module_names).
func (c *layoutEditor) moduleNames() []string {
	names := make([]string, 0, len(config.BuiltinModules))
	for _, m := range config.BuiltinModules {
		names = append(names, string(m))
	}
	for _, m := range c.k.store.svc.Config().Custom {
		names = append(names, "custom-"+m.Id)
	}
	return names
}

// pickModule opens the module picker (module_picker) beside anchor.
func (c *layoutEditor) pickModule(anchor widget.Widget, pick func(config.BarModule)) *searchPicker {
	if c.k.pickers == nil {
		return nil
	}
	return openSearchPicker(c.k, anchor, c.moduleNames(), pickerSpec{
		class: "module-picker-popover", listClass: "module-picker-list", scrollClass: "module-picker-scroll",
		placeholder: "settings-layout-search", maxH: modulePickerListPx, searchEntry: true,
		row: func(k *kit, name string) widget.Widget {
			return k.label(name, "module-picker-item")
		},
	}, func(name string) {
		var m config.BarModule
		if m.UnmarshalConfig(name) == nil {
			pick(m)
		}
	})
}

// layoutRow is bar_layout for a row.
var layoutRow = func(r *rowSpec) {
	r.fullWidth = true
	r.editor = func(k *kit, s slot, _ config.FieldMeta) control { return newLayoutEditor(k, s) }
}
