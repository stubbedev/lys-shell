package settings

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

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
	greeterApplyLabel  = "Apply to login screen"
	greeterApplyFailed = "Apply failed — see logs"
	greeterApplyHint   = "Writes the system login-screen config; asks for admin authentication."
)

// greeterApplyFooter is build_footer: the button pushing the greeter
// settings to the login screen, and its hint.
func greeterApplyFooter(k *kit) widget.Widget {
	box := gapBox(widget.Column)
	box.AddClass("settings-section")
	label := k.label(greeterApplyLabel)
	box.AppendAligned(k.button(label, func() {
		if err := applyGreeter(k.store); err != nil {
			log.Printf("settings: greeter apply failed: %v", err)
			label.SetText(greeterApplyFailed)
			return
		}
		log.Printf("settings: greeter apply: launched pkexec lys-login apply-config")
	}, css.SuggestedAction), false, widget.AlignStart)
	hint := k.label(greeterApplyHint, "settings-section-title")
	hint.SetWrap(true)
	box.Append(hint, false)
	return box
}

// applyGreeter stages the login keys the greeter takes and hands them
// to `pkexec lys-login apply-config` (dispatch).
func applyGreeter(s store) error {
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
	path := filepath.Join(os.TempDir(), fmt.Sprintf("lys-greeter-apply-%d.yaml", os.Getpid()))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return fmt.Errorf("stage %s: %w", path, err)
	}
	return spawnGreeterApply(path)
}

// spawnGreeterApply starts pkexec without waiting: the polkit agent
// asks, and lys-login reports on its own (a test seam).
var spawnGreeterApply = func(staged string) error {
	cmd := exec.Command("pkexec", "lys-login", "apply-config", staged) //nolint:gosec // fixed program, staged path
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn pkexec: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
