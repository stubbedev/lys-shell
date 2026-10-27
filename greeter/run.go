package greeter

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/imagedecode"
	"github.com/stubbedev/lys-shell/internal/logging"
	"github.com/stubbedev/lys-shell/service/auth"
	"github.com/stubbedev/lys-shell/shell/apptheme"
	"github.com/stubbedev/lys-shell/styling"
)

// Run shows the greeter until greetd starts a session (or the window
// closes): one fullscreen window on the kiosk compositor.
func Run(init Init) error {
	// lys-ui draws the cursor client-side from the XCURSOR_* theme: set
	// the resolved one before connecting.
	ApplyCursor(ResolveCursor(init.Config.Greeter, DetectCursor(cursorHome(init.Users, init.LastUser), init.LastSession)))

	// The stylesheet's font families and weights resolve through the
	// system store, as in the shell (the clock's 700 weight).
	widget.SetFaceResolver(apptheme.FontResolver)
	if err := installIcons(); err != nil {
		log.Printf("greeter: %v", err)
	}
	app.SetLogger(logging.Library())
	sess, err := app.Connect()
	if err != nil {
		return fmt.Errorf("greeter: connect: %w", err)
	}
	defer sess.Close()
	a := app.NewApplication(sess)
	apptheme.FollowDesktopFontSize(a)

	family := init.Config.General.FontSans
	base, err := app.Font(family, 16)
	if err != nil {
		return fmt.Errorf("greeter: font %q: %w", family, err)
	}
	font := app.FontFallback(base)

	var win *app.Window
	s := newScreen(init, font, Stylesheet(init.Config), deps{
		connect: func(cmd func() []string, env []string) (auth.Conversation, error) {
			return auth.NewGreetdFromEnv(cmd, env)
		},
		spawn: func(conv auth.Conversation, username string, onEvent func(auth.Event)) authHandle {
			return auth.Spawn(conv, username, onEvent)
		},
		invoke: a.Invoke,
		focus: func(w widget.Widget) {
			if win == nil {
				return
			}
			if err := win.SetFocus(w); err != nil {
				log.Printf("greeter: cannot focus %T: %v", w, err)
			}
		},
		quit: a.Quit,
		now:  time.Now,
		power: func(verb string) {
			// logind allows both for the active local session without
			// extra polkit rules.
			if err := exec.Command("systemctl", verb).Start(); err != nil { //nolint:gosec // a fixed verb
				log.Printf("greeter: power action %s failed: %v", verb, err)
			}
		},
		loadImage:  imagedecode.Open,
		loadAvatar: imagedecode.Open,
	})
	win, err = a.NewWindow(app.WindowConfig{
		Title:      "lys-greeter",
		AppID:      "dev.stubbe.lys.greeter",
		Root:       s.root,
		Background: render.RGB(0, 0, 0),
	})
	if err != nil {
		return err
	}
	win.Fullscreen()
	s.start()
	a.Every(time.Second, s.refreshClock)
	a.Every(250*time.Millisecond, func() { s.setCapsLock(sess.Mods()&app.ModCapsLock != 0) })
	s.applyDebugOps(os.Getenv("LYS_SHELL_GREETER_DEBUG"), func(d time.Duration, fn func()) {
		time.AfterFunc(d, func() { a.Invoke(fn) })
	})
	log.Printf("lys-greeter starting (%d sessions)", len(init.Sessions))
	err = a.Run()
	if err == app.ErrClosed { //nolint:errorlint // the loop's sentinel
		return nil
	}
	return err
}

// cursorHome is whose dotfiles seed cursor detection: the remembered
// user's, else the only user's.
func cursorHome(users []User, lastUser string) string {
	for _, u := range users {
		if u.Name == lastUser && lastUser != "" {
			return u.Home
		}
	}
	if len(users) == 1 {
		return users[0].Home
	}
	return ""
}

// applyDebugOps is the dev-harness hook (LYS_SHELL_GREETER_DEBUG):
// comma-separated "popup" opens the session picker and
// "login=<user>:<pass>" fills the form and submits, each a second in.
// greetd never forwards the variable, so production never sees it.
func (s *screen) applyDebugOps(spec string, after func(time.Duration, func())) {
	if spec == "" {
		return
	}
	for op := range strings.SplitSeq(spec, ",") {
		if op == "popup" {
			after(time.Second, s.drop.Open)
			continue
		}
		login, ok := strings.CutPrefix(op, "login=")
		if !ok {
			continue
		}
		user, pass, ok := strings.Cut(login, ":")
		if !ok {
			continue
		}
		after(time.Second, func() {
			s.prompt.Username.SetText(user)
			s.submit(pass)
		})
	}
}

// Stylesheet is install_css: the compiled stylesheet and the configured
// palette's theme, at the bundle's priority, with lys-ui's theme
// installed from the same palette. The greeter carries no user styles:
// pre-login there is no user.
func Stylesheet(cfg *config.Config) *widget.Stylesheet {
	palette := cfg.Styling.ActivePalette()
	p, err := styling.PaletteFromHex(palette)
	if err != nil {
		log.Printf("greeter: palette: %v", err)
		p = styling.Default()
	}
	apptheme.InstallWidgetTheme(p, cfg)
	return widget.NewStylesheet(styling.BundleCSS(cfg, palette, p), apptheme.Priority)
}
