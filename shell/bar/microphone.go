package bar

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-ui/widget"
)

// microphoneLabel is helpers.rs's format_label: the rounded percentage. A
// muted mic dims via the color, like the Rust icon-muted semantics.
func microphoneLabel(percent float64) string {
	return strconv.Itoa(int(math.Round(percent))) + "%"
}

// microphone is the module: the default input's level.
type microphoneModule struct {
	defaultDeviceChrome
	ctx    ModuleContext
	source pulse.Source
	btn    *barButton
	icon   *widget.Icon
}

func newMicrophone(ctx ModuleContext) (Module, error) {
	if ctx.App == nil {
		return nil, errors.New("microphone: requires the application loop")
	}
	if ctx.Pulse == nil {
		return nil, errors.New("microphone: no audio source available")
	}
	m := &microphoneModule{ctx: ctx, source: ctx.Pulse}
	m.icon = moduleIcon(ctx, ctx.Config.Microphone.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	followReads(ctx, "microphone", &firstRead{}, ctx.Pulse.Subscribe, m.read, m.apply)
	return m, nil
}

type microphoneRead struct {
	dev     pulse.InputDevice
	present bool
}

func (m *microphoneModule) read(ctx context.Context) (microphoneRead, error) {
	dev, err := m.source.DefaultSource(ctx)
	if errors.Is(err, pulse.ErrNoDefaultDevice) {
		return microphoneRead{}, nil
	}
	if err != nil {
		return microphoneRead{}, err
	}
	return microphoneRead{dev, true}, nil
}

// apply restyles the label and state icon; with no default source the
// module hides until one appears.
func (m *microphoneModule) apply(r microphoneRead) {
	m.setPresent(r.present)
	if !r.present {
		return
	}
	dev := r.dev
	cfg := m.ctx.Config.Microphone
	label := microphoneLabel(volumePercent(dev.Device))
	// The thresholds read the unrounded average, as the Rust watcher
	// does (the volume module rounds first).
	m.btn.applyThresholds(dev.Volume.AveragePercentage(), cfg.Thresholds)
	m.btn.SetLabel(label)
	if setter := m.icon; setter != nil {
		name := cfg.IconActive
		if dev.Muted {
			name = cfg.IconMuted
		}
		setter.SetThemeName(name)
	}
}

func (m *microphoneModule) Root() widget.Widget { return m.btn }
