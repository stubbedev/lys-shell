package search

import (
	"context"
	"strings"

	"github.com/stubbedev/lys-shell/desktopentry"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/shlex"
)

// appIcon is an entry's named icon or file.
func appIcon(raw, fallback string) launcher.Icon {
	switch {
	case raw == "":
		return launcher.IconName(fallback)
	case strings.HasPrefix(raw, "/"):
		return launcher.IconFile(raw)
	}
	return launcher.IconName(raw)
}

const appFallbackIcon = "ld-app-window-symbolic"

// appWeight lifts apps over an equal match elsewhere: most searches are
// for an app.
const appWeight = 12

// Apps offers the desktop entries, links included.
type Apps struct {
	env  Env
	dirs []string
}

// NewApps searches dirs (desktopentry.ApplicationDirs).
func NewApps(env Env, dirs []string) *Apps { return &Apps{env: env, dirs: dirs} }

// Spec implements Provider.
func (*Apps) Spec() Spec { return Spec{Section: SectionApps, Weight: appWeight} }

// appData is what a row launches: an app, or a Type=Link's URL.
type appData struct {
	app *desktopentry.App
	url string
}

// Load implements Provider.
func (a *Apps) Load(_ context.Context, publish func([]Entry)) {
	apps := desktopentry.AllApps(a.dirs)
	links := desktopentry.Links(a.dirs)
	entries := make([]Entry, 0, len(apps)+len(links))
	for i := range apps {
		app := &apps[i]
		if !app.ShouldShow() {
			continue
		}
		id := "app:" + app.ID
		subtitle := app.Comment()
		if subtitle == "" {
			subtitle = app.GenericName()
		}
		var exec string
		if argv, ok := shlex.Split(app.Exec()); ok && len(argv) > 0 {
			exec = argv[0][strings.LastIndexByte(argv[0], '/')+1:]
		}
		entries = append(entries, Entry{
			ID: id, Learn: id, Title: app.DisplayName(), Subtitle: subtitle,
			Keywords: strings.Join(append(app.Keywords(), app.GenericName(), exec), " "),
			Icon:     appIcon(app.Icon(), appFallbackIcon),
			Data:     appData{app: app},
		})
	}
	for _, l := range links {
		id := "link:" + l.ID
		entries = append(entries, Entry{
			ID: id, Learn: id, Title: l.Name, Subtitle: l.URL, Icon: appIcon(l.Icon, "ld-link-symbolic"),
			Data: appData{url: l.URL},
		})
	}
	publish(entries)
}

// launchIn runs app, in a terminal when it asks for one (gio's own
// terminal lookup rarely fits Wayland setups) or the action says so.
func (a *Apps) launch(app *desktopentry.App, terminal bool) Outcome {
	if !terminal && !app.Terminal() {
		if err := app.Launch(); err != nil {
			return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", app.DisplayName()), i18n.Str("error", err.Error()))}
		}
		return Close{}
	}
	argv, ok := shlex.Split(app.Exec())
	if !ok {
		return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", app.DisplayName()), i18n.Str("error", "unparseable Exec line"))}
	}
	kept := argv[:0]
	for _, tok := range argv {
		if !isFieldCode(tok) {
			kept = append(kept, tok)
		}
	}
	a.env.inTerminal(kept...)
	return Close{}
}

func isFieldCode(tok string) bool {
	return len(tok) == 2 && tok[0] == '%' && strings.IndexByte("fFuUdDnNickvm", tok[1]) >= 0
}

// Actions implements Provider.
func (a *Apps) Actions(e Entry) []Action {
	d, _ := e.Data.(appData)
	if d.app == nil {
		return []Action{
			{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
				a.env.openURI(d.url)
				return Close{}
			}},
			copyAction(i18n.T("launcher-action-copy-url"), d.url),
		}
	}
	app := d.app
	out := []Action{{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
		return a.launch(app, false)
	}}}
	for _, action := range app.Actions() {
		out = append(out, Action{Title: app.ActionName(action), Icon: "ld-play-symbolic", Run: func(context.Context) Outcome {
			if err := app.LaunchAction(action); err != nil {
				return Stay{Message: i18n.T("launcher-failed", i18n.Str("what", app.ActionName(action)), i18n.Str("error", err.Error()))}
			}
			return Close{}
		}})
	}
	if !app.Terminal() {
		out = append(out, Action{Title: i18n.T("launcher-action-run-in-terminal"), Icon: "ld-terminal-symbolic", Run: func(context.Context) Outcome {
			return a.launch(app, true)
		}})
	}
	return append(out, copyAction(i18n.T("launcher-action-copy-name"), app.DisplayName()))
}

// copyAction copies text and closes.
func copyAction(title, text string) Action {
	return Action{Title: title, Icon: "ld-copy-symbolic", Run: func(context.Context) Outcome { return Copy{Text: text} }}
}
