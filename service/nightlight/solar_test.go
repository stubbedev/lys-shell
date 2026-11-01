package nightlight

import (
	"testing"
	"time"
)

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// The solar.rs test cases, verbatim.
func TestPhaseAtMatchesTheRustCases(t *testing.T) {
	for _, tc := range []struct {
		name     string
		now      time.Time
		lat, lng float64
		want     Phase
	}{
		{"copenhagen summer noon is day", utc(2024, 6, 21, 12, 0), 55.6, 12.5, PhaseDay},
		{"copenhagen summer predawn is night", utc(2024, 6, 21, 1, 0), 55.6, 12.5, PhaseNight},
		{"copenhagen winter evening is night", utc(2024, 12, 21, 17, 0), 55.6, 12.5, PhaseNight},
		{"copenhagen winter midday is day", utc(2024, 12, 21, 11, 0), 55.6, 12.5, PhaseDay},
		{"arctic midwinter is night all day", utc(2024, 12, 21, 12, 0), 78.2, 15.6, PhaseNight},
		{"arctic midsummer is day all night", utc(2024, 6, 21, 0, 0), 78.2, 15.6, PhaseDay},
	} {
		if got := PhaseAt(tc.now, mustLocation(t, tc.lat, tc.lng)); got != tc.want {
			t.Errorf("%s: phase = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// sunriseAltitude is the geometric sunrise and sunset, with refraction
// and the solar disc.
const sunriseAltitude = -0.833

// Almanac sunrise/sunset times (timeanddate.com, UTC) the NOAA
// equation lands within a few minutes of.
func TestSunTimesAgainstKnownValues(t *testing.T) {
	// The simplified equation (one day number per UTC date) drifts a few
	// minutes at longitudes far from Greenwich.
	const tolerance = 8 * time.Minute
	for _, tc := range []struct {
		name           string
		day            time.Time
		lat, lng       float64
		rise, set      time.Time
		polarDayWant   bool
		polarNightWant bool
	}{
		{name: "copenhagen midsummer", day: utc(2024, 6, 21, 12, 0), lat: 55.6761, lng: 12.5683, rise: utc(2024, 6, 21, 2, 25), set: utc(2024, 6, 21, 19, 57)},
		{name: "london midwinter", day: utc(2024, 12, 21, 12, 0), lat: 51.5074, lng: -0.1278, rise: utc(2024, 12, 21, 8, 4), set: utc(2024, 12, 21, 15, 54)},
		{name: "new york equinox", day: utc(2024, 3, 20, 12, 0), lat: 40.7128, lng: -74.0060, rise: utc(2024, 3, 20, 11, 3), set: utc(2024, 3, 20, 23, 14)},
		{name: "sydney midwinter", day: utc(2024, 6, 21, 12, 0), lat: -33.8688, lng: 151.2093, rise: utc(2024, 6, 20, 20, 59), set: utc(2024, 6, 21, 6, 54)},
		{name: "svalbard polar night", day: utc(2024, 12, 21, 12, 0), lat: 78.2, lng: 15.6, polarNightWant: true},
		{name: "svalbard midnight sun", day: utc(2024, 6, 21, 12, 0), lat: 78.2, lng: 15.6, polarDayWant: true},
	} {
		st := sunTimesFor(tc.day, tc.lat, tc.lng, sunriseAltitude)
		if st.polarDay != tc.polarDayWant || st.polarNight != tc.polarNightWant {
			t.Errorf("%s: polar day/night = %v/%v, want %v/%v", tc.name, st.polarDay, st.polarNight, tc.polarDayWant, tc.polarNightWant)
			continue
		}
		if tc.polarDayWant || tc.polarNightWant {
			continue
		}
		if d := st.rise.Sub(tc.rise).Abs(); d > tolerance {
			t.Errorf("%s: sunrise %s, want %s (off by %v)", tc.name, st.rise.Format(time.TimeOnly), tc.rise.Format(time.TimeOnly), d)
		}
		if d := st.set.Sub(tc.set).Abs(); d > tolerance {
			t.Errorf("%s: sunset %s, want %s (off by %v)", tc.name, st.set.Format(time.TimeOnly), tc.set.Format(time.TimeOnly), d)
		}
	}
}

// Civil twilight times (UTC) from an independent evaluation (Python, the
// Meeus low-precision solar position, root-found at -6 degrees), the same
// tolerance as the sunrise cases.
func TestCivilTwilightAgainstKnownValues(t *testing.T) {
	const tolerance = 8 * time.Minute
	for _, tc := range []struct {
		name       string
		day        time.Time
		lat, lng   float64
		dawn, dusk time.Time
	}{
		{"copenhagen midsummer", utc(2024, 6, 21, 12, 0), 55.6761, 12.5683, utc(2024, 6, 21, 1, 25), utc(2024, 6, 21, 20, 59)},
		{"london midwinter", utc(2024, 12, 21, 12, 0), 51.5074, -0.1278, utc(2024, 12, 21, 7, 24), utc(2024, 12, 21, 16, 35)},
		{"new york equinox", utc(2024, 3, 20, 12, 0), 40.7128, -74.0060, utc(2024, 3, 20, 10, 32), utc(2024, 3, 20, 23, 37)},
		{"sydney midwinter", utc(2024, 6, 21, 12, 0), -33.8688, 151.2093, utc(2024, 6, 20, 20, 33), utc(2024, 6, 21, 7, 22)},
	} {
		st := sunTimesFor(tc.day, tc.lat, tc.lng, civilTwilightAltitude)
		if d := st.rise.Sub(tc.dawn).Abs(); d > tolerance {
			t.Errorf("%s: dawn %s, want %s (off by %v)", tc.name, st.rise.Format(time.TimeOnly), tc.dawn.Format(time.TimeOnly), d)
		}
		if d := st.set.Sub(tc.dusk).Abs(); d > tolerance {
			t.Errorf("%s: dusk %s, want %s (off by %v)", tc.name, st.set.Format(time.TimeOnly), tc.dusk.Format(time.TimeOnly), d)
		}
	}
}

// The phase flips exactly at civil dawn and dusk, not at sunrise and
// sunset.
func TestPhaseFlipsAtTheBoundaries(t *testing.T) {
	day := utc(2024, 3, 20, 12, 0)
	loc := mustLocation(t, 51.5074, -0.1278)
	st := sunTimesFor(day, loc.latitude, loc.longitude, civilTwilightAltitude)
	if got := PhaseAt(st.rise.Add(-time.Second), loc); got != PhaseNight {
		t.Errorf("a second before dawn = %v, want night", got)
	}
	if got := PhaseAt(st.rise, loc); got != PhaseDay {
		t.Errorf("at dawn = %v, want day (inclusive)", got)
	}
	if got := PhaseAt(st.set, loc); got != PhaseNight {
		t.Errorf("at dusk = %v, want night (exclusive)", got)
	}
	sunrise := sunTimesFor(day, loc.latitude, loc.longitude, sunriseAltitude).rise
	if got := PhaseAt(sunrise.Add(-time.Minute), loc); got != PhaseDay {
		t.Errorf("a minute before sunrise = %v, want day: civil dawn has passed", got)
	}
}

// The solar day decides, not the UTC date: a Sydney morning and a
// Honolulu evening both straddle UTC midnight, and read as night when
// the UTC date's pair is taken for the day in progress.
func TestPhaseFollowsTheSolarDayAcrossUTCMidnight(t *testing.T) {
	for _, tc := range []struct {
		name string
		now  time.Time
		loc  Location
		want Phase
	}{
		{"sydney morning, late on the UTC date", utc(2024, 6, 21, 22, 0), mustLocation(t, -33.8688, 151.2093), PhaseDay},
		{"sydney afternoon, early on the UTC date", utc(2024, 6, 21, 3, 0), mustLocation(t, -33.8688, 151.2093), PhaseDay},
		{"sydney night", utc(2024, 6, 21, 12, 0), mustLocation(t, -33.8688, 151.2093), PhaseNight},
		{"honolulu afternoon, early on the UTC date", utc(2024, 6, 22, 3, 0), mustLocation(t, 21.3069, -157.8583), PhaseDay},
		{"honolulu night", utc(2024, 6, 22, 10, 0), mustLocation(t, 21.3069, -157.8583), PhaseNight},
	} {
		if got := PhaseAt(tc.now, tc.loc); got != tc.want {
			t.Errorf("%s: phase = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestJulianConversion(t *testing.T) {
	// J2000.0 is 2000-01-01 12:00 TT; the formula treats it as UTC.
	got, ok := julianToUTC(2_451_545.0)
	if !ok || !got.Equal(utc(2000, 1, 1, 12, 0)) {
		t.Errorf("J2000 = %v, %v", got, ok)
	}
	if _, ok := julianToUTC(nan()); ok {
		t.Error("NaN converted to a time")
	}
}

func nan() float64 {
	zero := 0.0
	return zero / zero
}
