package styling

import (
	"strconv"
	"strings"
)

// TextRole is one step of the type scale every surface sizes its text
// from: a ratio of the desktop's body size (1rem, the desktop font).
type TextRole int

const (
	// Caption is secondary text: descriptions, statuses, section headers.
	Caption TextRole = iota
	// Body is running text, labels and controls.
	Body
	// Subtitle is a card or item heading one step above body.
	Subtitle
	// Title is a page or window heading.
	Title
	// Display is a large readout: a percentage, a temperature, a PIN.
	Display
	// Hero is a full-screen readout: the lock and dropdown clocks.
	Hero
)

// textRoles is the scale: each role's token name and its ratio of body.
var textRoles = [...]struct {
	name  string
	ratio float64
}{
	Caption:  {"caption", 0.875},
	Body:     {"body", 1},
	Subtitle: {"subtitle", 1.125},
	Title:    {"title", 1.375},
	Display:  {"display", 2.25},
	Hero:     {"hero", 4},
}

// TextRoles is every role, smallest first.
func TextRoles() []TextRole {
	roles := make([]TextRole, len(textRoles))
	for i := range roles {
		roles[i] = TextRole(i)
	}
	return roles
}

// Ratio is the role's size relative to body.
func (r TextRole) Ratio() float64 { return textRoles[r].ratio }

// Token is the role's custom property for surfaces scaled by
// --global-scale.
func (r TextRole) Token() string { return "--text-" + textRoles[r].name }

// BarToken is the role's custom property for the bar, scaled by
// --bar-scale.
func (r TextRole) BarToken() string { return "--bar-text-" + textRoles[r].name }

// Px is the role's size in pixels over a body of rem pixels at scale.
func (r TextRole) Px(rem, scale float64) float64 { return rem * r.Ratio() * scale }

// TypeScaleCSS declares every role's tokens on :root: rem times the
// role's ratio times --global-scale, and the bar's twin over
// --bar-scale.
func TypeScaleCSS() string {
	var b strings.Builder
	b.WriteString(":root {\n")
	for _, r := range TextRoles() {
		ratio := strconv.FormatFloat(r.Ratio(), 'f', -1, 64)
		b.WriteString("    " + r.Token() + ": calc(" + ratio + "rem * var(--global-scale));\n")
		b.WriteString("    " + r.BarToken() + ": calc(" + ratio + "rem * var(--bar-scale));\n")
	}
	b.WriteString("}")
	return b.String()
}
