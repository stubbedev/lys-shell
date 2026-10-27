package config

import (
	"errors"
	"path/filepath"
)

// LoginConfig is the login screen: one screen, run as the greeter before
// login and as the lock screen in the session, so both look and behave
// alike. The keys here apply to both; `greeter` and `lock` hold what
// only one of them has.
//
// Login screen: the greeter and the lock screen, one screen in two modes.
//
// The greeter reads the system config (`/etc/lys-shell/config.yaml`), the
// lock screen the user's: copy or symlink your user config if you want the
// greeter to follow it.
type LoginConfig struct {
	// Background is background-mode, background-image, and
	// background-color.
	Background Background `cfg:",inline"`
	// Gaussian blur radius applied to image/wallpaper backgrounds (0 = none).
	Blur LockBlur `cfg:"blur"`
	// Show a clock above the credential prompt.
	ShowClock bool `cfg:"show-clock"`
	// `strftime` format for the time.
	ClockFormat StrftimeFormat `cfg:"clock-format"`
	// `strftime` format for the date.
	DateFormat StrftimeFormat `cfg:"date-format"`
	// Maximum failed password attempts before further input is blocked
	// (`0` = unlimited). A locked session stays locked regardless.
	MaxAttempts uint32 `cfg:"max-attempts"`
	// Show the failed-attempt count.
	ShowFailedAttempts bool `cfg:"show-failed-attempts"`
	// The lock screen's own settings.
	Lock LoginLockConfig `cfg:"lock"`
	// The greeter's own settings.
	Greeter LoginGreeterConfig `cfg:"greeter"`
}

// LoginLockConfig is login.lock.
//
// Lock mode: the login screen locking the session via `ext-session-lock-v1`.
//
// When `enabled`, the session locks in response to the logind `Lock` signal
// (`loginctl lock-session`), the `lys-shell lock` CLI, or the shell IPC
// `lock()` method. The lock surface grabs all input and survives client
// crashes (the compositor shows a solid color), unlike a layer-shell overlay.
type LoginLockConfig struct {
	// Let lys-shell handle session locking. When off, lock requests are ignored
	// and an external locker (e.g. hyprlock) stays responsible.
	Enabled bool `cfg:"enabled"`
	// Lock the session as soon as the shell starts. For autologin setups
	// (e.g. greetd with no password at boot) where the lock screen is the
	// access gate: the session comes up locked and requires the password
	// before use. Off by default — a normal login has already authenticated.
	LockOnStart bool `cfg:"lock-on-start"`
	// Grace window after locking during which the screen unlocks without a
	// password (milliseconds, `0` = always require the password).
	GracePeriodMS uint32 `cfg:"grace-period-ms"`
	// Black out the lock screen after this idle time (milliseconds, `0` =
	// never). This is a visual blank that hides the clock/prompt and dismisses
	// on any key; true display power-off (DPMS) is left to your idle daemon.
	BlankTimeoutMS uint32 `cfg:"blank-timeout-ms"`
	// PAM service name used to authenticate the unlock. Distro-dependent:
	// `system-auth` (Arch/Fedora), `login`, or a custom `/etc/pam.d` entry.
	PamService string `cfg:"pam-service"`
}

// LoginGreeterConfig is login.greeter.
//
// Greeter mode: the login screen before login, as the display manager.
type LoginGreeterConfig struct {
	// Show clickable avatars for the machine's login users.
	ShowUserList bool `cfg:"show-user-list"`
	// Show the shutdown/reboot buttons at the bottom of the screen.
	ShowPowerButtons bool `cfg:"show-power-buttons"`
	// Xcursor theme used on the login screen (empty = system default).
	CursorTheme string `cfg:"cursor-theme"`
	// Logical cursor size on the login screen. Scaled automatically per
	// display, so HiDPI outputs get a matching high-resolution cursor.
	CursorSize uint32 `cfg:"cursor-size"`
	// CursorThemeExplicit and CursorSizeExplicit record that a layer
	// set the key: explicit values beat cursor auto-detection, schema
	// defaults do not.
	CursorThemeExplicit bool `cfg:"-"`
	CursorSizeExplicit  bool `cfg:"-"`
}

