package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/pulse"
)

// volumeLabel renders the format; "percent" like the other level
// modules. A muted sink keeps its percent label: mute shows through
// the icon (icon-muted), exactly as methods.rs's update_display.
func volumeLabel(format string, percent float64) string {
	return jinja.RenderOr(format, map[string]any{"percent": int64(math.Round(percent))})
}

// volumePercent is the level the Rust modules display: the channel
// average as a percentage, rounded (average_percentage().round()).
func volumePercent(dev pulse.Device) float64 {
	return math.Round(dev.Volume.AveragePercentage())
}

// volume is the module: the default sink's volume label, refreshed on
// PulseAudio events, scroll to adjust.
type volumeModule struct {
	defaultDeviceChrome
	ctx    ModuleContext
	source pulse.Source
	btn    *barButton
	icon   *widget.Icon
}

func newVolume(ctx ModuleContext) (Module, error) {
	if ctx.App == nil {
		return nil, errors.New("volume: requires the application loop")
	}
	if ctx.Pulse == nil {
		return nil, errors.New("volume: no audio source available")
	}
	m := &volumeModule{ctx: ctx, source: ctx.Pulse}
	m.icon = moduleIcon(ctx, ctx.Config.Volume.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	if err := followPulse(ctx, "volume", m.refresh); err != nil {
		return nil, err
	}
	return m, nil
}

type defaultDeviceChrome struct {
	chrome  moduleChrome
	present bool
}

func (c *defaultDeviceChrome) setChrome(chrome moduleChrome) {
	c.chrome = chrome
	c.show()
}

func (c *defaultDeviceChrome) setPresent(present bool) {
	c.present = present
	c.show()
}

func (c *defaultDeviceChrome) show() {
	if c.chrome != nil {
		c.chrome.SetVisible(c.present)
	}
}

func followPulse(ctx ModuleContext, module string, refresh func() error) error {
	if err := refresh(); err != nil {
		return fmt.Errorf("%s: %w", module, err)
	}
	ticks, stop, err := ctx.Pulse.Subscribe(ctx.Life())
	if err != nil {
		return fmt.Errorf("%s: %w", module, err)
	}
	go func() {
		for range ticks {
			ctx.Invoke(func() {
				if err := refresh(); err != nil {
					log.Printf("%s: refresh: %v", module, err)
				}
			})
		}
		stop()
	}()
	return nil
}

// refresh re-reads and restyles the label and state icon; with no
// default sink the module hides until one appears.
func (m *volumeModule) refresh() error {
	out, err := m.source.DefaultSink(context.Background())
	if errors.Is(err, pulse.ErrNoDefaultDevice) {
		m.setPresent(false)
		return nil
	}
	if err != nil {
		return err
	}
	m.setPresent(true)
	dev := out.Device
	cfg := m.ctx.Config.Volume
	label := ""
	if cfg.LabelShow {
		label = volumeLabel(cfg.Format, volumePercent(dev))
	}
	m.btn.SetLabel(label)
	m.btn.applyThresholds(volumePercent(dev), cfg.Thresholds)
	if setter := m.icon; setter != nil {
		setter.SetThemeName(volumeIconName(cfg, dev))
	}
	return nil
}

// volumeIconName is volume helpers.rs's select_icon: the muted icon
// wins, then the level list spans 1..100 across icons 0..n-1 (0%
// takes the first). The percentage is the rounded average the Rust
// module feeds in.
func volumeIconName(cfg config.VolumeConfig, dev pulse.Device) string {
	if dev.Muted || len(cfg.LevelIcons) == 0 {
		return cfg.IconMuted
	}
	return cfg.LevelIcons[levelIndexSpan(int(volumePercent(dev)), len(cfg.LevelIcons))]
}

func (m *volumeModule) Root() widget.Widget { return m.btn }
