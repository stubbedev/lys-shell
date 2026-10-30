package portal

import (
	"fmt"
	"hash/fnv"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

// windowShareSource is the shared-memory source a window share streams
// from, on a connection of the test's own.
func windowShareSource(t *testing.T, id string) *snapshotSource {
	t.Helper()
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	tl, ok := findToplevel(c, id)
	if !ok {
		t.Fatal("the shared window is gone")
	}
	grab := toplevelGrab(c, tl, false)
	first, err := grab(nil)
	if err != nil {
		t.Fatal(err)
	}
	return newSnapshotSource(grab, first, newVanishWatch(toplevelGone(c, id)))
}

// distinctFrames fills from src for d and counts the distinct frames.
func distinctFrames(t *testing.T, src *snapshotSource, d time.Duration) int {
	t.Helper()
	_, height, stride := src.Size()
	dst := make([]byte, int(stride)*int(height))
	seen := map[uint64]bool{}
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(time.Second / 30) {
		if _, ok := src.Fill(dst); !ok {
			continue
		}
		h := fnv.New64a()
		_, _ = h.Write(dst)
		seen[h.Sum64()] = true
	}
	return len(seen)
}

// animate redraws label every frame until the test ends.
func animate(t *testing.T, a *app.Application, label *widget.Label) {
	t.Helper()
	stop := make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		<-done
	})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case <-time.After(time.Second / 60):
			}
			a.Invoke(func() { label.SetText(fmt.Sprintf("frame %d", i)) })
		}
	}()
}

// A window share follows the window from its first second: an
// animating window streams changing frames at once, while a still one
// streams the same frame, so the count is the window's own content.
func TestWindowShareFollowsTheWindowFromTheStart(t *testing.T) {
	a := apptest.Run(t)
	label := widget.NewLabel(treetest.Font(t), 14, "shared", render.RGBA(255, 255, 255, 255))
	id := sharedWindow(t, a, label)

	still := windowShareSource(t, id)
	if n := distinctFrames(t, still, time.Second/2); n != 1 {
		t.Errorf("a still window streamed %d distinct frames, want 1", n)
	}

	animate(t, a, label)
	if n := distinctFrames(t, windowShareSource(t, id), time.Second); n < 5 {
		t.Errorf("an animating window streamed %d distinct frames in its first second, want at least 5", n)
	}
}
