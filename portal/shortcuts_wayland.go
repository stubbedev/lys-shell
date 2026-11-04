package portal

import "github.com/stubbedev/lys-ui/app"

// waylandShortcuts registers on the compositor's
// hyprland-global-shortcuts-v1 through the portal's Wayland loop.
type waylandShortcuts struct {
	loop    *waylandLoop
	onEvent func(key string, pressed bool)
}

// waylandShortcutsStarter checks the protocol on loop; onEvent runs off
// the loop for each press and release of a registered key.
func waylandShortcutsStarter(loop *waylandLoop) func(func(string, bool)) (shortcutDevice, error) {
	return func(onEvent func(key string, pressed bool)) (shortcutDevice, error) {
		available, err := answerOnLoop(loop, func(a *app.Application, reply func(bool)) {
			a.GlobalShortcutsAvailable(reply)
		})
		if err != nil {
			return nil, err
		}
		if !available {
			return nil, app.ErrGlobalShortcutsUnavailable
		}
		return waylandShortcuts{loop, onEvent}, nil
	}
}

func (w waylandShortcuts) register(key, id, appID, description, trigger string) (func(), error) {
	type registered struct {
		shortcut *app.GlobalShortcut
		err      error
	}
	r, err := answerOnLoop(w.loop, func(a *app.Application, reply func(registered)) {
		a.RegisterGlobalShortcut(id, appID, description, trigger, func(pressed bool, _ uint64) {
			go w.onEvent(key, pressed)
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
