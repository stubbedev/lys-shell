package settings

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
)

// generalPage is pages/general.
func generalPage(*config.Config) pageSpec {
	return pageSpec{
		id: "general", navKey: "settings-nav-general", icon: "ld-settings-symbolic",
		header: "settings-page-general",
		sections: []sectionSpec{
			{title: "settings-section-fonts", rows: []rowSpec{field("general.font-sans", fontRow), field("general.font-mono", monoFontRow)}},
			{title: "settings-section-scale-rounding", rows: fields("styling.scale", "styling.rounding")},
			{title: "settings-section-display", rows: fields("general.tearing-mode")},
		},
	}
}

// lockPage is pages/lock.
func lockPage(*config.Config) pageSpec {
	return pageSpec{
		id: "lock", navKey: "settings-nav-lock-page", icon: "ld-lock-symbolic",
		header: "settings-page-lock",
		sections: []sectionSpec{
			{title: "settings-section-general", rows: fields("login.lock.enabled", "login.lock.lock-on-start")},
			{title: "settings-section-background", rows: []rowSpec{
				field("login.background-mode"), field("login.background-image", filePath),
				field("login.background-color"), field("login.blur"),
			}},
			{title: "settings-section-clock", rows: fields("login.show-clock", "login.clock-format", "login.date-format")},
			{title: "settings-section-security", rows: fields("login.lock.grace-period-ms", "login.max-attempts",
				"login.show-failed-attempts", "login.lock.blank-timeout-ms", "login.lock.pam-service")},
			{title: "settings-section-animation", rows: surfaceAnimationRows("animations.lock")},
		},
	}
}

// greeterPage is pages/greeter, with the apply footer.
func greeterPage(*config.Config) pageSpec {
	return pageSpec{
		id: "greeter", navKey: "settings-nav-greeter-page", icon: "ld-monitor-symbolic",
		header: "settings-page-greeter",
		sections: []sectionSpec{
			{title: "settings-section-general", rows: fields("login.greeter.show-user-list", "login.greeter.show-power-buttons")},
			{title: "settings-section-cursor", rows: fields("login.greeter.cursor-theme", "login.greeter.cursor-size")},
		},
		footer: greeterApplyFooter,
	}
}

// Greeter apply texts (apply.rs: not localized in Rust either).
const (
	greeterApplyLabel      = "Apply to login screen"
	greeterApplyAdminLabel = "Apply as administrator"
	greeterApplyFailed     = "Apply failed"
	greeterApplyDone       = "The next login screen uses these settings."
	greeterApplyHint       = "Changes the login screen of lys-login's display manager."
)

// greeterApplyMode is who runs lys-login apply-config.
type greeterApplyMode int

const (
	// applyAsUser hands the change to lys-login's display manager,
	// which takes it from the user whose session it runs.
	applyAsUser greeterApplyMode = iota
	// applyAsAdmin runs apply-config as root through pkexec, which
	// writes the system config itself: the way without lys-login's
	// display manager, behind admin authentication.
	applyAsAdmin
)

// greeterApplyTimeout bounds the user's apply-config, one exchange on
// the display manager's socket; the admin path waits on the person
// answering the polkit prompt instead.
const greeterApplyTimeout = 10 * time.Second

// command is apply-config of the staged file in mode.
func (m greeterApplyMode) command(staged string) (*exec.Cmd, context.CancelFunc) {
	args := []string{"lys-login", "apply-config", staged}
	if m == applyAsAdmin {
		return exec.Command("pkexec", args...), func() {} //nolint:gosec // fixed program, staged path
	}
	ctx, cancel := context.WithTimeout(context.Background(), greeterApplyTimeout)
	return exec.CommandContext(ctx, args[0], args[1:]...), cancel //nolint:gosec // fixed program, staged path
}

// greeterApplyFooter is build_footer: the button pushing the greeter
// settings to the login screen, and its hint. A failed apply shows
// lys-login's reason and offers the administrator's path.
func greeterApplyFooter(k *kit) widget.Widget {
	box := gapBox(widget.Column)
	box.AddClass("settings-section")
	hint := k.label(greeterApplyHint, "settings-section-title")
	hint.SetWrap(true)
	admin := k.button(k.label(greeterApplyAdminLabel), nil)
	admin.SetVisible(false)
	apply := func(mode greeterApplyMode) {
		async(k, func() error { return applyGreeter(k.store, mode) }, func(err error) {
			if err != nil {
				log.Printf("settings: greeter apply failed: %v", err)
				hint.SetText(greeterApplyFailed + ": " + err.Error())
				admin.SetVisible(true)
				return
			}
			hint.SetText(greeterApplyDone)
			admin.SetVisible(false)
		})
	}
	admin.OnClick = func() { apply(applyAsAdmin) }
	box.AppendAligned(k.button(k.label(greeterApplyLabel), func() { apply(applyAsUser) }, css.SuggestedAction), false, widget.AlignStart)
	box.AppendAligned(admin, false, widget.AlignStart)
	box.Append(hint, false)
	return box
}

// applyGreeter stages the login keys the greeter takes and runs
// `lys-login apply-config` on them in mode, waiting for its verdict.
func applyGreeter(s store, mode greeterApplyMode) error {
	login := make(map[string]any, len(config.GreeterApplyKeys))
	for _, key := range config.GreeterApplyKeys {
		if v := s.value("login." + key); v != nil {
			if err := config.InsertPath(login, key, v); err != nil {
				return fmt.Errorf("stage login.%s: %w", key, err)
			}
		}
	}
	body, err := config.YAMLDocument(map[string]any{"login": login})
	if err != nil {
		return err
	}
	staged, err := stageGreeterApply(body)
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(staged); err != nil {
			log.Printf("settings: greeter apply: %v", err)
		}
	}()
	cmd, cancel := mode.command(staged)
	defer cancel()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if reason := strings.TrimSpace(stderr.String()); reason != "" {
			return fmt.Errorf("%s: %s", strings.Join(cmd.Args, " "), reason)
		}
		return fmt.Errorf("%s: %w", strings.Join(cmd.Args, " "), err)
	}
	return nil
}

// stageGreeterApply writes body to a fresh private file for
// apply-config to read.
func stageGreeterApply(body string) (string, error) {
	f, err := os.CreateTemp("", "lys-greeter-apply-*.yaml")
	if err != nil {
		return "", fmt.Errorf("stage greeter apply: %w", err)
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("stage %s: %w", f.Name(), err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("stage %s: %w", f.Name(), err)
	}
	return f.Name(), nil
}
