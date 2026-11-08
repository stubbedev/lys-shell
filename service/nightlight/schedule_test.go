package nightlight

import (
	"math"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/service/nightlight/nightschedule"
)

func mustLocation(t *testing.T, latitude, longitude float64) nightschedule.Location {
	t.Helper()
	loc, err := nightschedule.NewLocation(latitude, longitude)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func mustTransition(t *testing.T, minutes int64) nightschedule.Transition {
	t.Helper()
	tr, err := nightschedule.NewTransition(minutes)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func mired(s Setting) float64 { return miredsPerKelvin / s.temp }

// The ease runs from the identity through the mired and gamma midpoints
// to the full strength, centred on the solar event.
func TestTargetAtEasesAcrossDusk(t *testing.T) {
	loc := mustLocation(t, 55.6761, 12.5683)
	night := mustSetting(t, 3000, 80)
	tr := mustTransition(t, 15)
	dusk := sunTimesFor(utc(2024, 3, 20, 12, 0), loc.Latitude(), loc.Longitude(), civilTwilightAltitude).set

	before := TargetAt(dusk.Add(-8*time.Minute), loc, tr, night)
	if !before.Setting.IsIdentity() || before.Easing || before.Phase != PhaseDay {
		t.Errorf("before the ease = %+v, want day with no filter", before)
	}
	start := TargetAt(dusk.Add(-tr.Duration()/2), loc, tr, night)
	if !start.Setting.IsIdentity() || !start.Easing || start.Phase != PhaseNight {
		t.Errorf("first moment = %+v, want the identity, easing into night", start)
	}
	mid := TargetAt(dusk, loc, tr, night)
	wantMired := (mired(identity) + mired(night)) / 2
	if !mid.Easing || math.Abs(mired(mid.Setting)-wantMired) > 1e-6 || math.Abs(mid.Setting.gamma-90) > 1e-6 {
		t.Errorf("at dusk = %v mired, %v%%; want %v mired, 90%%", mired(mid.Setting), mid.Setting.gamma, wantMired)
	}
	end := TargetAt(dusk.Add(tr.Duration()/2), loc, tr, night)
	if end.Setting != night || end.Easing {
		t.Errorf("end = %+v, want the full strength, settled", end)
	}
	after := TargetAt(dusk.Add(time.Hour), loc, tr, night)
	if after.Setting != night || after.Easing || after.Phase != PhaseNight {
		t.Errorf("after = %+v, want full night", after)
	}
}

func TestTargetAtEasesAcrossDawn(t *testing.T) {
	loc := mustLocation(t, 55.6761, 12.5683)
	night := mustSetting(t, 3000, 80)
	tr := mustTransition(t, 15)
	dawn := sunTimesFor(utc(2024, 3, 20, 12, 0), loc.Latitude(), loc.Longitude(), civilTwilightAltitude).rise

	if start := TargetAt(dawn.Add(-tr.Duration()/2), loc, tr, night); start.Setting != night || !start.Easing || start.Phase != PhaseDay {
		t.Errorf("first moment = %+v, want the full strength, easing into day", start)
	}
	mid := TargetAt(dawn, loc, tr, night)
	if math.Abs(mid.Setting.gamma-90) > 1e-6 {
		t.Errorf("at dawn gamma = %v, want 90", mid.Setting.gamma)
	}
	if end := TargetAt(dawn.Add(tr.Duration()/2), loc, tr, night); !end.Setting.IsIdentity() || end.Easing {
		t.Errorf("end = %+v, want the identity, settled", end)
	}
}

// Stepping second by second through either ease only ever moves one way,
// and no step is a visible jump, whatever the configured strength.
func TestStepsNeverMoveBackwardsOrShow(t *testing.T) {
	loc := mustLocation(t, 55.6761, 12.5683)
	tr := mustTransition(t, 15)
	st := sunTimesFor(utc(2024, 3, 20, 12, 0), loc.Latitude(), loc.Longitude(), civilTwilightAltitude)
	const visible = 0.005
	for _, night := range []Setting{mustSetting(t, 5000, 100), mustSetting(t, 3000, 80), mustSetting(t, 1000, 100), mustSetting(t, 2000, 20), mustSetting(t, 6700, 100), mustSetting(t, 10000, 100), mustSetting(t, MaxTemp, 80)} {
		for _, tc := range []struct {
			event time.Time
			warms bool
		}{{st.set, true}, {st.rise, false}} {
			prev := TargetAt(tc.event.Add(-tr.Duration()/2), loc, tr, night).Setting
			for s := time.Second; s <= tr.Duration(); s += time.Second {
				now := TargetAt(tc.event.Add(-tr.Duration()/2+s), loc, tr, night).Setting
				backwards := mired(now) < mired(prev)-1e-9
				if tc.warms != (mired(night) > mired(identity)) {
					backwards = mired(now) > mired(prev)+1e-9
				}
				if backwards {
					t.Fatalf("%+v at %v: mired went from %v to %v", night, s, mired(prev), mired(now))
				}
				a, b := prev.scale(), now.scale()
				for ch := range a {
					if d := math.Abs(a[ch] - b[ch]); d > visible {
						t.Fatalf("%+v at %v: channel %d jumped by %v", night, s, ch, d)
					}
				}
				prev = now
			}
		}
	}
}

func TestZeroTransitionSwitchesAtTheEvent(t *testing.T) {
	loc := mustLocation(t, 55.6761, 12.5683)
	night := mustSetting(t, 3000, 80)
	var instant nightschedule.Transition
	dusk := sunTimesFor(utc(2024, 3, 20, 12, 0), loc.Latitude(), loc.Longitude(), civilTwilightAltitude).set
	if got := TargetAt(dusk.Add(-time.Second), loc, instant, night); !got.Setting.IsIdentity() || got.Easing || got.Phase != PhaseDay {
		t.Errorf("a second before dusk = %+v, want day with no easing", got)
	}
	if got := TargetAt(dusk, loc, instant, night); got.Setting != night || got.Easing || got.Phase != PhaseNight {
		t.Errorf("at dusk = %+v, want the full strength with no easing", got)
	}
}

// Where the sun never crosses civil twilight the date has no event, so
// nothing eases at any moment of it, and there is nothing to wait for.
func TestPolarDatesHaveNoTransition(t *testing.T) {
	night := mustSetting(t, 3000, 80)
	tr := mustTransition(t, 15)
	for _, tc := range []struct {
		name  string
		loc   nightschedule.Location
		date  time.Time
		phase Phase
	}{
		{"svalbard midnight sun", mustLocation(t, 78.2, 15.6), utc(2024, 6, 21, 0, 0), PhaseDay},
		{"svalbard polar night", mustLocation(t, 78.2, 15.6), utc(2024, 12, 21, 0, 0), PhaseNight},
		{"trondheim white night", mustLocation(t, 63.43, 10.39), utc(2024, 6, 21, 0, 0), PhaseDay},
	} {
		for m := range 24 * 60 {
			got := TargetAt(tc.date.Add(time.Duration(m)*time.Minute), tc.loc, tr, night)
			if got.Easing || got.Phase != tc.phase || got.Setting.IsIdentity() != (tc.phase == PhaseDay) {
				t.Fatalf("%s at minute %d = %+v, want settled %v", tc.name, m, got, tc.phase)
			}
		}
		if next, ok := NextEase(tc.date, tc.loc, tr); ok {
			t.Errorf("%s: next ease %v, want none", tc.name, next)
		}
	}
}

func TestNextEaseIsTheNextStart(t *testing.T) {
	loc := mustLocation(t, 55.6761, 12.5683)
	tr := mustTransition(t, 15)
	st := sunTimesFor(utc(2024, 3, 20, 12, 0), loc.Latitude(), loc.Longitude(), civilTwilightAltitude)
	start := st.set.Add(-tr.Duration() / 2)

	if got, ok := NextEase(utc(2024, 3, 20, 12, 0), loc, tr); !ok || !got.Equal(start) {
		t.Errorf("from noon = %v, %v; want %v", got, ok, start)
	}
	// The moment an ease starts it is under way: the next one is another.
	got, ok := NextEase(start, loc, tr)
	if !ok || !got.After(start.Add(time.Hour)) {
		t.Errorf("from the start itself = %v, %v; want a later ease", got, ok)
	}
	var instant nightschedule.Transition
	if got, ok := NextEase(utc(2024, 3, 20, 12, 0), loc, instant); !ok || !got.Equal(st.set) {
		t.Errorf("no transition = %v, %v; want dusk %v", got, ok, st.set)
	}
}

// Dawn and dusk land anywhere on the UTC clock away from Greenwich: at
// every longitude the strength moves at the ease's rate and no faster,
// with exactly one dawn and one dusk a day, through UTC midnight too.
func TestScheduleHasNoJumpsAtAnyLongitude(t *testing.T) {
	night := mustSetting(t, 3000, 100)
	tr := mustTransition(t, 15)
	maxStep := (mired(night) - mired(identity)) / 15 * 1.01
	for _, lng := range []float64{-179, -157.8, -90, -30, 0.1, 45, 90, 151.2, 179} {
		for _, lat := range []float64{-33.87, 21.3, 55.6} {
			loc := mustLocation(t, lat, lng)
			for _, date := range []time.Time{utc(2024, 3, 20, 0, 0), utc(2024, 6, 21, 0, 0), utc(2024, 12, 21, 0, 0)} {
				prev := TargetAt(date, loc, tr, night)
				eases := 0
				for m := 1; m <= 24*60; m++ {
					now := TargetAt(date.Add(time.Duration(m)*time.Minute), loc, tr, night)
					if d := math.Abs(mired(now.Setting) - mired(prev.Setting)); d > maxStep {
						t.Fatalf("(%v, %v) %v +%dm: mired jumped by %v, max %v", lat, lng, date, m, d, maxStep)
					}
					if now.Easing && !prev.Easing {
						eases++
					}
					prev = now
				}
				if eases < 1 || eases > 3 {
					t.Errorf("(%v, %v) %v: %d eases in a day, want dawn and dusk", lat, lng, date, eases)
				}
			}
		}
	}
}

func TestEasedHoldsTheEndpoints(t *testing.T) {
	night := mustSetting(t, 5000, 100)
	if got := night.eased(0); !got.IsIdentity() {
		t.Errorf("eased(0) = %+v, want the identity", got)
	}
	if got := night.eased(1); got != night {
		t.Errorf("eased(1) = %+v, want %+v", got, night)
	}
	if got := night.eased(0.01); got.IsIdentity() || got == night {
		t.Errorf("eased(0.01) = %+v, want neither endpoint", got)
	}
}
