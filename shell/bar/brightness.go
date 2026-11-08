package bar

import (
	"errors"
	"math"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/brightness"
)

// brightnessNoDevice is NO_DEVICE_LABEL.
const brightnessNoDevice = "--%"

// brightnessLabel is helpers.rs's format_label: the one variable is
// the rounded percent.
func brightnessLabel(format string, percent float64) string {
	return jinja.RenderOr(format, percentVars(percent))
}

func percentVars(percent float64) map[string]any {
	return map[string]any{"percent": int64(math.Round(percent))}
}

// brightnessIcon is select_icon: level-icons split 0-100% into equal
// bands, the last one closing at 100%; no icons is "".
func brightnessIcon(levels []string, percent float64) string {
	if len(levels) == 0 {
		return ""
	}
	i := int(math.Floor(percent / 100 * float64(len(levels))))
	return levels[max(0, min(i, len(levels)-1))]
}

// brightnessModule is the bar button: the mean percentage across
// backlights with its level icon, refreshed on sysfs changes.
type brightnessModule struct {
	ctx    ModuleContext
	source brightness.Source
	btn    *barButton
	icon   *widget.Icon
	firstRead
}

func newBrightness(ctx ModuleContext) (Module, error) {
	if ctx.Brightness == nil {
		return nil, errors.New("brightness: no backlight source available")
	}
	cfg := ctx.Config.Brightness
	m := &brightnessModule{ctx: ctx, source: ctx.Brightness}
	m.icon = moduleIcon(ctx, config.IconWith(cfg.IconShow, brightnessIcon(cfg.LevelIcons, 0), cfg.IconColor))
	m.btn = newBarButton(ctx, m.icon, "")
	followReads(ctx, "brightness", &m.firstRead, ctx.Brightness.Subscribe, m.source.Devices, m.apply)
	return m, nil
}

// apply is refresh_display: the label and icon for the mean level
// and its threshold colors, or "--%" and the first icon without a
// device.
func (m *brightnessModule) apply(devices []brightness.Device) {
	cfg := m.ctx.Config.Brightness
	percent, ok := brightness.AveragePercentage(devices)
	if !ok {
		m.btn.SetLabel(brightnessNoDevice)
		m.setIcon(brightnessIcon(cfg.LevelIcons, 0))
		return
	}
	m.btn.SetLabel(brightnessLabel(cfg.Format, percent))
	m.setIcon(brightnessIcon(cfg.LevelIcons, percent))
	m.btn.applyThresholds(percent, cfg.Thresholds)
}

func (m *brightnessModule) setIcon(name string) {
	if m.icon != nil && name != "" {
		m.icon.SetThemeName(name)
	}
}

func (m *brightnessModule) Root() widget.Widget { return m.btn }
