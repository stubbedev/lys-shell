package sharepicker

import (
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/apptest"
)

// A dismissed picker gives its pixels back: once the compositor has
// presented it, the shared memory it was drawn in is resident, and
// after the dismissal no more stays resident than before it opened
// but the one page lys-ui's pool keeps where two freed buffers met
// (it punches each slot's whole pages, stubbedev/lys-ui#433), so a
// share leaves no picker-sized trace for the rest of the session. The
// application has no other surface whose buffers could come and go
// meanwhile.
func TestADismissedPickerReleasesItsPixels(t *testing.T) {
	a := apptest.Run(t)
	p := headlessPicker(t, a, config.SharePickerWindows)
	presented := make(chan struct{})
	p.presented = sync.OnceFunc(func() { close(presented) })
	t.Cleanup(func() { p.Dismiss("/r/memory") })
	before := apptest.ShmResident(t)
	answers := make(chan string, 1)
	go func() { answers <- p.Pick(portaldialogs.Caller{Handle: "/r/memory"}, Request{Sources: AllSources}) }()
	apptest.Answer(t, "the picker presented", presented)
	shown := apptest.ShmResident(t)
	if shown <= before {
		t.Fatalf("the presented picker holds %d resident bytes, no more than the %d before it opened", shown, before)
	}
	p.Dismiss("/r/memory")
	apptest.Answer(t, "the dismissed picker's answer", answers)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		left := apptest.ShmResident(t)
		if left <= before+os.Getpagesize() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after dismissing, %d resident bytes stay, want at most the %d before the picker opened and a page (%d while shown)", left, before, shown)
		}
	}
}
