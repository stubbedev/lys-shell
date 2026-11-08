package bar

import (
	"log"

	"github.com/stubbedev/lys-desktop/service/mpris"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/sni"
)

// serveCLIDaemons exports the session-bus interfaces the `lys-shell` CLI
// drives (the Rust services' with_daemon registrations in
// lys-shell/src/bootstrap). Each is optional, as in Rust: a service
// that cannot start is logged and its CLI reports it as not running.
// They share the shell's session connection; the returned release drops
// every name.
func serveCLIDaemons(conn *dbus.Conn, ctx ModuleContext, tray *sni.Host) func() {
	if conn == nil {
		log.Print("cli daemons: no session bus")
		return func() {}
	}
	var releases []func()
	serve := func(what string, release func(), err error) {
		if err != nil {
			log.Printf("%s: daemon: %v", what, err)
			return
		}
		releases = append(releases, release)
	}
	if ctx.PowerProfiles != nil {
		release, err := powerprofiles.ServeDaemon(conn, ctx.PowerProfiles)
		serve("power-profiles", release, err)
	}
	// The bar's media service is the one the daemon serves, so `lys-shell
	// media` and the module agree on the active player.
	if media, ok := ctx.Media.(*mpris.Service); ok {
		release, err := mpris.ServeDaemon(conn, media)
		serve("media", release, err)
	}
	if tray != nil {
		release, err := sni.ServeDaemon(conn, ctx.SNI, tray.Actions(), tray.IsWatcher())
		serve("systray", release, err)
	}
	return func() {
		for _, release := range releases {
			release()
		}
	}
}
