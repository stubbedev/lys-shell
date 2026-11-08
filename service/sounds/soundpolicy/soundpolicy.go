package soundpolicy

// Mode is which notifications make a sound.
type Mode uint8

// The modes.
const (
	Off Mode = iota
	Critical
	All
)

// Policy is the sound configuration.
type Policy struct {
	Mode Mode
	// Default is the theme sound a notification without a sound hint
	// plays, and what a sound name the theme lacks falls back to.
	Default string
	// MuteApps are app-name or desktop-entry globs that never sound.
	MuteApps []string
	// Volume is the sounds' linear level, 1 playing them as recorded.
	Volume float64
}
