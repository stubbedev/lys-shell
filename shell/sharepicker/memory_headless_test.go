package sharepicker

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
)

// A dismissed picker gives its pixels back: the shared memory its
// surface was drawn in stops being resident, so a share leaves no
// picker-sized trace for the rest of the session.
func TestADismissedPickerReleasesItsPixels(t *testing.T) {
	rig := keyPicker(t, config.SharePickerWindows, Request{Sources: AllSources})
	shown := apptest.ShmResident(t)
	bounds := apptest.On(rig.a, func() int { b := rig.p.root.(widget.Boundser).Bounds(); return b.W * b.H * 4 })
	if bounds == 0 || shown < bounds {
		t.Fatalf("the shown picker holds %d resident bytes, want at least its %d-byte surface", shown, bounds)
	}
	rig.p.Dismiss("/r/keys")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		left := apptest.ShmResident(t)
		if shown-left >= bounds {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after dismissing, %d of the shown %d resident bytes stay, want at least the picker's %d gone", left, shown, bounds)
		}
	}
}
