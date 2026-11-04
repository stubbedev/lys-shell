package search

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/settingspage"
)

// System offers the session commands, through the commands the power
// menu runs. Those that end what you are doing ask first: a fuzzy search
// and an Enter should not be enough to reboot.
type System struct {
	env   Env
	power config.PowerConfig
}

// NewSystem builds it over the power configuration.
func NewSystem(env Env, power config.PowerConfig) *System { return &System{env: env, power: power} }

// Spec implements Provider.
func (*System) Spec() Spec { return Spec{Section: SectionCommands, MinQuery: 2} }

type systemCommand struct {
	id, titleKey, keywords, icon string
	command                      string
	shown                        bool
	confirm                      bool
}

func (s *System) commands() []systemCommand {
	p := s.power
	return []systemCommand{
		{"lock", "launcher-system-lock", "screen secure away", "ld-lock-symbolic", p.LockCommand, p.ShowLock, false},
		{"suspend", "launcher-system-suspend", "sleep standby", "ld-moon-symbolic", p.SuspendCommand, p.ShowSuspend, false},
		{"logout", "launcher-system-logout", "sign out exit session", "ld-log-out-symbolic", p.LogoutCommand, p.ShowLogout, true},
		{"reboot", "launcher-system-reboot", "restart", "ld-rotate-ccw-symbolic", p.RebootCommand, p.ShowReboot, true},
		{"shutdown", "launcher-system-shutdown", "power off poweroff halt", "ld-power-symbolic", p.ShutdownCommand, p.ShowShutdown, true},
	}
}

// Load implements Provider.
func (s *System) Load(_ context.Context, publish func([]Entry)) {
	var entries []Entry
	for _, c := range s.commands() {
		if !c.shown || c.command == "" {
			continue
		}
		id := "system:" + c.id
		entries = append(entries, Entry{
			ID: id, Learn: id, Title: i18n.T(c.titleKey), Keywords: c.keywords,
			Icon: launcher.IconName(c.icon), Data: c,
		})
	}
	publish(entries)
}

// Actions implements Provider.
func (s *System) Actions(e Entry) []Action {
	c, ok := e.Data.(systemCommand)
	if !ok {
		return nil
	}
	run := Action{Title: e.Title, Icon: c.icon, Run: func(context.Context) Outcome {
		s.env.SpawnShell(c.command)
		return Close{}
	}}
	if !c.confirm {
		return []Action{run}
	}
	return []Action{confirmed(run)}
}

// confirmed is run behind a level that asks first.
func confirmed(run Action) Action {
	return Action{Title: run.Title, Icon: run.Icon, Run: func(context.Context) Outcome {
		return Push{Level: &Level{
			Title:     run.Title,
			Message:   i18n.T("launcher-system-confirm", i18n.Str("what", run.Title)),
			Providers: []Provider{&fixedProvider{entries: []Entry{{ID: "confirm", Title: run.Title, Icon: launcher.IconName(run.Icon), Data: run}}}},
		}}
	}}
}

// fixedProvider offers rows whose Data is the Action to run: a flat list
// settled before it opens.
type fixedProvider struct{ entries []Entry }

func (*fixedProvider) Spec() Spec { return Spec{Section: SectionNone} }

func (*fixedProvider) Immediate() {}

func (p *fixedProvider) Load(_ context.Context, publish func([]Entry)) { publish(p.entries) }

func (*fixedProvider) Actions(e Entry) []Action {
	a, ok := e.Data.(Action)
	if !ok {
		return nil
	}
	return []Action{a}
}

// Settings offers lys-settings' pages.
type Settings struct {
	env   Env
	pages func(context.Context) ([]settingspage.Page, error)
}

// NewSettings offers pages.
func NewSettings(env Env, pages func(context.Context) ([]settingspage.Page, error)) *Settings {
	return &Settings{env: env, pages: pages}
}

// notInstalled says once a session that lys-settings is missing, which every
// opening of the launcher would otherwise repeat.
var notInstalled sync.Once

// Spec implements Provider.
func (*Settings) Spec() Spec { return Spec{Section: SectionSettings, MinQuery: 2} }

// Load implements Provider.
func (s *Settings) Load(ctx context.Context, publish func([]Entry)) {
	pages, err := s.pages(ctx)
	switch {
	case errors.Is(err, settingspage.ErrNotInstalled):
		notInstalled.Do(func() { log.Printf("launcher: no settings pages to offer: %v", err) })
		return
	case err != nil:
		log.Printf("launcher: no settings pages to offer: %v", err)
		return
	}
	entries := make([]Entry, 0, len(pages))
	for _, p := range pages {
		id := "settings:" + p.ID
		entries = append(entries, Entry{ID: id, Learn: id, Title: p.Title, Subtitle: i18n.T("launcher-settings-subtitle"), Keywords: p.ID, Icon: launcher.IconName(p.Icon), Data: p})
	}
	publish(entries)
}

// Actions implements Provider.
func (s *Settings) Actions(e Entry) []Action {
	p, ok := e.Data.(settingspage.Page)
	if !ok {
		return nil
	}
	return []Action{{Title: i18n.T("launcher-action-open"), Icon: "ld-external-link-symbolic", Run: func(context.Context) Outcome {
		s.env.Spawn(p.Argv())
		return Close{}
	}}}
}
