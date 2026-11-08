package idle

import (
	"context"
	"errors"
	"maps"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/idle/idlepolicy"
)

type fakeWatch struct {
	after        time.Duration
	idle, resume func()
	stopped      bool
}

type fakeWatcher struct{ watches []*fakeWatch }

func (w *fakeWatcher) Watch(after time.Duration, idle, resume func()) (func(), error) {
	fw := &fakeWatch{after: after, idle: idle, resume: resume}
	w.watches = append(w.watches, fw)
	return func() { fw.stopped = true }, nil
}

func (w *fakeWatcher) live() []*fakeWatch {
	var out []*fakeWatch
	for _, fw := range w.watches {
		if !fw.stopped {
			out = append(out, fw)
		}
	}
	return out
}

type fakeActions struct{ calls []string }

func (a *fakeActions) Dim(share idlepolicy.DimPercent) (func() error, error) {
	a.calls = append(a.calls, "dim")
	return func() error { a.calls = append(a.calls, "undim"); return nil }, nil
}

func (a *fakeActions) Lock() error { a.calls = append(a.calls, "lock"); return nil }

func (a *fakeActions) ScreenOff() (func() error, error) {
	a.calls = append(a.calls, "screen-off")
	return func() error { a.calls = append(a.calls, "screen-on"); return nil }, nil
}

func (a *fakeActions) Suspend() error { a.calls = append(a.calls, "suspend"); return nil }

func timeout(t *testing.T, d time.Duration) idlepolicy.Timeout {
	t.Helper()
	to, err := idlepolicy.NewTimeout(d)
	if err != nil {
		t.Fatal(err)
	}
	return to
}

func testPolicy(t *testing.T) idlepolicy.Policy {
	t.Helper()
	share, err := idlepolicy.NewDimPercent(30)
	if err != nil {
		t.Fatal(err)
	}
	var p idlepolicy.Policy
	p.Timeouts[idlepolicy.Dim] = idlepolicy.Timeouts{AC: timeout(t, 4*time.Minute), Battery: timeout(t, 2*time.Minute)}
	p.Timeouts[idlepolicy.Lock] = idlepolicy.Timeouts{AC: timeout(t, 5*time.Minute), Battery: timeout(t, 3*time.Minute)}
	p.Timeouts[idlepolicy.ScreenOff] = idlepolicy.Timeouts{AC: timeout(t, 10*time.Minute), Battery: timeout(t, 5*time.Minute)}
	p.Timeouts[idlepolicy.Suspend] = idlepolicy.Timeouts{AC: idlepolicy.Never, Battery: timeout(t, 15*time.Minute)}
	p.DimPercent = share
	return p
}

func afters(ws []*fakeWatch) []time.Duration {
	out := make([]time.Duration, len(ws))
	for i, w := range ws {
		out[i] = w.after
	}
	return out
}

func TestEachPowerSourceArmsItsOwnTimeouts(t *testing.T) {
	w, a := &fakeWatcher{}, &fakeActions{}
	s := New(w, a)
	if err := s.Configure(testPolicy(t), true, idlepolicy.AC); err != nil {
		t.Fatal(err)
	}
	if got, want := afters(w.live()), []time.Duration{4 * time.Minute, 5 * time.Minute, 10 * time.Minute}; !slices.Equal(got, want) {
		t.Fatalf("on AC: watches %v, want %v (suspend is never on AC)", got, want)
	}
	if err := s.SetPowerSource(idlepolicy.Battery); err != nil {
		t.Fatal(err)
	}
	if got, want := afters(w.live()), []time.Duration{2 * time.Minute, 3 * time.Minute, 5 * time.Minute, 15 * time.Minute}; !slices.Equal(got, want) {
		t.Fatalf("on battery: watches %v, want %v", got, want)
	}
	if len(w.watches) != 7 {
		t.Errorf("%d watches made in all, want 3 + 4", len(w.watches))
	}
	if len(a.calls) != 0 {
		t.Errorf("arming ran %v", a.calls)
	}
}

func TestStagesRunWhenIdleAndUndoOnResume(t *testing.T) {
	w, a := &fakeWatcher{}, &fakeActions{}
	s := New(w, a)
	if err := s.Configure(testPolicy(t), true, idlepolicy.Battery); err != nil {
		t.Fatal(err)
	}
	live := w.live()
	for _, fw := range live {
		fw.idle()
	}
	if want := []string{"dim", "lock", "screen-off", "suspend"}; !slices.Equal(a.calls, want) {
		t.Fatalf("idle through every stage ran %v, want %v", a.calls, want)
	}
	if _, ok := s.Status().Next(); ok {
		t.Error("a next stage after every stage ran")
	}
	live[0].idle()
	if len(a.calls) != 4 {
		t.Errorf("a repeated idle event ran its stage again: %v", a.calls)
	}
	a.calls = nil
	for _, fw := range live {
		fw.resume()
	}
	if want := []string{"undim", "screen-on"}; !slices.Equal(a.calls, want) {
		t.Fatalf("resume undid %v, want %v", a.calls, want)
	}
	if next, ok := s.Status().Next(); !ok || next.Stage != idlepolicy.Dim {
		t.Errorf("after resume the next stage is %v %v, want dim", next, ok)
	}
}

