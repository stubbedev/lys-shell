package bar

import (
	"slices"
	"strings"

	"github.com/stubbedev/lys-shell/internal/desktopentry"
)

// taskApp is what an app's desktop entry says: its name and icon, and
// the entry to launch; found is false for an app id no entry matches.
type taskApp struct {
	found bool
	name  string
	icon  string
	entry desktopentry.App
}

// taskApps resolves app ids to their desktop entries off the loop and
// keeps the answers: get answers at once from what is known and asks for
// the rest, and changed runs on the loop once an answer arrives.
type taskApps struct {
	known   map[string]taskApp
	asked   map[string]bool
	look    func(ids []string) map[string]taskApp
	goOff   func(func())
	post    func(func())
	changed func()
}

func newTaskApps(post func(func()), changed func()) *taskApps {
	return &taskApps{
		known:   map[string]taskApp{},
		asked:   map[string]bool{},
		look:    lookUpApps,
		goOff:   func(fn func()) { go fn() },
		post:    post,
		changed: changed,
	}
}

// get is the app for id if it is known yet; an unknown id is looked up.
func (a *taskApps) get(id string) (taskApp, bool) {
	app, ok := a.known[id]
	if !ok {
		a.want([]string{id})
	}
	return app, ok
}

// want looks up the ids not known or asked for yet, in one walk.
func (a *taskApps) want(ids []string) {
	var missing []string
	for _, id := range ids {
		if _, ok := a.known[id]; !ok && !a.asked[id] && id != "" {
			a.asked[id] = true
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return
	}
	a.goOff(func() {
		found := a.look(missing)
		a.post(func() {
			for _, id := range missing {
				a.known[id] = found[id]
				delete(a.asked, id)
			}
			a.changed()
		})
	})
}

// lookUpApps finds the desktop entries for app ids, trying each id as
// its desktop id and then lowercased, as app ids often differ from their
// entry only in case.
func lookUpApps(ids []string) map[string]taskApp {
	fileIDs := make([]string, 0, 2*len(ids))
	for _, id := range ids {
		fileIDs = append(fileIDs, desktopentry.FileID(id), desktopentry.FileID(strings.ToLower(id)))
	}
	fileIDs = slices.Compact(fileIDs)
	byID := map[string]desktopentry.App{}
	for _, app := range desktopentry.FindApps(desktopentry.ApplicationDirs(), fileIDs) {
		byID[app.ID] = app
	}
	out := make(map[string]taskApp, len(ids))
	for _, id := range ids {
		for _, fileID := range []string{desktopentry.FileID(id), desktopentry.FileID(strings.ToLower(id))} {
			if app, ok := byID[fileID]; ok {
				out[id] = taskApp{found: true, name: app.DisplayName(), icon: app.Icon(), entry: app}
				break
			}
		}
	}
	return out
}
