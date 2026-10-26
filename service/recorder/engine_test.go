package recorder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/internal/portalreq"
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
	callers   []portalreq.Caller
	dismissed []string
}

func (p *fakePicker) Pick(ctx context.Context, c portalreq.Caller, r sharepicker.Request) (string, error) {
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

// The recorder asks the picker for outputs only, offering to remember
// the choice when it has somewhere to keep it, and takes one output
// back; a cancel, a non-output, several sources or a malformed reply
// are errors, not guesses.
func TestPickAsksForOneOutput(t *testing.T) {
	for reply, want := range map[string]struct {
		name     string
		remember bool
		err      string
	}{
		"r/screen:DP-1":          {name: "DP-1", remember: true},
		"/screen:HDMI-A-1":       {name: "HDMI-A-1"},
		"":                       {err: ErrPickerCancelled.Error()},
		"/window:firefox":        {err: `replied "/window:firefox", not one output`},
		"/region:DP-1@0,0,10,10": {err: "not one output"},
		"/screen:A;screen:B":     {err: "not one output"},
		"garbage":                {err: "not one output"},
	} {
		p := &fakePicker{reply: reply}
		e := NewGPUEngine(p, nil, t.TempDir())
		name, remember, err := e.pick(context.Background())
		switch {
		case want.err != "":
			if err == nil || !strings.Contains(err.Error(), want.err) {
				t.Errorf("%q: %v, want %q", reply, err, want.err)
			}
		case err != nil || name != want.name || remember != want.remember:
			t.Errorf("%q: %q remember %v (%v), want %q remember %v", reply, name, remember, err, want.name, want.remember)
		}
		if r := p.asked[0]; r.Sources != sharepicker.SourceOutputs || !r.AllowToken || r.Multiple {
			t.Errorf("%q: asked %+v, want one output with the remember box", reply, r)
		}
	}
	p := &fakePicker{reply: "r/screen:DP-1"}
	if _, _, err := NewGPUEngine(p, nil, "").pick(context.Background()); err != nil || p.asked[0].AllowToken {
		t.Errorf("no state dir: asked %+v (%v), want no remember box", p.asked[0], err)
	}
	p = &fakePicker{err: errors.New("org.freedesktop.DBus.Error.ServiceUnknown")}
	if _, _, err := NewGPUEngine(p, nil, "").pick(context.Background()); err == nil || !strings.Contains(err.Error(), "share picker: org.freedesktop.DBus.Error.ServiceUnknown") {
		t.Errorf("no picker: %v", err)
	}
}

// A start cancelled while the picker shows takes that picker down, by
// the handle it was asked under, and is the context's error.
func TestCancelledPickDismissesThePicker(t *testing.T) {
	p := &fakePicker{block: true}
	e := NewGPUEngine(p, nil, "")
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

// A remembered output that is gone is forgotten and the picker asked;
// a remembered file that names no output is forgotten too. On a
// compositor that copies outputs only into shared memory, the picked
// output fails with that reason.
func TestRememberedOutputGoneAsksAgain(t *testing.T) {
	swaytest.Start(t)
	dir := t.TempDir()
	saved := filepath.Join(dir, outputFile)
	for _, content := range []string{"screen:NOPE-9\n", "window:firefox\n"} {
		if err := os.WriteFile(saved, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		p := &fakePicker{reply: "r/screen:HEADLESS-1"}
		_, err := NewGPUEngine(p, nil, dir).OpenSession(context.Background(), false)
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
	if _, err := NewGPUEngine(p, nil, dir).OpenSession(context.Background(), false); !errors.Is(err, errOutputGone) {
		t.Errorf("a picked output that does not exist: %v, want errOutputGone", err)
	}
}
