package bar

import (
	"errors"
	"math"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/upower"
	"github.com/stubbedev/lys-shell/styling"
)

// batteryLabel renders the format template: the only variable the Rust
// module's context carries is "percent" (rounded), and the braces are
// whitespace-insensitive. No battery maps to the unavailable label.
func batteryLabel(format string, percentage float64, present bool) string {
	if !present {
		return i18n.T("bar-battery-unavailable")
	}
	return jinja.RenderOr(format, map[string]any{"percent": int64(math.Round(percentage))})
}

// thresholdColor returns the label-color override the thresholds
// resolve for value (evaluate_thresholds: last match wins).
func thresholdColor(value float64, thresholds []config.ThresholdEntry, palette *styling.Palette) (render.Color, bool) {
	return resolveOverride(config.EvaluateThresholds(value, thresholds).LabelColor, palette)
}

// resolveOverride resolves an optional color override.
func resolveOverride(cv *config.ColorValue, palette *styling.Palette) (render.Color, bool) {
	if cv == nil {
		return 0, false
	}
	return styling.ResolveColor(*cv, palette)
}

// battery is the module: a label fed from the UPower device snapshot,
// refreshed on PropertiesChanged.
type battery struct {
	ctx    ModuleContext
	source upower.Source
	btn    *barButton
	icon   *widget.Icon
}

func newBattery(ctx ModuleContext) (Module, error) {
	if ctx.App == nil {
		// Headless construction has no loop to subscribe on.
		return nil, errors.New("battery: requires the application loop")
	}
	if ctx.Battery == nil {
		return nil, errors.New("battery: no battery source available")
	}
	m := &battery{ctx: ctx, source: ctx.Battery}
	m.icon = moduleIcon(ctx, ctx.Config.Battery.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	if _, err := followReads(ctx, "battery", m.source.Subscribe, m.source.Read, m.apply); err != nil {
		return nil, err
	}
	return m, nil
}

// apply updates the label and state icon for the device.
func (m *battery) apply(dev upower.Device) {
	cfg := m.ctx.Config.Battery
	label := ""
	if cfg.LabelShow {
		label = batteryLabel(cfg.Format, dev.Percentage, dev.IsPresent)
	}
	m.btn.applyThresholds(dev.Percentage, cfg.Thresholds)
	m.btn.SetLabel(label)
	m.setIcon(cfg, dev)
}

// setIcon follows battery helpers.rs's select_icon: alert when absent
// or unknown, charging when charging or pending charge, else the
// level list bucketed by percentage.
func (m *battery) setIcon(cfg config.BatteryConfig, dev upower.Device) {
	setter := m.icon
	if setter == nil {
		return
	}
	var name string
	switch {
	case !dev.IsPresent || dev.State == upower.StateUnknown:
		name = cfg.AlertIcon
	case dev.State == upower.StateCharging || dev.State == upower.StatePendingCharge:
		name = cfg.ChargingIcon
	case len(cfg.LevelIcons) == 0:
		name = cfg.AlertIcon
	default:
		name = cfg.LevelIcons[levelIndexFloor(dev.Percentage, len(cfg.LevelIcons))]
	}
	setter.SetThemeName(name)
}

func (m *battery) Root() widget.Widget { return m.btn }
