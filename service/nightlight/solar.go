// Package nightlight is the night light module's service side: the
// colour filter it applies through the compositor's CTM or gamma
// protocol, the dawn/dusk schedule (NOAA sunrise equation, no network)
// and its easing, the GeoClue location lookup, and the records that
// survive restarts.
package nightlight

import (
	"math"
	"time"

	"github.com/stubbedev/lys-desktop/service/nightlight/nightschedule"
	"github.com/stubbedev/lys-lib/third_party/gomath"
)

// civilTwilightAltitude is the solar altitude of civil dawn and dusk, in
// degrees: the event the schedule anchors to.
const civilTwilightAltitude = -6.0

// Phase is whether the sun is above civil twilight at a place and time.
type Phase uint8

// Solar phases.
const (
	PhaseDay Phase = iota + 1
	PhaseNight
)

// String is the persist-record spelling.
func (p Phase) String() string {
	switch p {
	case PhaseDay:
		return "day"
	case PhaseNight:
		return "night"
	}
	return "none"
}

// sunTimes is one solar day's outcome at an altitude: the solar noon
// and the sun's crossings of the altitude, or the sun never going below
// it or never above it.
type sunTimes struct {
	noon       time.Time
	rise, set  time.Time
	polarDay   bool
	polarNight bool
}

// solarDayAt is the civil solar day now belongs to, the one whose solar
// noon is nearest: a UTC date's day spills across midnight away from
// Greenwich, so the date alone can name the wrong one.
func solarDayAt(now time.Time, loc nightschedule.Location) sunTimes {
	nearest := sunTimesFor(now, loc.Latitude(), loc.Longitude(), civilTwilightAltitude)
	for _, days := range []time.Duration{-24, 24} {
		st := sunTimesFor(now.Add(days*time.Hour), loc.Latitude(), loc.Longitude(), civilTwilightAltitude)
		if st.noon.Sub(now).Abs() < nearest.noon.Sub(now).Abs() {
			nearest = st
		}
	}
	return nearest
}

// PhaseAt is day from civil dawn up to civil dusk of the solar day now
// belongs to, night otherwise; polar day is day and polar night is
// night.
func PhaseAt(now time.Time, loc nightschedule.Location) Phase {
	st := solarDayAt(now, loc)
	switch {
	case st.polarDay:
		return PhaseDay
	case st.polarNight:
		return PhaseNight
	case !now.Before(st.rise) && now.Before(st.set):
		return PhaseDay
	}
	return PhaseNight
}

// unixEpochDaysFromCE is chrono's num_days_from_ce of 1970-01-01.
const unixEpochDaysFromCE = 719163

// remEuclid is f64::rem_euclid: the non-negative remainder.
func remEuclid(x, m float64) float64 {
	r := math.Mod(x, m)
	if r < 0 {
		r += m
	}
	return r
}

func radians(deg float64) float64 { return deg * math.Pi / 180 }

func degrees(rad float64) float64 { return rad * 180 / math.Pi }

// sunTimesFor is solar.rs sun_times, step for step, at any solar
// altitude in degrees.
func sunTimesFor(now time.Time, latitude, longitude, altitude float64) sunTimes {
	utc := now.UTC()
	midnight := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	daysFromCE := float64(midnight.Unix()/86400 + unixEpochDaysFromCE)
	// Julian day number (integer, at noon) for the date of now.
	jdn := daysFromCE + 1_721_425.0

	// Days since J2000, with leap-second fudge. lW is longitude WEST.
	lW := -longitude
	n := math.Round(jdn - 2_451_545.0 + 0.0008)

	// Mean solar time: solar noon comes later the further west, so the
	// west longitude ADDS. solar.rs subtracts it here, which mirrors
	// every schedule about Greenwich (Copenhagen's sunrise lands 1h40m
	// late, New York's ten hours off); the known-value tests pin the
	// corrected sign.
	jStar := n + lW/360.0

	// Solar mean anomaly (degrees).
	m := remEuclid(357.5291+float64(0.985_600_28*jStar), 360.0)
	mRad := radians(m)

	// Equation of the center (degrees).
	c := float64(1.9148*gomath.Sin(mRad)) + float64(0.0200*gomath.Sin(2.0*mRad)) + float64(0.0003*gomath.Sin(3.0*mRad))

	// Ecliptic longitude (degrees).
	lambda := remEuclid(m+c+180.0+102.9372, 360.0)
	lambdaRad := radians(lambda)

	// Solar transit (Julian date of solar noon).
	jTransit := 2_451_545.0 + jStar + float64(0.0053*gomath.Sin(mRad)) - float64(0.0069*gomath.Sin(2.0*lambdaRad))

	// Sun declination.
	sinDecl := gomath.Sin(lambdaRad) * gomath.Sin(radians(23.4397))
	decl := gomath.Asin(sinDecl)

	noon, okNoon := julianToUTC(jTransit)
	if !okNoon {
		// Numerically degenerate: the safer "day" keeps the filter off.
		return sunTimes{polarDay: true}
	}

	// Hour angle at the altitude.
	latRad := radians(latitude)
	cosOmega := (gomath.Sin(radians(altitude)) - float64(gomath.Sin(latRad)*gomath.Sin(decl))) /
		(gomath.Cos(latRad) * gomath.Cos(decl))

	if cosOmega < -1.0 {
		return sunTimes{noon: noon, polarDay: true} // never goes below
	}
	if cosOmega > 1.0 {
		return sunTimes{noon: noon, polarNight: true} // never comes above
	}

	omega := degrees(gomath.Acos(cosOmega))
	jRise := jTransit - omega/360.0
	jSet := jTransit + omega/360.0

	rise, okRise := julianToUTC(jRise)
	set, okSet := julianToUTC(jSet)
	if !okRise || !okSet {
		return sunTimes{noon: noon, polarDay: true}
	}
	return sunTimes{noon: noon, rise: rise, set: set}
}

// julianToUTC converts a Julian Date to a UTC instant, rounded to the
// second; false when it is not a finite time.
func julianToUTC(jd float64) (time.Time, bool) {
	secs := (jd - 2_440_587.5) * 86_400.0
	if math.IsNaN(secs) || math.IsInf(secs, 0) {
		return time.Time{}, false
	}
	return time.Unix(int64(math.Round(secs)), 0).UTC(), true
}
