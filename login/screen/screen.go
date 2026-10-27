package screen

import (
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// Mode is the situation the login screen is shown in.
type Mode int

// The modes: the greeter before login, and the lock over a session.
const (
	Greeter Mode = iota
	Lock
)

// GreeterRows are the rows only the greeter shows: the user list above
// the entries, the session picker below them, and the power buttons at
// the bottom of the screen. Any may be nil.
type GreeterRows struct {
	Users, Session, Power widget.Widget
}

// Spec is one login screen surface.
type Spec struct {
	Mode   Mode
	Config config.LoginConfig
	Font   render.Font
	Sheet  *widget.Stylesheet
	// Backdrop is the background image, nil for the color fill.
	Backdrop *Backdrop
	// Greeter holds the greeter's own rows; Build reads it in greeter
	// mode only.
	Greeter GreeterRows
	// CapsLock is the caps lock warning's text, in the mode's locale.
	CapsLock string
	// Focus moves keyboard focus.
	Focus func(widget.Widget)
	// OnSubmit receives the secret entry's text on Enter.
	OnSubmit func(string)
}

// Surface is a built login screen and the handles its mode drives.
type Surface struct {
	Root   widget.Widget
	Prompt *Prompt
	// Scrim is the blank-screen blackout over everything.
	Scrim *widget.Box
}

// Build assembles a login screen: the background, the credential prompt
// with the clock and the caps lock warning, and the blank scrim, the same
// in every mode; the greeter adds its username entry and its rows.
func Build(s Spec) *Surface {
	o := Options{
		Font: s.Font, ShowClock: s.Config.ShowClock, CapsLock: s.CapsLock, Focus: s.Focus,
	}
	var layers []widget.Widget
	if s.Mode == Greeter {
		o.WithUsername = true
		o.Header, o.Extra = s.Greeter.Users, s.Greeter.Session
	}
	surface := &Surface{Prompt: newPrompt(o, s.OnSubmit), Scrim: Scrim()}
	layers = append(layers, Background(s.Backdrop, HexFill(s.Config.Background.Color)), surface.Prompt.Reveal)
	if s.Mode == Greeter && s.Greeter.Power != nil {
		layers = append(layers, s.Greeter.Power)
	}
	layers = append(layers, surface.Scrim)
	surface.Root = Window(s.Sheet, layers...)
	return surface
}
