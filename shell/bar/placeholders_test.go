package bar

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/idleinhibit/inhibitduration"
	"github.com/stubbedev/lys-desktop/service/mpris"
	"github.com/stubbedev/lys-secrets/vault"

	"github.com/stubbedev/lys-shell/internal/jinja"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/sysinfo"
	"github.com/stubbedev/lys-shell/service/treeman"
	"github.com/stubbedev/lys-shell/service/weather"
)

func declaredNames(t *testing.T, path string) []string {
	t.Helper()
	ps := config.Placeholders(path)
	if len(ps) == 0 {
		t.Fatalf("%s declares no placeholders", path)
	}
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	slices.Sort(names)
	return names
}

func renderedNames[V any](vars map[string]V) []string {
	return slices.Sorted(maps.Keys(vars))
}

func TestEveryModuleRendersExactlyItsDeclaredPlaceholders(t *testing.T) {
	for path, names := range map[string][]string{
		"modules.battery.format":        renderedNames(batteryVars(45)),
		"modules.bitwarden.format":      renderedNames(bitwardenVars(vault.Status{})),
		"modules.brightness.format":     renderedNames(percentVars(65)),
		"modules.volume.format":         renderedNames(percentVars(45)),
		"modules.cpu.format":            renderedNames(cpuContext(sysinfo.CPU{})),
		"modules.ram.format":            renderedNames(ramContext(sysinfo.Memory{})),
		"modules.storage.format":        renderedNames(storageContext(sysinfo.Storage{})),
		"modules.idle-inhibit.format":   renderedNames(idleInhibitVars(false, inhibitduration.Duration{}, 0)),
		"modules.keybind-mode.format":   renderedNames(keybindModeVars("")),
		"modules.keyboard-input.format": renderedNames(keyboardLayoutVars("us", nil)),
		"modules.mail.format":           renderedNames(mailVars(3)),
		"modules.media.format":          renderedNames(mediaVars(mpris.Player{})),
		"modules.netstat.format":        renderedNames(netstatVars(sysinfo.Rate{})),
		"modules.nightlight.format":     renderedNames(nightlightVars(nil, 4500, 80)),
		"modules.power-profiles.format": renderedNames(powerProfilesVars("balanced")),
		"modules.recorder.format":       renderedNames(recorderVars(recorder.Change{})),
		"modules.treeman.format":        renderedNames(treemanVars(&treeman.Status{})),
		"modules.weather.format":        renderedNames(weatherVars(&weather.Weather{}, false)),
		"modules.window-title.format":   renderedNames(windowTitleVars("", "")),
	} {
		if want := declaredNames(t, path); !slices.Equal(names, want) {
			t.Errorf("%s renders %v, declares %v", path, names, want)
		}
	}
}

func TestADeclaredPlaceholderRendersAndAnUndeclaredOneDoesNot(t *testing.T) {
	for _, p := range config.Placeholders("modules.treeman.format") {
		if got := treemanLabel(p.Token, &treeman.Status{Total: 7, Stable: 7, Up: 7, Down: 7, Failed: 7}); got != "7" {
			t.Errorf("%s rendered %q, want 7", p.Token, got)
		}
	}
	if got := treemanLabel("{{ pending }}", &treeman.Status{Total: 7}); got != "{{ pending }}" {
		t.Errorf("an undeclared placeholder rendered %q, want it literal", got)
	}
	if got := jinja.RenderOr("[{{ pending }}]", batteryVars(45)); got != "[]" {
		t.Errorf("an undeclared template variable rendered %q, want nothing", got)
	}
}

func TestTheWorldClockPlaceholderRendersItsSample(t *testing.T) {
	ps := config.Placeholders("modules.world-clock.format")
	if len(ps) != 1 {
		t.Fatalf("world clock placeholders = %+v", ps)
	}
	if got := worldClockRender(ps[0].Token, time.Date(2026, 3, 5, 14, 30, 0, 0, time.UTC)); got != ps[0].Sample {
		t.Errorf("%s rendered %q, want the sample %q", ps[0].Token, got, ps[0].Sample)
	}
}
