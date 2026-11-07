package portal

import "github.com/stubbedev/lys-ui/app"

// waylandShortcuts registers on the compositor's
// hyprland-global-shortcuts-v1 through the portal's Wayland loop.
type waylandShortcuts struct {
	loop   *waylandLoop
	events shortcutEvents
}

// waylandShortcutsStarter picks the device on loop: xx_hotkey_v1 where
// the compositor offers it, else hyprland-global-shortcuts-v1. events
// run off the loop for each press, release and revocation of a bound
// key.
func waylandShortcutsStarter(loop *waylandLoop) func(shortcutEvents) (shortcutDevice, error) {
	return func(events shortcutEvents) (shortcutDevice, error) {
		type offered struct{ hotkeys, global bool }
		got, err := answerOnLoop(loop, func(a *app.Application, reply func(offered)) {
			if a.HotkeysAvailable() {
				reply(offered{hotkeys: true})
				return
			}
			a.GlobalShortcutsAvailable(func(global bool) { reply(offered{global: global}) })
		})
		switch {
		case err != nil:
			return nil, err
		case got.hotkeys:
			return &waylandHotkeys{loop: loop, events: events, apps: map[string]*app.Hotkeys{}}, nil
		case got.global:
			return waylandShortcuts{loop, events}, nil
		}
		return nil, app.ErrGlobalShortcutsUnavailable
	}
}

func (waylandShortcuts) shortcutDevice() {}

func (w waylandShortcuts) register(key, id, appID, description, trigger string) (func(), error) {
	type registered struct {
		shortcut *app.GlobalShortcut
		err      error
	}
	r, err := answerOnLoop(w.loop, func(a *app.Application, reply func(registered)) {
		a.RegisterGlobalShortcut(id, appID, description, trigger, func(pressed bool, _ uint64) {
			go w.events.pressed(key, pressed)
		}, func(s *app.GlobalShortcut, err error) { reply(registered{s, err}) })
	})
	if err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	s := r.shortcut
	return func() { _ = w.loop.do(func(*app.Application) error { s.Destroy(); return nil }) }, nil
}

// waylandHotkeys binds on the compositor's xx_hotkey_v1 through the
// portal's Wayland loop, one manager per app id.
type waylandHotkeys struct {
	loop   *waylandLoop
	events shortcutEvents
	// apps are the managers by app id; touched on the loop only.
	apps map[string]*app.Hotkeys
}

func (*waylandHotkeys) shortcutDevice() {}

// manager is appID's manager, opened on first use. Call on the loop.
func (w *waylandHotkeys) manager(a *app.Application, appID string) (*app.Hotkeys, error) {
	if h, ok := w.apps[appID]; ok {
		return h, nil
	}
	h, err := a.NewHotkeys(appID)
	if err != nil {
		return nil, err
	}
	w.apps[appID] = h
	return h, nil
}

func (w *waylandHotkeys) bind(key, appID, description string, trigger app.Accel) (hotkey, error) {
	type bound struct {
		k   *app.Hotkey
		err error
	}
	r, err := answerOnLoop(w.loop, func(a *app.Application, reply func(bound)) {
		h, err := w.manager(a, appID)
		if err != nil {
			reply(bound{err: err})
			return
		}
		var k *app.Hotkey
		k, err = h.NewHotkey(description, app.HotkeyEvents{
			Pressed: func(pressed bool) { go w.events.pressed(key, pressed) },
			Revoked: func(message string) {
				k.Destroy()
				go w.events.revoked(key, message)
			},
		})
		if err != nil {
			reply(bound{err: err})
			return
		}
		answered := func(err error) {
			if err != nil {
				k.Destroy()
				reply(bound{err: err})
				return
			}
			reply(bound{k: k})
		}
		if err := k.Bind(trigger, answered); err != nil {
			answered(err)
		}
	})
	if err != nil {
		return nil, err
	}
	if r.err != nil {
		return nil, r.err
	}
	return waylandHotkey{loop: w.loop, k: r.k}, nil
}

// waylandHotkey is one bound xx_hotkey_v1.
type waylandHotkey struct {
	loop *waylandLoop
	k    *app.Hotkey
}

func (h waylandHotkey) rebind(trigger app.Accel) error {
	answer, err := answerOnLoop(h.loop, func(_ *app.Application, reply func(error)) {
		if err := h.k.Bind(trigger, reply); err != nil {
			reply(err)
		}
	})
	if err != nil {
		return err
	}
	return answer
}

func (h waylandHotkey) unbind() {
	_ = h.loop.do(func(*app.Application) error { h.k.Destroy(); return nil })
}
