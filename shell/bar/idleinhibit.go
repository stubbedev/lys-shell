package bar

import (
	"log"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/idle/idlepolicy"
	"github.com/stubbedev/lys-desktop/service/idleinhibit/inhibitduration"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/idle"
	"github.com/stubbedev/lys-shell/service/idleinhibit"
)

// idleInhibitLabel is helpers.rs's build_label: On/Off state, the
// remaining time (H:MM:SS or M:SS, "∞" when indefinite, "-" when
// off), and the stored duration ("∞" when indefinite).
func idleInhibitLabel(format string, active bool, duration inhibitduration.Duration, remainingSecs uint32) string {
	return jinja.RenderOr(format, idleInhibitVars(active, duration, remainingSecs))
}

func idleInhibitVars(active bool, duration inhibitduration.Duration, remainingSecs uint32) map[string]any {
	state := i18n.T("bar-idle-inhibit-off")
	if active {
		state = i18n.T("bar-idle-inhibit-on")
	}
	var remaining string
	switch {
	case !active:
		remaining = "-"
	case duration.Indefinite():
		remaining = "∞"
	default:
		remaining = idleinhibit.FormatDuration(remainingSecs)
	}
	total := "∞"
	if !duration.Indefinite() {
		total = strconv.FormatUint(uint64(duration.Minutes()), 10)
	}
	return map[string]any{"state": state, "remaining": remaining, "duration": total}
}

// idleInhibitModule is the module: the shared inhibit state rendered
// into the bar. The Wayland inhibitor itself is the bar's idleHolder,
// so idle is held with or without this module in the layout.
type idleInhibitModule struct {
	ctx   ModuleContext
	state *idleinhibit.State
	btn   *barButton
	icon  *widget.Icon
}

func newIdleInhibit(ctx ModuleContext) (Module, error) {
	state := ctx.IdleInhibit
	if state == nil {
		state = idleinhibit.NewState(ctx.Config.IdleInhibit.StartupDuration.Duration())
	}
	m := &idleInhibitModule{ctx: ctx, state: state}
	m.icon = moduleIcon(ctx, ctx.Config.IdleInhibit.Icons()[config.IdleInhibitInactive])
	m.btn = newBarButton(ctx, m.icon, "")
	m.render()
	changes, stop := state.Changes()
	follow(m.ctx, changes, stop, func(struct{}) { m.render() })
	if policy := ctx.Idle; policy != nil {
		changes, stop := policy.Changes()
		follow(m.ctx, changes, stop, func(struct{}) { m.render() })
	}
	return m, nil
}

// render applies the state to label and icon.
func (m *idleInhibitModule) render() {
	cfg := m.ctx.Config.IdleInhibit
	snap := m.state.Status()
	text := idleInhibitLabel(cfg.Format, snap.Active, snap.Duration, snap.RemainingS)
	m.btn.SetLabel(text)
	m.btn.SetDescription(idleTooltip(m.ctx.Idle, m.state))
	if setter := m.icon; setter != nil {
		icon := cfg.Icons()[config.IdleInhibitInactive]
		if snap.Active {
			icon = cfg.Icons()[config.IdleInhibitActive]
		}
		setter.SetThemeName(icon.Name)
	}
}

func (m *idleInhibitModule) Root() widget.Widget { return m.btn }

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

func idleTooltip(policy *idle.Service, state *idleinhibit.State) string {
	if policy == nil {
		return i18n.T("bar-idle-policy-off")
	}
	status := policy.Status()
	if !status.Enabled {
		return i18n.T("bar-idle-policy-off")
	}
	if state.Inhibiting() {
		return i18n.T("bar-idle-held-by", i18n.Str("holders", strings.Join(idleHolders(state), ", ")))
	}
	next, ok := status.Next()
	if !ok {
		return i18n.T("bar-idle-all-reached")
	}
	return i18n.T("bar-idle-next", i18n.Str("stage", i18n.T("bar-idle-stage-"+next.Stage.String())), i18n.Str("after", idlepolicy.FormatDuration(next.After)))
}

func idleHolders(state *idleinhibit.State) []string {
	holds := state.Holds()
	names := make([]string, 0, len(holds)+1)
	if state.Active() {
		names = append(names, i18n.T("bar-idle-held-by-you"))
	}
	ids := slices.Sorted(maps.Keys(holds))
	for _, id := range ids {
		names = append(names, holds[id].App)
	}
	return names
}
