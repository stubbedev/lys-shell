package bar

import (
	"errors"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/recorder"
)

// recorderLabel is helpers.rs's build_label: Idle/Replay armed/Paused/
// Recording for {{ state }}, the H:MM:SS (or M:SS) clock for
// {{ elapsed }}, a dash while idle.
func recorderLabel(format string, snap recorder.Change) string {
	return jinja.RenderOr(format, recorderVars(snap))
}

func recorderVars(snap recorder.Change) map[string]any {
	state := i18n.T("bar-recorder-idle")
	switch {
	case snap.Active && snap.Paused:
		state = i18n.T("bar-recorder-paused")
	case snap.Active:
		state = i18n.T("bar-recorder-recording")
	case snap.Replay == recorder.ReplayArmed:
		state = i18n.T("bar-recorder-replay")
	}
	elapsed := "-"
	if snap.Active {
		elapsed = recorder.FormatElapsed(snap.ElapsedSecs)
	}
	return map[string]any{"state": state, "elapsed": elapsed}
}

// recorderIconName is helpers.rs's select_icon over the states: a
// recording wins over an armed replay.
func recorderIconName(cfg config.RecorderConfig, snap recorder.Change) string {
	switch {
	case snap.Active && snap.Paused:
		return cfg.Icons()[config.RecorderPaused].Name
	case snap.Active, snap.Preparing:
		return cfg.Icons()[config.RecorderRecording].Name
	case snap.Replay == recorder.ReplayArmed:
		return cfg.Icons()[config.RecorderReplay].Name
	}
	return cfg.Icons()[config.RecorderIdle].Name
}

// recorderModule is the screen-recorder bell: state and elapsed clock
// from the shared recorder service.
type recorderModule struct {
	ctx  ModuleContext
	src  *recorder.State
	btn  *barButton
	icon *widget.Icon
	// chrome is the module root the preparing class goes on.
	chrome moduleChrome
}

func newRecorder(ctx ModuleContext) (Module, error) {
	if ctx.Recorder == nil {
		return nil, errors.New("recorder: no recorder service available")
	}
	m := &recorderModule{ctx: ctx, src: ctx.Recorder}
	m.icon = moduleIcon(ctx, ctx.Config.Recorder.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	m.refresh()
	// Follow the shared state; headless construction refreshes inline.
	changes, stop := m.src.Changes()
	follow(m.ctx, changes, stop, func(recorder.Change) { m.refresh() })
	return m, nil
}

// refresh applies the state to label and icon.
func (m *recorderModule) refresh() {
	cfg := m.ctx.Config.Recorder
	snap := m.src.Snapshot()
	text := recorderLabel(cfg.Format, snap)
	m.btn.SetLabel(text)
	if setter := m.icon; setter != nil {
		setter.SetThemeName(recorderIconName(cfg, snap))
	}
	// The pre-capture delay pulses the icon through the module root's
	// recorder-preparing class.
	setClass(m.chrome, "recorder-preparing", snap.Preparing)
	setClass(m.chrome, "recorder-replay", snap.Replay == recorder.ReplayArmed)
	// A failed start or a recording that died stays visible until the
	// next start: recorder-failed tints the icon, the tooltip says why.
	failed := recorderFailure(snap)
	setClass(m.chrome, "recorder-failed", failed != "")
	m.btn.SetDescription(failed)
}

// recorderFailure is the error the idle module shows, "" for none.
func recorderFailure(snap recorder.Change) string {
	if snap.Status != recorder.StatusIdle {
		return ""
	}
	return snap.Error
}

func (m *recorderModule) setChrome(c moduleChrome) {
	m.chrome = c
	m.refresh()
}

func (m *recorderModule) Root() widget.Widget { return m.btn }
