package settings

import (
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
)

// newSidebar is Sidebar on lys-ui's sidebar beside content: the title,
// one collapsible group per nav section with active open, and the
// reset-all button. Picking a page reports its id to onNavigate; the
// footer button runs onResetAll.
func newSidebar(k *kit, sections []navSection, active string, content widget.Widget, onNavigate func(id string), onResetAll func()) *widget.Sidebar {
	t := i18n.Settings()
	s := widget.NewSidebar(k.face, content)
	s.SetCollapse(widget.SidebarNone)
	s.SetNarrowWidth(0)

	th := widget.Current()
	header := widget.NewBox(widget.Row, th.Spacing, th.Padding)
	header.AddClass("sidebar-header")
	header.AppendAligned(k.icon("ld-settings-symbolic", "sidebar-icon"), false, widget.AlignCenter)
	header.AppendAligned(k.label(t.Get("settings-title"), "sidebar-title", css.LargeText), false, widget.AlignCenter)
	s.SetHeader(header)

	for _, sec := range sections {
		items := make([]widget.SidebarItem, len(sec.pages))
		for i, p := range sec.pages {
			id := p.id
			items[i] = widget.SidebarItem{
				Label:      t.Get(p.navKey),
				ID:         id,
				Icon:       k.icon(p.icon, "sidebar-item-icon"),
				Active:     id == active,
				OnActivate: func() { onNavigate(id) },
			}
		}
		s.AddCollapsibleGroup(t.Get(sec.key), true, items...)
	}

	reset := widget.NewBox(widget.Row, th.Spacing, 0)
	reset.AppendAligned(k.icon("ld-rotate-ccw-symbolic", "sidebar-reset-icon"), false, widget.AlignCenter)
	reset.AppendAligned(k.label(t.Get("settings-reset-all"), "sidebar-reset-label"), true, widget.AlignCenter)
	s.SetFooter(k.button(reset, onResetAll, css.Ghost, "sidebar-reset-all"))
	return s
}
