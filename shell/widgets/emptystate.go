package widgets

import (
	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
)

// EmptyState is the empty-state template on lys-ui's StatusPage: the
// icon on its muted tile over the title and the wrapped description,
// centered in whatever page holds it.
func EmptyState(font render.Font, px float64, icon, title, description string) *widget.StatusPage {
	page, _ := EmptyStateIcon(font, px, icon, title, description)
	return page
}

// EmptyStateIcon is EmptyState that also hands back its icon, for
// pages whose glyph follows state.
func EmptyStateIcon(font render.Font, px float64, icon, title, description string) (*widget.StatusPage, *widget.Icon) {
	page := widget.NewStatusPage(font, "", title, description)
	page.AddClass("empty-state")
	glyph := widget.NewThemeIcon(icon, int(px*1.7))
	page.SetIcon(glyph)
	return page, glyph
}
