package nightlight

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/third_party/wayland/wl"
)

func openFilter(t *testing.T) (*Filter, *atomic.Int32) {
	t.Helper()
	var changes atomic.Int32
	f, err := Open(func() { changes.Add(1) })
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(f.Close)
	return f, &changes
}

func TestCTMAppliesAndClears(t *testing.T) {
	comp := newCompositor(t, true, true)
	comp.addOutput("DP-1")
	comp.addOutput("HDMI-A-1")
	f, _ := openFilter(t)
	if f.Protocol() != ctmIface {
		t.Fatalf("protocol = %s, want the CTM ahead of gamma", f.Protocol())
	}
	// Open alone binds nothing: an idle filter blocks no other client.
	eventually(t, "no matrix before Apply", wantIdentity(comp, "DP-1"))

	s := mustSetting(t, 3500, 90)
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 warmed", wantMatrix(comp, "DP-1", s.Matrix()))
	eventually(t, "HDMI-A-1 warmed", wantMatrix(comp, "HDMI-A-1", s.Matrix()))

	// A hotplugged output is warmed too, and an unplugged one forgotten.
	comp.addOutput("DP-2")
	eventually(t, "DP-2 warmed", wantMatrix(comp, "DP-2", s.Matrix()))
	comp.removeOutput("HDMI-A-1")
	warmer := mustSetting(t, 2500, 100)
	if err := f.Apply(warmer); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 rewarmed", wantMatrix(comp, "DP-1", warmer.Matrix()))

	if err := f.Clear(); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{"DP-1", "DP-2"} {
		eventually(t, out+" identity after Clear", wantIdentity(comp, out))
	}
	if err := f.Refused(); err != nil {
		t.Errorf("Refused = %v after a clean run", err)
	}
}

func TestGammaAppliesAndClears(t *testing.T) {
	comp := newCompositor(t, false, true)
	comp.addOutput("DP-1")
	f, _ := openFilter(t)
	if f.Protocol() != gammaIface {
		t.Fatalf("protocol = %s, want gamma control", f.Protocol())
	}
	s := mustSetting(t, 4000, 80)
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 ramps", wantRamps(comp, "DP-1", s.Ramps(comp.gammaSize)))

	comp.addOutput("DP-2")
	eventually(t, "DP-2 ramps", wantRamps(comp, "DP-2", s.Ramps(comp.gammaSize)))

	cooler := mustSetting(t, 6000, 100)
	if err := f.Apply(cooler); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 new ramps", wantRamps(comp, "DP-1", cooler.Ramps(comp.gammaSize)))

	if err := f.Clear(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 identity after Clear", wantIdentity(comp, "DP-1"))
	eventually(t, "DP-2 identity after Clear", wantIdentity(comp, "DP-2"))
}

// The compositor restores the identity when the client goes away, so a
// shell that exits or crashes leaves no tint behind.
func TestClosingRestoresTheIdentity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ctm, gamma bool
	}{{"ctm", true, true}, {"gamma", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			comp := newCompositor(t, tc.ctm, tc.gamma)
			comp.addOutput("DP-1")
			f, err := Open(nil)
			if err != nil {
				t.Fatal(err)
			}
			s := mustSetting(t, 3000, 100)
			if err := f.Apply(s); err != nil {
				t.Fatal(err)
			}
			if tc.ctm {
				eventually(t, "warmed", wantMatrix(comp, "DP-1", s.Matrix()))
			} else {
				eventually(t, "warmed", wantRamps(comp, "DP-1", s.Ramps(comp.gammaSize)))
			}
			f.Close()
			eventually(t, "identity after Close", wantIdentity(comp, "DP-1"))
			if err := f.Err(); err != nil {
				t.Errorf("Err after Close = %v, want nil", err)
			}
			if err := f.Apply(s); err == nil {
				t.Error("Apply after Close: want an error")
			}
		})
	}
}

func TestSecondCTMClientIsRefused(t *testing.T) {
	comp := newCompositor(t, true, false)
	comp.addOutput("DP-1")
	first, _ := openFilter(t)
	warm := mustSetting(t, 3000, 100)
	if err := first.Apply(warm); err != nil {
		t.Fatal(err)
	}
	eventually(t, "first warmed", wantMatrix(comp, "DP-1", warm.Matrix()))

	second, changes := openFilter(t)
	if err := second.Apply(mustSetting(t, 6000, 100)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "second refused", func() error {
		var refused *RefusedError
		if err := second.Refused(); !errors.As(err, &refused) || refused.Protocol != ctmIface {
			return errors.New("no CTM refusal yet")
		}
		return nil
	})
	if changes.Load() == 0 {
		t.Error("the refusal was not reported through changed")
	}
	// The refused client changes nothing; the owner's matrix stays.
	eventually(t, "owner's matrix kept", wantMatrix(comp, "DP-1", warm.Matrix()))
	if err := first.Refused(); err != nil {
		t.Errorf("owner Refused = %v", err)
	}
	// Clearing forgets the refusal.
	if err := second.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := second.Refused(); err != nil {
		t.Errorf("Refused after Clear = %v", err)
	}
}

