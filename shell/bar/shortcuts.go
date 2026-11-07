package bar

import (
	"log"

	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/shellipc"
)

type shellShortcut struct {
	id, description, trigger string
	press                    func()
}

type shortcutRegistrar func(id, appID, description, trigger string, on func(pressed bool, unixSec uint64), done func(*app.GlobalShortcut, error))

func registerShellShortcuts(register shortcutRegistrar, shortcuts []shellShortcut) (stop func()) {
	var live []*app.GlobalShortcut
	for _, sc := range shortcuts {
		register(sc.id, shellipc.AppID, sc.description, sc.trigger, func(pressed bool, _ uint64) {
			if pressed {
				sc.press()
			}
		}, func(s *app.GlobalShortcut, err error) {
			if err != nil {
				log.Printf("shortcut %s: %v; the CLI still opens it", sc.id, err)
				return
			}
			live = append(live, s)
		})
	}
	return func() {
		for _, s := range live {
			s.Destroy()
		}
	}
}

type barVisibility interface {
	Hide(monitor string)
	Show(monitor string)
	Toggle(monitor string)
}

func shellShortcuts(openLauncher, toggleDashboard func(), bars barVisibility) []shellShortcut {
	return []shellShortcut{
		{id: "launcher", description: i18n.T("shortcut-launcher"), trigger: "SUPER+Space", press: openLauncher},
		{id: "dashboard", description: i18n.T("shortcut-dashboard"), trigger: "SUPER+A", press: toggleDashboard},
		{id: "bar-hide", description: i18n.T("shortcut-bar-hide"), press: func() { bars.Hide("") }},
		{id: "bar-show", description: i18n.T("shortcut-bar-show"), press: func() { bars.Show("") }},
		{id: "bar-toggle", description: i18n.T("shortcut-bar-toggle"), press: func() { bars.Toggle("") }},
	}
}
