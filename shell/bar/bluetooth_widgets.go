package bar

import (
	"github.com/stubbedev/gelm/widget"
)

// btDeviceRow is one device row (device_item): the row box carries the
// classes and the stylesheet paints it — the available :hover, the
// padding, the first-child radii. The whole row is the click target
// (SetOnClickWithin; the action buttons are clicking descendants and
// keep their own), and hovering swaps the status label for the actions
// (SetOnHoverWithin; the buttons are descendants, so moving onto one
// holds the swap).
type btDeviceRow struct {
	*widget.Box
	// status and actions are the hover stack's two pages.
	slot   *widget.Stack
	status *widget.Label
	// statusShown is status_visible: connected, paired, or pending.
	statusShown bool
	actions     *widget.Box
	// hoverSwaps is set for my devices: only they carry the swap.
	hoverSwaps bool
	pending    bool

	onClick func()
}

// syncSlot shows the actions while hovered and idle, else the status
// (set_visible_child_name).
func (r *btDeviceRow) syncSlot() {
	if r.actions == nil {
		return
	}
	r.status.SetVisible(r.statusShown)
	if r.hoverSwaps && r.HoverWithin() && !r.pending {
		r.slot.Show(btSlotActions)
	} else {
		r.slot.Show(btSlotStatus)
	}
}

// The hover stack's pages.
const (
	btSlotStatus  = "status"
	btSlotActions = "actions"
)