// DefaultsLogin returns the schema defaults.
func DefaultsLogin() LoginConfig {
	return LoginConfig{
		Background:         defaultBackground(),
		ShowClock:          true,
		ClockFormat:        mustStrftime("%H:%M"),
		DateFormat:         mustStrftime("%A, %B %-d"),
		ShowFailedAttempts: true,
		Lock:               LoginLockConfig{Enabled: true, PamService: "system-auth"},
		Greeter:            LoginGreeterConfig{ShowUserList: true, ShowPowerButtons: true, CursorSize: 24},
	}
}

// GreeterConfigPath is the system config the greeter reads when no
// --config is given: pre-login there is no $HOME to discover from.
const GreeterConfigPath = "/etc/lys-shell/config.yaml"

// RuntimeOverlayPath is the runtime.yaml beside a config file: where
// `config set`, the settings GUI, and `lys-login apply-config` write
// overrides so they never clobber a hand-written config.yaml.
func RuntimeOverlayPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "runtime.yaml")
}

// LoadGreeter is the greeter's config load: the file at path with
// its imports on the config layer, the sibling runtime.yaml on the
// runtime layer. A file that fails to load leaves its layer out (the
// errors come back joined beside a usable config), so the greeter
// always renders.
func LoadGreeter(path string, sink DiagnosticSink) (*Config, error) {
	var errs []error
	configTree, err := loadTree(path)
	if err != nil {
		errs = append(errs, err)
		configTree = nil
	}
	runtimeTree, err := readRuntime(RuntimeOverlayPath(path))
	if err != nil {
		errs = append(errs, err)
	}
	configTree, runtimeTree = migrateLogin(configTree), migrateLogin(runtimeTree)
	cfg, _, err := build(configTree, runtimeTree, nil, sink)
	if err != nil {
		errs = append(errs, err)
	}
	for _, tree := range []any{configTree, runtimeTree} {
		if hasPath(tree, "login.greeter.cursor-theme") {
			cfg.Login.Greeter.CursorThemeExplicit = true
		}
		if hasPath(tree, "login.greeter.cursor-size") {
			cfg.Login.Greeter.CursorSizeExplicit = true
		}
	}
	return cfg, errors.Join(errs...)
}

// GreeterApplyKeys are the login keys, relative to login, that
// lys-settings pushes to the system login screen and lys-login
// apply-config accepts, in the settings app's order: the shared look
// and the greeter's own keys, never the lock screen's.
var GreeterApplyKeys = []string{
	"background-mode", "background-image", "background-color", "blur",
	"show-clock", "clock-format", "date-format",
	"max-attempts", "show-failed-attempts",
	"greeter.show-user-list", "greeter.show-power-buttons",
	"greeter.cursor-theme", "greeter.cursor-size",
}

// Background is the background keys the lock screen and the greeter
// share, with the same descriptions in both schemas.
type Background struct {
	// How the background is drawn: solid color, an image, or the wallpaper.
	Mode LockBackground `cfg:"background-mode"`
	// Background image path (used when `background-mode: image`).
	Image string `cfg:"background-image"`
	// Background fill color (used when `background-mode: color`).
	Color HexColor `cfg:"background-color"`
}

// defaultBackground is the shared schema default: a black fill.
func defaultBackground() Background {
	return Background{Mode: BackgroundColor, Color: mustHex("#000000")}
}

// ImagePath resolves the file the background shows: the configured
// image for image mode, the wallpaper for wallpaper mode, and "" for
// color mode (or an unset path, which draws the color).
func (b Background) ImagePath(wallpaper string) string {
	switch b.Mode {
	case BackgroundImage:
		return b.Image
	case BackgroundWallpaper:
		return wallpaper
	}
	return ""
}

// LockBackground is how the lock screen and the greeter draw their
// background (lock/types.rs).
//
// How the lock screen background is rendered.
type LockBackground string

// Background modes.
const (
	// Solid color fill (`background-color`).
	BackgroundColor LockBackground = "color"
	// A specific image file (`background-image`).
	BackgroundImage LockBackground = "image"
	// Reuse the current desktop wallpaper.
	BackgroundWallpaper LockBackground = "wallpaper"
)

var _ = registerEnum(BackgroundColor, BackgroundImage, BackgroundWallpaper)
