package recorder

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/internal/swaytest"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// fakePicker answers every pick with reply, or holds it until the
// context ends when block is set.
type fakePicker struct {
	reply string
	err   error
	block bool

	mu        sync.Mutex
	asked     []sharepicker.Request
	callers   []portaldialogs.Caller
	dismissed []string
}

func (p *fakePicker) Pick(ctx context.Context, c portaldialogs.Caller, r sharepicker.Request) (string, error) {
	p.mu.Lock()
	p.asked = append(p.asked, r)
	p.callers = append(p.callers, c)
	p.mu.Unlock()
	if p.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return p.reply, p.err
}

func (p *fakePicker) Dismiss(_ context.Context, handle string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dismissed = append(p.dismissed, handle)
	return nil
}

// noLogical knows no output's logical size.
func noLogical(string) (image.Point, bool) { return image.Point{}, false }

// The recorder asks the picker for an output or a region, offering to
// remember the choice when it has somewhere to keep it, and takes one
// back; a cancel, a window, several sources or a malformed reply are
// errors, not guesses.
func TestPickAsksForOneOutputOrRegion(t *testing.T) {
	for reply, want := range map[string]struct {
		target   sharepicker.Target
		remember bool
		err      string
	}{
		"r/screen:DP-1":              {target: sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "DP-1"}, remember: true},
		"/screen:HDMI-A-1":           {target: sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "HDMI-A-1"}},
		"r/region:DP-1@5,6,70,80":    {target: sharepicker.Target{Kind: sharepicker.TargetRegion, Name: "DP-1", X: 5, Y: 6, Width: 70, Height: 80}, remember: true},
		"":                           {err: ErrPickerCancelled.Error()},
		"/window:firefox":            {err: `replied "/window:firefox", not one output or region`},
		"/screen:A;screen:B":         {err: "not one output or region"},
		"/screen:A;region:A@0,0,1,1": {err: "not one output or region"},
		"garbage":                    {err: "not one output or region"},
	} {
		p := &fakePicker{reply: reply}
		e := NewGPUEngine(p, nil, noLogical, t.TempDir())
		target, remember, err := e.pick(context.Background())
		switch {
		case want.err != "":
			if err == nil || !strings.Contains(err.Error(), want.err) {
				t.Errorf("%q: %v, want %q", reply, err, want.err)
			}
		case err != nil || target != want.target || remember != want.remember:
			t.Errorf("%q: %+v remember %v (%v), want %+v remember %v", reply, target, remember, err, want.target, want.remember)
		}
		if r := p.asked[0]; r.Sources != sharepicker.SourceOutputs|sharepicker.SourceRegions || !r.AllowToken || r.Multiple {
			t.Errorf("%q: asked %+v, want one output or region with the remember box", reply, r)
		}
	}
	p := &fakePicker{reply: "r/screen:DP-1"}
	if _, _, err := NewGPUEngine(p, nil, noLogical, "").pick(context.Background()); err != nil || p.asked[0].AllowToken {
		t.Errorf("no state dir: asked %+v (%v), want no remember box", p.asked[0], err)
	}
	p = &fakePicker{err: errors.New("org.freedesktop.DBus.Error.ServiceUnknown")}
	if _, _, err := NewGPUEngine(p, nil, noLogical, "").pick(context.Background()); err == nil || !strings.Contains(err.Error(), "share picker: org.freedesktop.DBus.Error.ServiceUnknown") {
		t.Errorf("no picker: %v", err)
	}
}

// A start cancelled while the picker shows takes that picker down, by
// the handle it was asked under, and is the context's error.
func TestCancelledPickDismissesThePicker(t *testing.T) {
	p := &fakePicker{block: true}
	e := NewGPUEngine(p, nil, noLogical, "")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.OpenSession(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancelled: %v, want the context's error", err)
	}
	if len(p.dismissed) != 1 || p.dismissed[0] != p.callers[0].Handle || p.callers[0].Handle == "" {
		t.Errorf("dismissed %q for callers %+v, want the pick's handle", p.dismissed, p.callers)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := e.OpenSession(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a second cancelled pick: %v", err)
	}
	if p.callers[0].Handle == p.callers[1].Handle {
		t.Error("two picks shared a handle")
	}
}

// A remembered source whose output is gone is forgotten and the picker
// asked; a remembered file that names neither an output nor a region is
// forgotten too. On a compositor that copies outputs only into shared
// memory, the picked output fails with that reason.
func TestRememberedSourceGoneAsksAgain(t *testing.T) {
	swaytest.Start(t)
	dir := t.TempDir()
	saved := filepath.Join(dir, sourceFile)
	for _, content := range []string{"screen:NOPE-9\n", "region:NOPE-9@0,0,10,10\n", "window:firefox\n"} {
		if err := os.WriteFile(saved, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		p := &fakePicker{reply: "r/screen:HEADLESS-1"}
		_, err := NewGPUEngine(p, nil, noLogical, dir).OpenSession(context.Background(), false)
		if len(p.asked) != 1 {
			t.Errorf("%q: asked %d times, want the picker once", content, len(p.asked))
		}
		if _, serr := os.Stat(saved); !errors.Is(serr, os.ErrNotExist) {
			t.Errorf("%q: still remembered: %v", content, serr)
		}
		if err == nil || !strings.Contains(err.Error(), "output HEADLESS-1 offers no screencopy into GPU memory") {
			t.Errorf("%q: %v, want pixman's sway to offer no GPU capture", content, err)
		}
	}
	p := &fakePicker{reply: "/screen:NOPE-1"}
	if _, err := NewGPUEngine(p, nil, noLogical, dir).OpenSession(context.Background(), false); !errors.Is(err, errOutputGone) {
		t.Errorf("a picked output that does not exist: %v, want errOutputGone", err)
	}
}

// A region is measured in its output's logical size: an output with no
// known size is gone, and a region outside it is an error naming both.
func TestTargetSource(t *testing.T) {
	logical := func(name string) (image.Point, bool) { return image.Pt(640, 360), name == "DP-1" }
	buffer := image.Pt(1280, 720)
	whole, err := targetSource(sharepicker.Target{Kind: sharepicker.TargetOutput, Name: "DP-1"}, noLogical, buffer, capture.TransformNormal)
	if err != nil || whole != image.Rect(0, 0, 1280, 720) {
		t.Errorf("an output: %v (%v), want the whole buffer", whole, err)
	}
	region := sharepicker.Target{Kind: sharepicker.TargetRegion, Name: "DP-1", X: 10, Y: 20, Width: 100, Height: 50}
	if got, err := targetSource(region, logical, buffer, capture.TransformNormal); err != nil || got != image.Rect(20, 40, 220, 140) {
		t.Errorf("a region at scale 2: %v (%v)", got, err)
	}
	region.Name = "DP-2"
	if _, err := targetSource(region, logical, buffer, capture.TransformNormal); !errors.Is(err, errOutputGone) {
		t.Errorf("an unknown output's region: %v, want errOutputGone", err)
	}
	region.Name, region.X = "DP-1", 600
	if _, err := targetSource(region, logical, buffer, capture.TransformNormal); err == nil || !strings.Contains(err.Error(), "not inside the 640x360 output") {
		t.Errorf("a region past the edge: %v", err)
	}
}
