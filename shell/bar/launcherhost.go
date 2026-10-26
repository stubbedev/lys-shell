package bar

import (
	"log"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/launcheripc"
	engine "github.com/stubbedev/lys-shell/service/launcher"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/shell/launcher"
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
		OpenHistory: engine.OpenHistory,
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
