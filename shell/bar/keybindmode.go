package bar

import (
	"errors"
	"log"

	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/lyswm"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/hyprland"
)

// keybindModeLabel is helpers.rs's format_label: an empty submap
// renders the "default" vocabulary word through the mode placeholder.
func keybindModeLabel(format, submap string) string {
	return jinja.RenderOr(format, keybindModeVars(submap))
}

func keybindModeVars(submap string) map[string]any {
	mode := submap
	if mode == "" {
		mode = i18n.T("bar-keybind-mode-default")
	}
	return map[string]any{"mode": mode}
}

// keybindModeVisible is helpers.rs's compute_visibility: auto-hide
// collapses the module on the default submap.
func keybindModeVisible(submap string, autoHide bool) bool {
	return !autoHide || submap != ""
}

// keybindMode is the module: the compositor's active submap.
type keybindModeModule struct {
	ctx    ModuleContext
	btn    *barButton
	icon   *widget.Icon
	chrome moduleChrome
	submap string
}

var errKeybindModeUnsupported = errors.New("keybind-mode: the compositor is neither Hyprland nor lys-wm")

func newKeybindMode(ctx ModuleContext) (Module, error) {
	if ctx.Hyprland == nil && detectCompositor() != compositorLysWM {
		return nil, errKeybindModeUnsupported
	}
	m := &keybindModeModule{ctx: ctx}
	m.icon = moduleIcon(ctx, ctx.Config.KeybindMode.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	m.render()
	mountOffLoop(ctx, func() {
		submaps, stop, err := submapSource(ctx)
		if err != nil {
			log.Printf("keybind-mode: %v", err)
			return
		}
		follow(ctx, submaps, stop, func(submap string) {
			m.submap = submap
			m.render()
		})
	})
	return m, nil
}

func submapSource(ctx ModuleContext) (<-chan string, func(), error) {
	life := ctx.Life()
	switch {
	case ctx.Hyprland != nil:
		events, err := ctx.Hyprland.Events(life)
		if err != nil {
			return nil, nil, err
		}
		submaps := make(chan string)
		go func() {
			defer close(submaps)
			for event := range events {
				if event.Kind != hyprland.EventSubmap {
					continue
				}
				select {
				case submaps <- event.Name:
				case <-life.Done():
					return
				}
			}
		}()
		return submaps, nil, nil
	case detectCompositor() == compositorLysWM:
		w, err := lyswm.WatchState(lyswm.SubmapState)
		if err != nil {
			return nil, nil, err
		}
		submaps := make(chan string)
		go func() {
			defer close(submaps)
			for {
				select {
				case submaps <- w.Value():
				case <-life.Done():
					return
				}
				if _, ok := <-w.Ticks(); !ok {
					return
				}
			}
		}()
		return submaps, w.Close, nil
	}
	return nil, nil, errKeybindModeUnsupported
}

// render applies the current submap to the label and, under auto-hide,
// the module root's visibility.
func (m *keybindModeModule) render() {
	cfg := m.ctx.Config.KeybindMode
	text := keybindModeLabel(cfg.Format, m.submap)
	m.btn.SetLabel(text)
	if m.chrome != nil {
		m.chrome.SetVisible(keybindModeVisible(m.submap, cfg.AutoHide))
	}
}

func (m *keybindModeModule) setChrome(c moduleChrome) {
	m.chrome = c
	m.render()
}

func (m *keybindModeModule) Root() widget.Widget { return m.btn }
