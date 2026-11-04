package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/hyprland"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/mango"
	"github.com/stubbedev/lys-shell/service/niri"
	"github.com/stubbedev/lys-shell/service/sway"
)

// The Hyprland source logic from keyboard_input/sources/hyprland.rs:
// Hyprland names the throwaway keyboards that wtype and friends create
// for a single synthetic keystroke, and reports placeholder keymaps
// while a keyboard has no usable xkb state.
const (
	virtualKeyboardPrefix = "hl-virtual-keyboard-"
	// placeholder keymaps.
	noneKeymap  = "none"
	errorKeymap = "error"
)

// unknownLayout is component.rs's UNKNOWN_LAYOUT: the layout shown
// while the source knows none, or could not be read at start.
const unknownLayout = "?"

// mainKeyboardLayout picks the active layout: the main non-virtual
// keyboard's keymap, else the first non-virtual keyboard with a real
// keymap. Empty string when no keyboard qualifies.
func mainKeyboardLayout(devices hyprland.Devices) string {
	fallback := ""
	for _, keyboard := range devices.Keyboards {
		if strings.HasPrefix(keyboard.Name, virtualKeyboardPrefix) {
			continue
		}
		if keyboard.Main && keyboard.ActiveKeymap != noneKeymap && keyboard.ActiveKeymap != errorKeymap {
			return keyboard.ActiveKeymap
		}
		if fallback == "" && keyboard.ActiveKeymap != noneKeymap && keyboard.ActiveKeymap != errorKeymap {
			fallback = keyboard.ActiveKeymap
		}
	}
	return fallback
}

// keyboardLayoutLabel renders the format with layout and alias (the
// alias falls back to the layout itself).
func keyboardLayoutLabel(layout, format string, aliasMap map[string]string) string {
	alias, ok := aliasMap[layout]
	if !ok {
		alias = layout
	}
	return jinja.RenderOr(format, map[string]any{"layout": layout, "alias": alias})
}

// layoutSource is sources/mod.rs's KeyboardLayoutSource: where the
// active layout comes from on this compositor. read re-queries it;
// each tick from subscribe is the cue to read again.
type layoutSource struct {
	read      func() (string, error)
	subscribe func(ctx context.Context) (<-chan struct{}, func(), error)
	close     func()
	// next moves the compositor to the next layout; the layout event that
	// follows updates the label.
	next func(ctx context.Context) error
}

// keyboardLayoutSource is factory.rs's build_source: the compositor's
// source in Compositor::detect order (Hyprland, niri, mango, sway).
func keyboardLayoutSource(ctx ModuleContext) (layoutSource, error) {
	switch {
	case ctx.Hyprland != nil:
		return hyprlandLayoutSource(ctx.Hyprland), nil
	case detectCompositor() == compositorLysWM:
		w, err := lyswm.WatchState(lyswm.LayoutState)
		if err != nil {
			return layoutSource{}, err
		}
		return layoutSource{
			read: func() (string, error) {
				layout := w.Value()
				if layout.Name == "" {
					return "", fmt.Errorf("lys-wm's layout %d has no name", layout.Index)
				}
				return layout.Name, nil
			},
			subscribe: func(context.Context) (<-chan struct{}, func(), error) { return w.Ticks(), nil, nil },
			close:     w.Close,
			next:      func(context.Context) error { return lyswm.Dispatch("nextlayout") },
		}, nil
	case detectCompositor() == compositorNiri:
		conn, err := niri.Connect()
		if err != nil {
			return layoutSource{}, err
		}
		return layoutSource{
			read: func() (string, error) {
				layouts, err := conn.KeyboardLayouts()
				if err != nil {
					return "", err
				}
				name, _ := layouts.Current()
				return name, nil
			},
			subscribe: func(context.Context) (<-chan struct{}, func(), error) { return niri.Subscribe() },
			close:     func() { _ = conn.Close() },
			next: func(ctx context.Context) error {
				return conn.Action(ctx, "SwitchLayout", map[string]any{"layout": "Next"})
			},
		}, nil
	case detectCompositor() == compositorMango:
		w, err := mango.Watch()
		if err != nil {
			return layoutSource{}, err
		}
		return layoutSource{
			read: func() (string, error) {
				monitors, _ := w.State()
				return mango.KeyboardLayout(monitors), nil
			},
			subscribe: func(context.Context) (<-chan struct{}, func(), error) { return w.Ticks(), nil, nil },
			close:     w.Close,
			next:      func(context.Context) error { return mango.Dispatch("switch_keyboard_layout") },
		}, nil
	case detectCompositor() == compositorSway:
		conn, err := sway.Connect()
		if err != nil {
			return layoutSource{}, err
		}
		return layoutSource{
			read: func() (string, error) {
				inputs, err := conn.Inputs()
				if err != nil {
					return "", err
				}
				return sway.KeyboardLayout(inputs), nil
			},
			subscribe: func(context.Context) (<-chan struct{}, func(), error) { return sway.SubscribeTo("input") },
			close:     func() { _ = conn.Close() },
			next: func(ctx context.Context) error {
				return conn.RunCommand(ctx, "input type:keyboard xkb_switch_layout next")
			},
		}, nil
	}
	return layoutSource{}, errors.New("keyboard-input: unsupported compositor")
}

