// Package portalreq is what the desktop-portal backend tells a shell
// dialog about the request it serves.
package portalreq

// Caller identifies the portal request behind a shell dialog: Handle is
// the request's object path, the key the services' Dismiss names;
// AppID is the app asking ("" for a host app); ParentWindow is the
// app's window identifier ("wayland:<xdg-foreign handle>", or "");
// Modal is whether the dialog should hold the keyboard until answered
// (the portal's modal option, true unless the app said otherwise).
// Over D-Bus it is the struct (sssb).
type Caller struct {
	Handle, AppID, ParentWindow string
	Modal                       bool
}

// Option is one entry of a Choice: its id and its label, (ss).
type Option struct{ ID, Label string }

// Choice is one of the extra questions a portal dialog may carry (the
// FileChooser's and Access' choices option, (ssa(ss)s)): an id, a label,
// the options, and the initial option ("" lets the dialog pick). No
// options is a yes/no question with the values "true" and "false".
type Choice struct {
	ID, Label string
	Options   []Option
	Initial   string
}

// Boolean reports whether the choice is a yes/no question.
func (c Choice) Boolean() bool { return len(c.Options) == 0 }

// Start is the value the choice starts at: the initial option when it
// is one of the options, else the first (or "false" for a yes/no).
func (c Choice) Start() string {
	if c.Boolean() {
		if c.Initial == "true" {
			return "true"
		}
		return "false"
	}
	for _, o := range c.Options {
		if o.ID == c.Initial {
			return o.ID
		}
	}
	return c.Options[0].ID
}

// Selection is one answered choice, (ss): the choice's id and the
// option picked.
type Selection struct{ ID, Value string }
