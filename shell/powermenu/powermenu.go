// Package powermenu is the native power menu (shell/power_menu): a
// full-screen dimmed overlay with a centered pane of frosted glass
// holding lock, log out, suspend, reboot and shut-down buttons, each
// running its [modules.power] command. The power module's `:menu`
// binding opens it; the left and right arrows walk the buttons and
// Escape dismisses it. It enters and leaves through the power
// surface's transition, and a button's command runs once the menu is
// gone.
package powermenu

import (
	"log"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/shell/reveal"
	"github.com/stubbedev/lys-desktop/shell/widgets"
	"github.com/stubbedev/lys-desktop/styling"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"
)

// iconPx is an action glyph's size.
const iconPx = 24

// escape dismisses the menu; left and right walk its buttons.
var (
	escape, _ = app.ParseAccel("Escape")
	left, _   = app.ParseAccel("Left")
	right, _  = app.ParseAccel("Right")
)

// action is one button: its icon, label, command and visibility, and
// whether it ends the session.
type action struct {
	icon, label string
	danger      bool
	command     func(config.PowerConfig) string
	visible     func(config.PowerConfig) bool
}

// actions are ACTIONS, in their order.
var actions = []action{
	{"ld-lock-symbolic", "Lock", false, func(c config.PowerConfig) string { return c.LockCommand }, func(c config.PowerConfig) bool { return c.ShowLock }},
	{"ld-log-out-symbolic", "Log out", true, func(c config.PowerConfig) string { return c.LogoutCommand }, func(c config.PowerConfig) bool { return c.ShowLogout }},
	{"ld-moon-symbolic", "Suspend", false, func(c config.PowerConfig) string { return c.SuspendCommand }, func(c config.PowerConfig) bool { return c.ShowSuspend }},
	{"ld-rotate-ccw-symbolic", "Reboot", true, func(c config.PowerConfig) string { return c.RebootCommand }, func(c config.PowerConfig) bool { return c.ShowReboot }},
	{"ld-power-symbolic", "Shut down", true, func(c config.PowerConfig) string { return c.ShutdownCommand }, func(c config.PowerConfig) bool { return c.ShowShutdown }},
}

// Window is the overlay surface the menu maps (app.LayerWindow).
type Window interface {
	Close()
	SetFocus(widget.Widget) error
}

// Deps are the menu's collaborators.
type Deps struct {
	// Config is the live config; commands are read on click.
	Config func() *config.Config
	// Open maps the overlay.
	Open func(cfg app.LayerConfig) (Window, error)
	// Run runs a command without waiting for it (spawn.Quiet).
	Run func(cmd string)
	// Font shapes the labels; Sheet (the power-menu-* rules) styles
	// the overlay.
	Font  render.Font
	Sheet *widget.Stylesheet
}

// Menu is the power menu. Its methods run on the UI loop.
type Menu struct {
	d   Deps
	win Window
	rev *widget.Revealer
	// buttons are the shown actions, in order; focused indexes the one
	// with the keyboard, -1 before the first arrow.
	buttons []*widget.Button
	focused int
	// closing: the exit is playing; a Show reopens a fresh surface.
	closing bool
}

// New builds the menu; nothing maps until Show.
func New(d Deps) *Menu { return &Menu{d: d} }

