package apptheme

import (
	"log"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/internal/icons"
)

// iconSearch is the lys-ui icon search path the registry drives (a seam
// for tests; the shell's is lys-ui's process-wide theme).
type iconSearch struct {
	paths   func() []string
	set     func([]string)
	refresh func()
}

var lysUIIcons = iconSearch{paths: widget.IconSearchPaths, set: widget.SetIconSearchPaths, refresh: widget.RefreshIcons}

// initIcons is bootstrap.rs's init_icons (IconRegistry::init): the
// icons directory and index.theme set up, stale icons migrated, the
// registry's roots put first on the icon search path, and the directory
// watched so icons `lys-shell icons install` writes while the shell runs
// resolve without a restart. A failure is logged and the shell runs on
// without lys-shell's icons. The returned function stops the watch.
func initIcons(r *icons.Registry, search iconSearch) (stop func()) {
	if err := r.EnsureSetup(); err != nil {
		log.Printf("lys-shell: icon registry init failed: %v", err)
		return func() {}
	}
	icons.Migrate(r.IconsDir())
	refresh := func() {
		search.set(r.ThemeSearchPaths(search.paths()))
		search.refresh()
	}
	refresh()
	stop, err := r.Watch(refresh)
	if err != nil {
		log.Printf("lys-shell: cannot watch icons directory: %v", err)
		return func() {}
	}
	return stop
}

// Setup is the process-wide half of a surface's start: the stylesheet
// fonts resolve through the system store, rem follows the desktop font
// (FollowDesktopFontSize), and lys-shell's icons join the icon search
// path. The returned function stops the icons watch.
func Setup(a *app.Application) (stop func()) {
	widget.SetFaceResolver(FontResolver)
	FollowDesktopFontSize(a)
	registry, err := icons.NewRegistry()
	if err != nil {
		log.Printf("lys-shell: icon registry init failed: %v", err)
		return func() {}
	}
	return initIcons(registry, lysUIIcons)
}