// Layout reads the active layout, as the OSD's osd.Layouts.
func (s layoutSource) Layout() (string, error) { return s.read() }

// Subscribe ticks on every cue to read the layout again until ctx
// ends, as the OSD's osd.Layouts.
func (s layoutSource) Subscribe(ctx context.Context) (<-chan struct{}, func(), error) {
	return s.subscribe(ctx)
}

// hyprlandLayoutSource reads hyprland's devices on every event: the
// event payload is not trustworthy (per-device quirks), so the Rust
// watcher re-reads too.
func hyprlandLayoutSource(conn *hyprland.Connection) layoutSource {
	return layoutSource{
		read: func() (string, error) {
			devices, err := conn.Devices()
			if err != nil {
				return "", err
			}
			return mainKeyboardLayout(devices), nil
		},
		subscribe: func(ctx context.Context) (<-chan struct{}, func(), error) {
			events, err := conn.Events(ctx)
			if err != nil {
				return nil, nil, err
			}
			ticks := make(chan struct{}, 1)
			go func() {
				defer close(ticks)
				for range events {
					select {
					case ticks <- struct{}{}:
					default:
					}
				}
			}()
			return ticks, nil, nil
		},
		close: func() {},
		next:  func(context.Context) error { return conn.NextKeyboardLayout() },
	}
}

// keyboardLayout is the module: the active keyboard layout.
type keyboardLayout struct {
	ctx  ModuleContext
	btn  *barButton
	next atomic.Pointer[func(context.Context) error]
	firstRead
}

func newKeyboardLayout(ctx ModuleContext) (Module, error) {
	if !focusAvailable(ctx) {
		return nil, errors.New("keyboard-input: unsupported compositor")
	}
	m := &keyboardLayout{ctx: ctx}
	m.btn = newBarButton(ctx, moduleIcon(ctx, ctx.Config.KeyboardInput.Icon()), "")
	m.show(unknownLayout)
	mountOffLoop(ctx, func() {
		source, err := keyboardLayoutSource(ctx)
		if err != nil {
			log.Printf("keyboard-input: %v", err)
			return
		}
		m.next.Store(&source.next)
		ctx.Invoke(m.reveal)
		reads := newOffLoopRead(ctx, "keyboard-input", func(context.Context) (string, error) { return readLayout(source) }, m.show)
		ticks, stop, err := source.subscribe(ctx.Life())
		if err != nil {
			log.Printf("keyboard-input: subscribe: %v", err)
			source.close()
			return
		}
		reads.first()
		reads.on(ticks, func() {
			if stop != nil {
				stop()
			}
			source.close()
		})
	})
	return m, nil
}

func readLayout(source layoutSource) (string, error) {
	layout, err := source.read()
	if err != nil {
		return "", err
	}
	if layout == "" {
		return unknownLayout, nil
	}
	return layout, nil
}

// show renders layout into the label.
func (m *keyboardLayout) show(layout string) {
	cfg := m.ctx.Config.KeyboardInput
	label := ""
	if cfg.LabelShow {
		label = keyboardLayoutLabel(layout, cfg.Format, cfg.LayoutAliasMap)
	}
	m.btn.SetLabel(label)
}

func (m *keyboardLayout) Root() widget.Widget { return m.btn }

// RunAction handles the module's own :next-layout; the rest fall through.
func (m *keyboardLayout) RunAction(action config.ClickAction) {
	if action.Kind != config.ClickShell || action.Command != config.KeyboardNextLayoutAction {
		runClickAction(m.ctx, action)
		return
	}
	next := m.next.Load()
	if next == nil {
		log.Printf("keyboard-input: no connection to the compositor yet; next-layout ignored")
		return
	}
	life := m.ctx.Life()
	go func() {
		if err := (*next)(life); err != nil {
			log.Printf("keyboard-input: switch to the next layout: %v", err)
		}
	}()
}