func TestSecondGammaClientIsRefused(t *testing.T) {
	comp := newCompositor(t, false, true)
	comp.addOutput("DP-1")
	first, _ := openFilter(t)
	warm := mustSetting(t, 3000, 100)
	if err := first.Apply(warm); err != nil {
		t.Fatal(err)
	}
	eventually(t, "first warmed", wantRamps(comp, "DP-1", warm.Ramps(comp.gammaSize)))

	second, _ := openFilter(t)
	if err := second.Apply(mustSetting(t, 6000, 100)); err != nil {
		t.Fatal(err)
	}
	eventually(t, "second refused", func() error {
		var refused *RefusedError
		if err := second.Refused(); !errors.As(err, &refused) || refused.Protocol != gammaIface || refused.Output != "DP-1" {
			return errors.New("no gamma refusal naming DP-1 yet")
		}
		return nil
	})
	eventually(t, "owner's ramps kept", wantRamps(comp, "DP-1", warm.Ramps(comp.gammaSize)))
}

func TestOpenWithoutAColourProtocol(t *testing.T) {
	comp := newCompositor(t, false, false)
	comp.addOutput("DP-1")
	if _, err := Open(nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Open = %v, want ErrUnsupported", err)
	}
}

// An ease is a run of fractional strengths through Apply, each of which
// reaches the display on either protocol, ending back at the identity
// when the filter lets go, warm or cool of the identity, from the ease's
// first step.
func TestAnEaseReachesTheDisplayOnEitherProtocol(t *testing.T) {
	for _, tc := range []struct {
		name       string
		ctm, gamma bool
		want       func(*compositor, Setting) func() error
	}{
		{"ctm", true, false, func(c *compositor, s Setting) func() error { return wantMatrix(c, "DP-1", s.Matrix()) }},
		{"gamma", false, true, func(c *compositor, s Setting) func() error { return wantRamps(c, "DP-1", s.Ramps(c.gammaSize)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comp := newCompositor(t, tc.ctm, tc.gamma)
			comp.addOutput("DP-1")
			f, _ := openFilter(t)
			for _, night := range []Setting{mustSetting(t, 3000, 80), mustSetting(t, 10000, 100)} {
				for _, fraction := range []float64{1.0 / 900, 0.25, 0.5, 0.75, 1} {
					s := night.eased(fraction)
					if err := f.Apply(s); err != nil {
						t.Fatal(err)
					}
					eventually(t, tc.name+" step", tc.want(comp, s))
				}
				if err := f.Clear(); err != nil {
					t.Fatal(err)
				}
				eventually(t, "identity after Clear", wantIdentity(comp, "DP-1"))
			}
		})
	}
}

// easedPair finds two different steps of an ease that encode alike.
func easedPair(t *testing.T, night Setting) (a, b Setting) {
	t.Helper()
	const steps = 4000
	for i := 1; i < steps; i++ {
		a, b = night.eased(float64(i)/steps), night.eased(float64(i+1)/steps)
		if a != b && encodeMatrix(a.Matrix()) == encodeMatrix(b.Matrix()) {
			return a, b
		}
	}
	t.Fatal("no two steps of an ease encode alike")
	return a, b
}

func eventuallyCommits(t *testing.T, c *compositor, want int) {
	t.Helper()
	eventually(t, fmt.Sprintf("%d commits", want), func() error {
		if got := c.commits(); got != want {
			return fmt.Errorf("commits = %d, want %d", got, want)
		}
		return nil
	})
}

// roundtripFilter proves the compositor has read all f sent: its sync
// is answered in order, after the requests before it.
func roundtripFilter(t *testing.T, f *Filter) {
	t.Helper()
	done := &syncDone{ch: make(chan struct{})}
	f.mu.Lock()
	wlcb := wl.NewCallback(f.ctx)
	wlcb.AddDoneHandler(done)
	err := f.ctx.SendRequest(f.display, 0, wlcb)
	f.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no sync reply")
	}
}

type syncDone struct{ ch chan struct{} }

func (d *syncDone) HandleCallbackDone(wl.CallbackDoneEvent) { close(d.ch) }