// Show opens the menu with the current config's buttons, or does
// nothing while it is already up.
func (m *Menu) Show() {
	if m.win != nil && !m.closing {
		return
	}
	cfg := m.d.Config()
	th := widget.Current()
	row := widget.NewBox(widget.Row, th.Spacing, 3*th.Padding)
	row.AddClass(css.Glass, "power-menu-surface")
	row.SetHomogeneous(true)
	m.buttons, m.focused = m.buttons[:0], -1
	for _, a := range actions {
		if !a.visible(cfg.Power) {
			continue
		}
		b := m.button(a)
		m.buttons = append(m.buttons, b)
		row.Append(b, false)
	}
	m.rev = widget.NewRevealer(widgets.Center(row))
	root := widget.NewBox(widget.Column, 0, 0)
	root.AddClass("power-menu-window")
	if m.d.Sheet != nil {
		root.AttachStylesheet(m.d.Sheet)
	}
	root.Append(m.rev, true)
	win, err := m.d.Open(app.LayerConfig{
		Layer:          app.LayerOverlay,
		Anchor:         app.AnchorTop | app.AnchorBottom | app.AnchorLeft | app.AnchorRight,
		ExclusiveZone:  -1,
		Keyboard:       app.KeyboardExclusive,
		Namespace:      "lys-shell-power-menu",
		Root:           root,
		BackgroundBlur: cfg.Styling.Translucent(),
		KeyCapture: func(a app.Accel) bool {
			switch a.Sym { // whatever modifiers are held
			case escape.Sym:
				m.Cancel()
			case left.Sym:
				m.step(-1)
			case right.Sym:
				m.step(1)
			default:
				return false
			}
			return true
		},
	})
	if err != nil {
		log.Printf("power menu: cannot map the overlay: %v", err)
		return
	}
	m.win, m.closing = win, false
	reveal.Show(m.rev, cfg.Animations, config.AnimPower)
}

// step moves the keyboard along the buttons, wrapping at the ends. The
// first step lands on the end it walks from.
func (m *Menu) step(by int) {
	if m.win == nil || m.closing || len(m.buttons) == 0 {
		return
	}
	n := len(m.buttons)
	if m.focused < 0 && by < 0 {
		m.focused = 0
	}
	m.focused = (m.focused + by + n) % n
	if err := m.win.SetFocus(m.buttons[m.focused]); err != nil {
		log.Printf("power menu: focus a button: %v", err)
	}
}

const (
	// buttonPx is an action tile's side.
	buttonPx = 96
	// chipPx is the side of the disc behind an action's glyph.
	chipPx = 48
)

// button is one action: a borderless square tile with the glyph on a
// disc over the label, its command re-read on click.
func (m *Menu) button(a action) *widget.Button {
	icon := widget.NewThemeIcon(a.icon, iconPx)
	icon.AddClass("power-menu-icon")
	label := widget.NewLabel(m.d.Font, widgets.TextPx(styling.Body, 1), a.label, 0)
	label.AddClass("power-menu-label", css.DimLabel)
	label.SetAlignment(render.AlignCenter)
	content := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	content.SetVExpand(true)
	chip := widget.NewBox(widget.Column, 0, 0)
	chip.AddClass("power-menu-chip")
	chip.SetSizeRequest(chipPx, chipPx)
	chip.Append(widgets.Center(icon), true)
	content.AppendAligned(chip, false, widget.AlignCenter)
	content.Append(label, false)
	b := widget.NewButton(widgets.Center(content), 0, 0)
	b.AddClass("power-menu-button", css.Flat)
	if a.danger {
		b.AddClass("danger")
	}
	b.SetSizeRequest(buttonPx, buttonPx)
	b.OnClick = func() { m.Run(a.command(m.d.Config().Power)) }
	return b
}

// Run plays the exit, closes the menu, then runs cmd (an empty one
// runs nothing).
func (m *Menu) Run(cmd string) { m.hide(cmd) }

// Cancel plays the exit and closes the menu.
func (m *Menu) Cancel() { m.hide("") }

func (m *Menu) hide(then string) {
	if m.win == nil || m.closing {
		return
	}
	m.closing = true
	win := m.win
	reveal.Hide(m.rev, m.d.Config().Animations, config.AnimPower, func() {
		win.Close()
		if m.win == win {
			m.win, m.rev, m.closing = nil, nil, false
		}
		if then != "" {
			m.d.Run(then)
		}
	})
}

// Open reports whether the menu is up (its exit not begun).
func (m *Menu) Open() bool { return m.win != nil && !m.closing }
