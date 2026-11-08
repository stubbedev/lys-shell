package bar

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/identity"
	"github.com/stubbedev/lys-desktop/settingspage"
	"github.com/stubbedev/lys-desktop/shell/apptheme"
	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-lib/xdg"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/launcheripc"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/service/launcher/answer"
	"github.com/stubbedev/lys-shell/service/launcher/modes"
	"github.com/stubbedev/lys-shell/service/launcher/search"
	"github.com/stubbedev/lys-shell/service/launcher/typetext"
	"github.com/stubbedev/lys-shell/shell/launcher"
)

// startLauncher serves the launcher socket from the shell, as the Rust
// shell's launcher service does: `lys-shell launcher` sessions open the
// surface on the UI loop, which reads the live config on every session
// (so a reload reaches the next one). The returned stop closes the
// socket; open toggles the search panel in process, as a bare
// `lys-shell launcher` does.
// launcherShell is what the launcher draws on beyond the module context:
// the wallpaper picker's service (nil without one) and the compositor's
// focused output.
type launcherShell struct {
	wallpapers launcher.Wallpapers
	focused    func() (string, bool)
}

func startLauncher(application *app.Application, outputs func() []*app.Output, current func() *config.Config, live func() ModuleContext, theme *apptheme.Theme, font render.Font, sh launcherShell) (stop, open func()) {
	ctx := live()
	deps := launcher.Deps{
		Invoke:        application.Invoke,
		Wallpapers:    sh.wallpapers,
		FocusedOutput: sh.focused,
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
		WatchFiles:    application.WatchFiles,
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
	var surface *launcher.Surface
	openPicker := func() { surface.Open(inProcessSession(launcher.WallpaperMode)) }
	deps.Search = panelDeps(current, live, newBrowserIndex(), &deps, openPicker)
	if ctx.Clipboard != nil {
		deps.Clipboard = ctx.Clipboard
	}
	if ctx.Bitwarden != nil {
		deps.Vault = func() engine.Mode { return newVaultMode(ctx.Bitwarden, application.Invoke, time.Now) }
	}
	surface = launcher.New(deps)
	open = func() { surface.Open(inProcessSession(launcher.SearchMode)) }
	stop, err := launcheripc.NewServer(surface).Listen()
	if err != nil {
		log.Printf("launcher: the launcher socket is unavailable: %v", err)
		return func() {}, open
	}
	return stop, open
}

var inProcessSessions atomic.Uint64

// inProcessSession is a session the shell opens itself, in mode.
func inProcessSession(mode string) *launcheripc.Session {
	sess, _ := launcheripc.NewSession(1<<63|inProcessSessions.Add(1), launcheripc.SessionOptions{Mode: &mode}, false, nil)
	return sess
}

// newBrowserIndex reads the browsers of this home directory; without one
// the panel offers neither bookmarks nor history.
func newBrowserIndex() *search.BrowserIndex {
	configDir, configOK := xdg.OS.ConfigDir(identity.Name)
	cacheDir, cacheOK := xdg.OS.CacheDir(identity.Name)
	home := os.Getenv("HOME")
	if !configOK || !cacheOK || !filepath.IsAbs(home) {
		log.Print("launcher: XDG_CONFIG_HOME, XDG_CACHE_HOME and HOME are not absolute paths: browser bookmarks and history are unavailable")
		return nil
	}
	return search.NewBrowserIndex(home, filepath.Dir(configDir), filepath.Join(cacheDir, "favicons"))
}

// panelDeps is what each search session draws on. It asks live each time,
// on the UI loop as a session opens: a service that started after the
// shell did is in the context by then.
func panelDeps(current func() *config.Config, live func() ModuleContext, browser *search.BrowserIndex, deps *launcher.Deps, openPicker func()) func() search.Deps {
	settingsPages := settingspage.NewLister()
	rates := newCurrencyRates()
	return func() search.Deps {
		cfg := current()
		controls := controlSourcesOf(live())
		if deps.Wallpapers != nil {
			controls.wallpaper = openPicker
		}
		d := search.Deps{
			Env:           search.RealEnv(cfg.Launcher.Terminal),
			Windows:       modes.DetectWindows(),
			Browser:       browser,
			Toggles:       controls.toggles,
			Media:         controls.mediaControls,
			Capture:       controls.captureControls,
			SettingsPages: settingsPages,
		}
		d.SkinTone = search.SkinToneOf(cfg.Launcher.Emoji.SkinTone)
		d.SetSkinTone = func(tone modes.SkinTone) {
			live().setConfig(skinTonePath, string(search.SkinToneName(tone)))
		}
		if cfg.Launcher.Search.CurrencyRates && rates != nil {
			d.Rates = rates
		}
		if deps.Clipboard != nil {
			d.Clipboard = deps.Clipboard
		}
		if deps.Vault != nil {
			d.Vault = deps.Vault
		}
		return d
	}
}

// skinTonePath is where a skin tone picked from an emoji's actions is kept.
const skinTonePath = "launcher.emoji.skin-tone"

// newCurrencyRates keeps the ECB rates in the cache directory; without one
// the panel answers no currencies.
func newCurrencyRates() *answer.Rates {
	cacheDir, ok := xdg.OS.CacheDir(identity.Name)
	if !ok {
		log.Print("launcher: XDG_CACHE_HOME is not an absolute path: currency answers are unavailable")
		return nil
	}
	return answer.NewRates(answer.FeedURL, filepath.Join(cacheDir, "fx.xml"), time.Now)
}