func TestCTMSendsOnlyWhatTheWireWouldChange(t *testing.T) {
	comp := newCompositor(t, true, false)
	comp.addOutput("DP-1")
	f, _ := openFilter(t)
	night := mustSetting(t, 5000, 100)
	a, b := easedPair(t, night)

	if err := f.Apply(a); err != nil {
		t.Fatal(err)
	}
	roundtripFilter(t, f)
	eventuallyCommits(t, comp, 1)

	if err := f.Apply(b); err != nil {
		t.Fatal(err)
	}
	if err := f.Apply(a); err != nil {
		t.Fatal(err)
	}
	roundtripFilter(t, f)
	if got := comp.commits(); got != 1 {
		t.Errorf("commits = %d after steps that encode alike, want 1", got)
	}

	if err := f.Apply(night); err != nil {
		t.Fatal(err)
	}
	roundtripFilter(t, f)
	if got := comp.commits(); got != 2 {
		t.Errorf("commits = %d after a changed matrix, want 2", got)
	}
	eventually(t, "the changed matrix", wantMatrix(comp, "DP-1", night.Matrix()))
}

// An output that appears has no matrix, however unchanged the value.
func TestCTMSendsTheSameMatrixToANewOutput(t *testing.T) {
	comp := newCompositor(t, true, false)
	comp.addOutput("DP-1")
	f, _ := openFilter(t)
	s := mustSetting(t, 5000, 100)
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 warmed", wantMatrix(comp, "DP-1", s.Matrix()))
	comp.addOutput("DP-2")
	eventually(t, "the new output warmed", wantMatrix(comp, "DP-2", s.Matrix()))
	eventually(t, "DP-1 still warmed", wantMatrix(comp, "DP-1", s.Matrix()))
}

// Clearing destroys the manager and the compositor forgets the matrix,
// so the next bind sends it even if nothing about it changed.
func TestCTMSendsTheSameMatrixAfterARebind(t *testing.T) {
	comp := newCompositor(t, true, false)
	comp.addOutput("DP-1")
	f, _ := openFilter(t)
	s := mustSetting(t, 5000, 100)
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 warmed", wantMatrix(comp, "DP-1", s.Matrix()))
	if err := f.Clear(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the identity", wantIdentity(comp, "DP-1"))
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1 warmed again", wantMatrix(comp, "DP-1", s.Matrix()))
}

func TestGammaSendsOnlyChangedRamps(t *testing.T) {
	comp := newCompositor(t, false, true)
	comp.addOutput("DP-1")
	f, _ := openFilter(t)
	s := mustSetting(t, 5000, 100)
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the control's size", func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.outputs[firstOutput(f)].size == 0 {
			return errors.New("no ramp size yet")
		}
		return nil
	})
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first ramps", wantRamps(comp, "DP-1", s.Ramps(256)))
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	roundtripFilter(t, f)
	if got := comp.rampsSent("DP-1"); got != 1 {
		t.Errorf("ramps sent %d times for one value, want 1", got)
	}

	warmer := mustSetting(t, 3000, 100)
	if err := f.Apply(warmer); err != nil {
		t.Fatal(err)
	}
	roundtripFilter(t, f)
	if got := comp.rampsSent("DP-1"); got != 2 {
		t.Errorf("ramps sent %d times after a change, want 2", got)
	}
	eventually(t, "the changed ramps", wantRamps(comp, "DP-1", warmer.Ramps(256)))
}

// A control that is gone took its ramps with it: a new output, and the
// same output after a Clear, get the unchanged value.
func TestGammaSendsTheSameRampsToANewControl(t *testing.T) {
	comp := newCompositor(t, false, true)
	comp.addOutput("DP-1")
	f, _ := openFilter(t)
	s := mustSetting(t, 5000, 100)
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1's ramps", wantRamps(comp, "DP-1", s.Ramps(256)))

	comp.addOutput("DP-2")
	eventually(t, "the new output's ramps", wantRamps(comp, "DP-2", s.Ramps(256)))

	if err := f.Clear(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the identity", wantIdentity(comp, "DP-1"))
	if err := f.Apply(s); err != nil {
		t.Fatal(err)
	}
	eventually(t, "DP-1's ramps again", wantRamps(comp, "DP-1", s.Ramps(256)))
}

func firstOutput(f *Filter) uint32 {
	for name := range f.outputs {
		return name
	}
	return 0
}

// An ease's real traffic: one Apply a second over 15 minutes.
func TestEaseCommitCounts(t *testing.T) {
	const steps = 900
	for _, c := range []struct {
		temp, gamma int
		max         int
	}{{5000, 100, 120}, {2500, 90, 400}} {
		night := mustSetting(t, c.temp, c.gamma)
		distinct := 0
		var last encodedMatrix
		for i := 0; i <= steps; i++ {
			e := encodeMatrix(night.eased(float64(i) / steps).Matrix())
			if i == 0 || e != last {
				distinct++
			}
			last = e
		}
		t.Logf("%dK %d%%: %d steps, %d distinct on the wire", c.temp, c.gamma, steps+1, distinct)
		if distinct > c.max {
			t.Errorf("%dK: %d distinct matrices, want at most %d", c.temp, distinct, c.max)
		}
	}
}
