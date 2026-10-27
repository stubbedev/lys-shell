package widgets

import (
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/styling"
)

// TextPx is role's size in pixels at scale over the desktop's body
// size, the rem the stylesheet's --text-* tokens resolve against.
func TextPx(role styling.TextRole, scale float64) float64 {
	return role.Px(widget.RootFontSize(), scale)
}
