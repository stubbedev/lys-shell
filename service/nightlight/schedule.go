package nightlight

import (
	"time"

	"github.com/stubbedev/lys-desktop/service/nightlight/nightschedule"
)

// Target is what the schedule wants at a moment.
type Target struct {
	// Phase is the phase in force: an ease counts as the phase it heads
	// for from its first moment, though it is centred on the solar
	// event, so a manual toggle made during one outlasts it.
	Phase Phase
	// Setting is the strength to show; the identity means no filter.
	Setting Setting
	// Easing is whether the moment is inside a transition, when Setting
	// keeps changing.
	Easing bool
}

// TargetAt is the schedule at now for a place, a transition length and
// the full night strength. Each transition is centred on civil dawn or
// dusk; a polar solar day has neither, so it has no transition.
func TargetAt(now time.Time, loc nightschedule.Location, transition nightschedule.Transition, night Setting) Target {
	length := transition.Duration()
	target := Target{Phase: PhaseAt(now.Add(length/2), loc)}
	st := solarDayAt(now, loc)
	switch {
	case st.polarDay:
		target.Setting = identity
	case st.polarNight:
		target.Setting = night
	default:
		day := float64(easedPast(now, st.rise, length) * (1 - easedPast(now, st.set, length)))
		target.Setting = night.eased(1 - day)
		target.Easing = inTransition(now, st.rise, length) || inTransition(now, st.set, length)
	}
	return target
}

// NextEase is the moment the next transition after now starts, or, with
// no transition length, the next civil dawn or dusk; false when polar
// days leave none in reach.
func NextEase(now time.Time, loc nightschedule.Location, transition nightschedule.Transition) (time.Time, bool) {
	length := transition.Duration()
	var next time.Time
	found := false
	consider := func(event time.Time) {
		start := event.Add(-length / 2)
		if start.After(now) && (!found || start.Before(next)) {
			next, found = start, true
		}
	}
	for _, days := range []time.Duration{-24, 0, 24, 48} {
		st := sunTimesFor(now.Add(days*time.Hour), loc.Latitude(), loc.Longitude(), civilTwilightAltitude)
		if st.polarDay || st.polarNight {
			continue
		}
		consider(st.rise)
		consider(st.set)
	}
	return next, found
}

// easedPast is how far now is past a transition centred on event: 0
// before it, 1 after it.
func easedPast(now, event time.Time, length time.Duration) float64 {
	elapsed := now.Sub(event) + length/2
	switch {
	case elapsed < 0:
		return 0
	case elapsed >= length:
		return 1
	}
	return float64(elapsed) / float64(length)
}

// inTransition is whether now is inside the transition centred on
// event.
func inTransition(now, event time.Time, length time.Duration) bool {
	elapsed := now.Sub(event) + length/2
	return elapsed >= 0 && elapsed < length
}