func TestDisabledArmsNothingAndReconfiguringUndoes(t *testing.T) {
	w, a := &fakeWatcher{}, &fakeActions{}
	s := New(w, a)
	if err := s.Configure(testPolicy(t), true, idlepolicy.AC); err != nil {
		t.Fatal(err)
	}
	w.live()[0].idle()
	stale := w.live()[0]
	if err := s.Configure(testPolicy(t), false, idlepolicy.AC); err != nil {
		t.Fatal(err)
	}
	if n := len(w.live()); n != 0 {
		t.Fatalf("disabled policy holds %d watches", n)
	}
	if want := []string{"dim", "undim"}; !slices.Equal(a.calls, want) {
		t.Errorf("disabling while dimmed ran %v, want %v", a.calls, want)
	}
	stale.idle()
	if len(a.calls) != 2 {
		t.Errorf("a stopped watch still runs its stage: %v", a.calls)
	}
}

type countingWatcher struct {
	fakeWatcher
	err error
}

func (w *countingWatcher) Watch(after time.Duration, idle, resume func()) (func(), error) {
	if w.err != nil {
		return nil, w.err
	}
	return w.fakeWatcher.Watch(after, idle, resume)
}

func TestAWatchThatCannotBeMadeIsTheError(t *testing.T) {
	refused := errors.New("refused")
	s := New(&countingWatcher{err: refused}, &fakeActions{})
	if err := s.Configure(testPolicy(t), true, idlepolicy.AC); !errors.Is(err, refused) {
		t.Fatalf("Configure = %v, want the watcher's error", err)
	}
}

type fakeBacklights struct {
	devices map[string]brightness.Device
	sets    int
}

func (f *fakeBacklights) Devices(context.Context) ([]brightness.Device, error) {
	out := make([]brightness.Device, 0, len(f.devices))
	for _, name := range []string{"intel_backlight", "ddcci7"} {
		if d, ok := f.devices[name]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeBacklights) Set(_ context.Context, name string, percent float64) error {
	f.sets++
	d := f.devices[name]
	d.Brightness = uint32(min(max(percent, 0), 100)/100*float64(d.Max) + 0.5)
	f.devices[name] = d
	return nil
}

func TestDimRestoresTheExactPreviousLevel(t *testing.T) {
	levels := map[string]brightness.Device{
		"intel_backlight": {Name: "intel_backlight", Brightness: 19393, Max: 96000},
		"ddcci7":          {Name: "ddcci7", Brightness: 37, Max: 100},
	}
	b := &fakeBacklights{devices: maps.Clone(levels)}
	share, err := idlepolicy.NewDimPercent(30)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := System{Backlights: b}.Dim(share)
	if err != nil {
		t.Fatal(err)
	}
	for name, before := range levels {
		if got := b.devices[name].Brightness; got >= before.Brightness || got == 0 {
			t.Errorf("%s dimmed to %d from %d", name, got, before.Brightness)
		}
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	for name, before := range levels {
		if got := b.devices[name].Brightness; got != before.Brightness {
			t.Errorf("%s restored to %d, want exactly %d", name, got, before.Brightness)
		}
	}
}

func TestStagesWithoutTheirSystemFailLoudly(t *testing.T) {
	var s System
	share, err := idlepolicy.NewDimPercent(30)
	if err != nil {
		t.Fatal(err)
	}
	if restore, err := s.Dim(share); err != nil || restore != nil {
		t.Errorf("Dim without backlights = %v, %v; want nothing to do", restore != nil, err)
	}
	if err := s.Lock(); !errors.Is(err, ErrNoLogind) {
		t.Errorf("Lock = %v, want ErrNoLogind", err)
	}
	if err := s.Suspend(); !errors.Is(err, ErrNoLogind) {
		t.Errorf("Suspend = %v, want ErrNoLogind", err)
	}
	if _, err := s.ScreenOff(); !errors.Is(err, ErrNoOutputPower) {
		t.Errorf("ScreenOff = %v, want ErrNoOutputPower", err)
	}
}

func TestWaitingBetweenStagesStartsNoGoroutineOrTimer(t *testing.T) {
	w, a := &fakeWatcher{}, &fakeActions{}
	s := New(w, a)
	before := runtime.NumGoroutine()
	if err := s.Configure(testPolicy(t), true, idlepolicy.Battery); err != nil {
		t.Fatal(err)
	}
	w.live()[0].idle()
	w.live()[0].resume()
	if after := runtime.NumGoroutine(); after != before {
		t.Errorf("arming and running a stage left %d goroutines, had %d", after, before)
	}
	if n := len(w.watches); n != 4 {
		t.Errorf("%d watches for 4 stages", n)
	}
}
