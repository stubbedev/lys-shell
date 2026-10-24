package bar

import (
	"log"
	"slices"
	"strconv"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
)

// idleInhibitLabel is helpers.rs's build_label: On/Off state, the
// remaining time (H:MM:SS or M:SS, "∞" when indefinite, "-" when
// off), and the stored duration ("∞" when indefinite).
func idleInhibitLabel(format string, active bool, durationMins uint32, remainingSecs int) string {
	state := i18n.T("bar-idle-inhibit-off")
	if active {
		state = i18n.T("bar-idle-inhibit-on")
	}
	var remaining string
	switch {
	case !active:
		remaining = "-"
	case durationMins == 0:
		remaining = "∞"
	default:
		remaining = idleinhibit.FormatDuration(remainingSecs)
	}
	duration := "∞"
	if durationMins != 0 {
		duration = strconv.FormatUint(uint64(durationMins), 10)
	}
	return jinja.RenderOr(format, map[string]any{"state": state, "remaining": remaining, "duration": duration})
}

// idleInhibitModule is the module: the shared inhibit state rendered
// into the bar. The Wayland inhibitor itself is the bar's idleHolder,
// so idle is held with or without this module in the layout.
type idleInhibitModule struct {
	ctx   ModuleContext
	state *idleinhibit.State
	label *widget.Label
	icon  *widget.Icon
	root  widget.Widget
}

func newIdleInhibit(ctx ModuleContext) (Module, error) {
	state := ctx.IdleInhibit
	if state == nil {
		state = idleinhibit.NewState(ctx.Config.IdleInhibit.StartupDuration)
	}
	m := &idleInhibitModule{ctx: ctx, state: state, label: widget.NewLabel(ctx.Font, ctx.Style.labelPx, "", ctx.Style.fg)}
	m.icon = moduleIcon(ctx, ctx.Config.IdleInhibit.Icons()[config.IdleInhibitInactive])
	m.root = assembleModule(ctx, m.icon, m.label)
	m.render()
	changes, stop := state.Changes()
	follow(m.ctx, changes, stop, func(struct{}) { m.render() })
	return m, nil
}

// render applies the state to label and icon.
func (m *idleInhibitModule) render() {
	cfg := m.ctx.Config.IdleInhibit
	snap := m.state.Status()
	text := ""
	if cfg.LabelShow {
		text = idleInhibitLabel(cfg.Format, snap.Active, snap.DurationMins, snap.RemainingS)
	}
	m.label.SetText(text)
	if setter := m.icon; setter != nil {
		icon := cfg.Icons()[config.IdleInhibitInactive]
		if snap.Active {
			icon = cfg.Icons()[config.IdleInhibitActive]
		}
		setter.SetThemeName(icon.Name)
	}
}

func (m *idleInhibitModule) Root() widget.Widget { return m.root }

// idleHolder keeps one Wayland idle inhibitor alive while the shared
// state inhibits (the user's toggle or an app's hold), bound to an open
// bar surface and moved to another when that bar closes. Loop goroutine
// only.
type idleHolder struct {
	state *idleinhibit.State
	// inhibit binds an inhibitor to a host (Application.InhibitIdle).
	inhibit func(app.Host) (func(), error)
	hosts   []app.Host
	host    app.Host
	release func()
}

// add offers an open bar surface.
func (h *idleHolder) add(host app.Host) {
	h.hosts = append(h.hosts, host)
	h.sync()
}

// remove withdraws a bar surface before it closes.
func (h *idleHolder) remove(host app.Host) {
	h.hosts = slices.DeleteFunc(h.hosts, func(o app.Host) bool { return o == host })
	if h.host == host {
		h.drop()
	}
	h.sync()
}

// sync matches the inhibitor to the state.
func (h *idleHolder) sync() {
	if !h.state.Inhibiting() {
		h.drop()
		return
	}
	if h.release != nil || len(h.hosts) == 0 {
		return
	}
	release, err := h.inhibit(h.hosts[0])
	if err != nil {
		log.Printf("idle-inhibit: cannot inhibit idle: %v", err)
		return
	}
	h.host, h.release = h.hosts[0], release
}

func (h *idleHolder) drop() {
	if h.release != nil {
		h.release()
	}
	h.host, h.release = nil, nil
}
