package nightschedule

import (
	"errors"
	"fmt"
	"time"
)

// ErrNullIsland is the coordinates 0, 0: open water no machine sits on,
// and the value an unset coordinate used to default to, so it is never
// taken for a position.
var ErrNullIsland = errors.New("nightlight: the coordinates 0, 0 are not a position")

// Location is a position in decimal degrees, north and east positive.
// NewLocation is the only way to build one, so every Location is on the
// globe and not 0, 0.
type Location struct {
	latitude  float64
	longitude float64
}

// NewLocation validates the coordinates, refusing NaN, anything off the
// globe, and ErrNullIsland.
func NewLocation(latitude, longitude float64) (Location, error) {
	if !(latitude >= -90 && latitude <= 90) {
		return Location{}, fmt.Errorf("nightlight: latitude %v is outside -90 to 90", latitude)
	}
	if !(longitude >= -180 && longitude <= 180) {
		return Location{}, fmt.Errorf("nightlight: longitude %v is outside -180 to 180", longitude)
	}
	if latitude == 0 && longitude == 0 {
		return Location{}, ErrNullIsland
	}
	return Location{latitude: latitude, longitude: longitude}, nil
}

// Latitude is the latitude in decimal degrees, north positive.
func (l Location) Latitude() float64 { return l.latitude }

// Longitude is the longitude in decimal degrees, east positive.
func (l Location) Longitude() float64 { return l.longitude }

// MaxTransitionMinutes bounds an ease: past two hours it stops reading
// as a transition at a solar event.
const MaxTransitionMinutes = 120

// Transition is how long an ease between day and night lasts, in whole
// minutes, 0 for an instant switch. NewTransition is the only way to a
// non-zero one, so it never exceeds MaxTransitionMinutes.
type Transition struct{ minutes uint32 }

// NewTransition validates minutes against 0..MaxTransitionMinutes.
func NewTransition(minutes int64) (Transition, error) {
	if minutes < 0 || minutes > MaxTransitionMinutes {
		return Transition{}, fmt.Errorf("nightlight: transition of %d minutes is outside 0-%d", minutes, MaxTransitionMinutes)
	}
	return Transition{minutes: uint32(minutes)}, nil
}

// Minutes is the length in minutes.
func (t Transition) Minutes() uint32 { return t.minutes }

// Duration is the length as a duration.
func (t Transition) Duration() time.Duration { return time.Duration(t.minutes) * time.Minute }
