package bar

import (
	"errors"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/lyswm"
)

// keybindModeLabel is helpers.rs's format_label: an empty submap
// renders the "default" vocabulary word through the mode placeholder.
func keybindModeLabel(format, submap string) string {
	mode := submap
	if mode == "" {
		mode = i18n.T("bar-keybind-mode-default")
	}
	return jinja.RenderOr(format, map[string]any{"mode": mode})
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

func newKeybindMode(ctx ModuleContext) (Module, error) {
	submaps, stop, err := submapSource(ctx)
	if err != nil {
		return nil, err
	}
	m := &keybindModeModule{ctx: ctx}
	m.icon = moduleIcon(ctx, ctx.Config.KeybindMode.Icon())
	m.btn = newBarButton(ctx, m.icon, "")
	m.render()
	follow(ctx, submaps, stop, func(submap string) {
		m.submap = submap
		m.render()
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
		w, err := lyswm.Watch(lyswm.EventSubmap)
		if err != nil {
			return nil, nil, err
		}
		submaps := make(chan string)
		go func() {
			defer close(submaps)
			for {
				select {
				case submaps <- w.Submap():
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
	return nil, nil, errors.New("keybind-mode: the compositor is neither Hyprland nor lys-wm")
}

// render applies the current submap to the label and, under auto-hide,
// the module root's visibility.
func (m *keybindModeModule) render() {
	cfg := m.ctx.Config.KeybindMode
	text := ""
	if cfg.LabelShow {
		text = keybindModeLabel(cfg.Format, m.submap)
	}
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
