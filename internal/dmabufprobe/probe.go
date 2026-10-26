// Package dmabufprobe picks the DRM modifier a screencopy into GPU
// memory uses: the modifiers the compositor imports, ordered against
// the ones the device renders and exports, tried one at a time until a
// buffer allocates, imports and takes a frame. The ScreenCast portal
// and the recorder both allocate this way.
package dmabufprobe

import (
	"errors"
	"fmt"
	"slices"

	"github.com/stubbedev/lys-lib/dmabuf"
)

// Layout is one modifier to probe and how its buffers are allocated:
// for rendering into, when the device renders the modifier, or only for
// transfer and sampling, when it only exports it (linear on NVIDIA).
type Layout struct {
	Modifier uint64
	Render   bool
}

// Layouts orders the modifiers to probe among the ones the compositor
// imports, in its order: linear first, the layout every consumer and
// driver takes, then the ones the device renders in, then the ones it
// only exports. A modifier the device neither renders nor exports is
// left out.
func Layouts(compositor, rendered, exported []uint64) []Layout {
	var linear, drawn, exportOnly []Layout
	for _, m := range compositor {
		l := Layout{Modifier: m, Render: slices.Contains(rendered, m)}
		switch {
		case !l.Render && !slices.Contains(exported, m):
		case m == dmabuf.ModifierLinear:
			linear = append(linear, l)
		case l.Render:
			drawn = append(drawn, l)
		default:
			exportOnly = append(exportOnly, l)
		}
	}
	return slices.Concat(linear, drawn, exportOnly)
}

// ErrNoLayout is a probe that found no modifier to try.
var ErrNoLayout = errors.New("dmabufprobe: the compositor and the device share no modifier")

// First tries layouts in order, one at a time, and returns the first
// one try accepts with its result. One at a time, because a compositor
// may advertise a tiling it then fails to import (sway's Vulkan
// renderer on NVIDIA), and a set would leave the pick to the driver.
// Declining every layout names why each one failed.
func First[T any](layouts []Layout, try func(Layout) (T, error)) (T, Layout, error) {
	var zero T
	if len(layouts) == 0 {
		return zero, Layout{}, ErrNoLayout
	}
	declined := make([]error, 0, len(layouts))
	for _, l := range layouts {
		v, err := try(l)
		if err == nil {
			return v, l, nil
		}
		declined = append(declined, fmt.Errorf("modifier %#x: %w", l.Modifier, err))
	}
	return zero, Layout{}, errors.Join(declined...)
}
