package bar

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	"github.com/stubbedev/lys-shell/internal/version"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/launcher/search"
	"github.com/stubbedev/lys-shell/service/launcher/typetext"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/launcher"
	"github.com/stubbedev/lys-shell/shell/settings"
)

// startLauncher serves the launcher socket from the shell, as the Rust
// shell's launcher service does: `lys-shell launcher` sessions open the
// surface on the UI loop, which reads the live config on every session
// (so a reload reaches the next one). The returned stop closes the
// socket.
func startLauncher(application *app.Application, outputs func() []*app.Output, current func() *config.Config, ctx ModuleContext, theme *apptheme.Theme, font render.Font) func() {
	deps := launcher.Deps{
		Invoke: application.Invoke,
		After: func(d time.Duration, fn func()) func() {
			t := time.AfterFunc(d, func() { application.Invoke(fn) })
			return func() { t.Stop() }
		},
		Config: current,
		Open: func(cfg app.LayerConfig) (launcher.Window, error) {
			w, err := application.NewLayer(cfg)
			if err != nil {
				return nil, err
			}
			return w, nil
		},
		Font:          font,
		Variants:      app.FontVariantsOf(font),
		Sheet:         theme.Sheet,
		SheetPriority: apptheme.Priority,
		Outputs:       outputs,
		MonitorWidth: func() int {
			if outs := outputs(); len(outs) > 0 {
				return int(outs[0].LogicalW)
			}
			return 0
		},
		Copy: func(text string) {
			if c := application.Clipboard(); c != nil {
				if err := c.WriteText(text); err != nil {
					log.Printf("launcher: copy: %v", err)
				}
			}
		},
		Type:        typetext.Type,
		OpenHistory: engine.OpenHistory,
		OpenFrecency: func() (*search.Frecency, error) {
			path, ok := search.FrecencyPath()
			if !ok {
				return nil, errors.New("launcher: no state directory: uses will not be remembered")
			}
			return search.OpenFrecency(path, time.Now)
		},
	}
	browser := newBrowserIndex()
	deps.Search = func() search.Deps {
		cfg := current()
		d := search.Deps{
			Env:     search.RealEnv(cfg.Launcher.Terminal),
			Windows: modes.DetectWindows(),
			Browser: browser,
			SettingsPages: func() []search.SettingsPage {
				pages := settings.Pages(cfg)
				out := make([]search.SettingsPage, len(pages))
				for i, p := range pages {
					out[i] = search.SettingsPage{ID: p.ID, Title: p.Title, Icon: p.Icon}
				}
				return out
			},
		}
		if deps.Clipboard != nil {
			d.Clipboard = deps.Clipboard
		}
		if deps.Vault != nil {
			d.Vault = deps.Vault
		}
		return d
	}
	if ctx.Clipboard != nil {
		deps.Clipboard = ctx.Clipboard
	}
	if ctx.Bitwarden != nil {
		deps.Vault = func() engine.Mode { return newVaultMode(ctx.Bitwarden, application.Invoke, time.Now) }
	}
	stop, err := launcheripc.NewServer(launcher.New(deps)).Listen()
	if err != nil {
		log.Printf("launcher: the launcher socket is unavailable: %v", err)
		return func() {}
	}
	return stop
}

// newBrowserIndex reads the browsers of this home directory; without one
// the panel offers neither bookmarks nor history.
func newBrowserIndex() *search.BrowserIndex {
	configDir, configOK := xdg.OS.ConfigDir(version.Name)
	cacheDir, cacheOK := xdg.OS.CacheDir(version.Name)
	home := os.Getenv("HOME")
	if !configOK || !cacheOK || !filepath.IsAbs(home) {
		log.Print("launcher: XDG_CONFIG_HOME, XDG_CACHE_HOME and HOME are not absolute paths: browser bookmarks and history are unavailable")
		return nil
	}
	return search.NewBrowserIndex(home, filepath.Dir(configDir), filepath.Join(cacheDir, "favicons"))
}
