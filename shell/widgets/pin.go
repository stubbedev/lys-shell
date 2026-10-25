package widgets

import (
	"fmt"

	"github.com/stubbedev/lys-ui/widget"
)

// sizer is a widget with a size request and a maximum size; every
// lys-ui widget, and every type built on widget.Base, has them.
type sizer interface {
	SetSizeRequest(w, h int)
	SetMaximumSize(w, h int)
}

// Pin holds w at exactly width x height on each non-zero axis, its
// size request flooring and its maximum capping it, so content that
// measures larger is clipped, and returns it. A zero axis keeps w's
// natural extent. Pinning again re-pins it (a column dragged wider).
func Pin(w widget.Widget, width, height int) widget.Widget {
	s, ok := w.(sizer)
	if !ok {
		panic(fmt.Sprintf("widgets.Pin: %T has no layout properties", w))
	}
	s.SetSizeRequest(width, height)
	s.SetMaximumSize(width, height)
	return w
}
