package idle

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/service/idle/idlepolicy"
	"github.com/stubbedev/lys-desktop/swaytest"
	"github.com/stubbedev/lys-lib/vinput"
)

type recordingActions struct {
	mu    sync.Mutex
	calls []string
	out   *Compositor
}

func (a *recordingActions) record(call string) {
	a.mu.Lock()
	a.calls = append(a.calls, call)
	a.mu.Unlock()
}

func (a *recordingActions) Calls() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

func (a *recordingActions) Dim(idlepolicy.DimPercent) (func() error, error) {
	a.record("dim")
	return func() error { a.record("undim"); return nil }, nil
}

func (a *recordingActions) Lock() error { a.record("lock"); return nil }

func (a *recordingActions) ScreenOff() (func() error, error) {
	a.record("screen-off")
	restore, err := a.out.PowerOff()
	if err != nil {
		return nil, err
	}
	return func() error { a.record("screen-on"); return restore() }, nil
}

func (a *recordingActions) Suspend() error { a.record("suspend"); return nil }

func connectSway(t *testing.T) *Compositor {
	t.Helper()
	swaytest.Start(t)
	c, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func outputsPowered(t *testing.T) []bool {
	t.Helper()
	var outputs []struct {
		Power bool `json:"power"`
	}
	if err := json.Unmarshal(swaytest.Msg(t, "-t", "get_outputs", "-r"), &outputs); err != nil {
		t.Fatal(err)
	}
	powered := make([]bool, len(outputs))
	for i, o := range outputs {
		powered[i] = o.Power
	}
	return powered
}

func awaitCalls(t *testing.T, a *recordingActions, want []string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		got := a.Calls()
		if len(got) >= len(want) {
			if !slices.Equal(got, want) {
				t.Fatalf("stages ran %v, want %v", got, want)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("stages ran %v, want %v", got, want)
		}
	}
}

func secondsPolicy(t *testing.T, stages ...idlepolicy.Stage) idlepolicy.Policy {
	t.Helper()
	share, err := idlepolicy.NewDimPercent(50)
	if err != nil {
		t.Fatal(err)
	}
	p := idlepolicy.Policy{DimPercent: share}
	for i, stage := range stages {
		p.Timeouts[stage] = idlepolicy.Timeouts{AC: timeout(t, time.Duration(i+1)*time.Second), Battery: timeout(t, time.Duration(i+1)*time.Second)}
	}
	return p
}

func TestAnIdleSessionWalksEveryStageOnTheCompositor(t *testing.T) {
	c := connectSway(t)
	a := &recordingActions{out: c}
	s := New(c, a)
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Configure(secondsPolicy(t, idlepolicy.Dim, idlepolicy.Lock, idlepolicy.ScreenOff, idlepolicy.Suspend), true, idlepolicy.AC); err != nil {
		t.Fatal(err)
	}
	awaitCalls(t, a, []string{"dim", "lock", "screen-off", "suspend"})
	if powered := outputsPowered(t); slices.Contains(powered, true) || len(powered) == 0 {
		t.Fatalf("after screen-off the outputs are powered %v", powered)
	}
	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pointer.Close)
	if err := pointer.Motion(5, 5); err != nil {
		t.Fatal(err)
	}
	if err := pointer.Roundtrip(); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); len(a.Calls()) < 6; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("input undid %v", a.Calls()[4:])
		}
	}
	if undone := slices.Sorted(slices.Values(a.Calls()[4:])); !slices.Equal(undone, []string{"screen-on", "undim"}) {
		t.Fatalf("input undid %v, want the dim and the screen-off", undone)
	}
	for deadline := time.Now().Add(5 * time.Second); slices.Contains(outputsPowered(t), false); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("input left the outputs powered %v", outputsPowered(t))
		}
	}
}

func TestInputBeforeATimeoutHoldsItsStage(t *testing.T) {
	c := connectSway(t)
	a := &recordingActions{out: c}
	s := New(c, a)
	t.Cleanup(func() { _ = s.Close() })
	share, err := idlepolicy.NewDimPercent(50)
	if err != nil {
		t.Fatal(err)
	}
	p := idlepolicy.Policy{DimPercent: share}
	p.Timeouts[idlepolicy.Dim] = idlepolicy.Timeouts{AC: timeout(t, 2*time.Second), Battery: timeout(t, time.Second)}
	if err := s.Configure(p, true, idlepolicy.AC); err != nil {
		t.Fatal(err)
	}
	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pointer.Close)
	for range 6 {
		time.Sleep(500 * time.Millisecond)
		if err := pointer.Motion(1, 0); err != nil {
			t.Fatal(err)
		}
		if err := pointer.Roundtrip(); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.Calls(); len(got) != 0 {
		t.Fatalf("input every 500ms still ran %v on a 2s timeout", got)
	}
	if err := s.SetPowerSource(idlepolicy.Battery); err != nil {
		t.Fatal(err)
	}
	awaitCalls(t, a, []string{"dim"})
}
