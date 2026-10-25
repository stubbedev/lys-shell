package bar

import (
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"
)

// ringVariants is every variant class lys-ui's ring recolors by.
var ringVariants = []string{css.Success, css.Warning, css.Error}

// statRing is a dashboard gauge: lys-ui's ProgressRing with its value
// written in the middle, recolored by a threshold variant.
type statRing struct {
	ring    *widget.ProgressRing
	label   *widget.Label
	variant string
}

func newStatRing(label *widget.Label) *statRing {
	r := &statRing{ring: widget.NewProgressRing(0), label: label}
	r.ring.AddClass("stat-ring")
	label.AddClass(css.Heading)
	r.ring.SetChild(label)
	return r
}

// set shows fraction (clamped by the ring), its text and the variant
// class ("" for the accent).
func (r *statRing) set(fraction float64, text, variant string) {
	if variant != r.variant {
		r.ring.RemoveClass(ringVariants...)
		if variant != "" {
			r.ring.AddClass(variant)
		}
		r.variant = variant
	}
	r.label.SetText(text)
	r.ring.SetValue(fraction)
}
