package nightlight

import (
	"errors"
	"sync/atomic"
	"testing"
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
