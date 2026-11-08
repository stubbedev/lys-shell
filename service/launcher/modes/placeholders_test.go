package modes

import (
	"maps"
	"slices"
	"testing"

	"github.com/stubbedev/lys-desktop/config"

	"github.com/stubbedev/lys-shell/service/launcher"
)

func TestEveryRowTemplateRendersExactlyItsDeclaredPlaceholders(t *testing.T) {
	for path, vars := range map[string]map[string]string{
		"launcher.drun.display-format":  drunVars("", "", "", "", ""),
		"launcher.window.format":        windowVars(WindowInfo{}),
		"launcher.combi.display-format": combiVars("", ""),
	} {
		var declared []string
		for _, p := range config.Placeholders(path) {
			declared = append(declared, p.Name)
			if got := launcher.Render(p.Token, launcher.Values(map[string]string{p.Name: "x"})); got != "x" {
				t.Errorf("%s: %s rendered %q, want its value", path, p.Token, got)
			}
		}
		slices.Sort(declared)
		if rendered := slices.Sorted(maps.Keys(vars)); len(declared) == 0 || !slices.Equal(rendered, declared) {
			t.Errorf("%s renders %v, declares %v", path, rendered, declared)
		}
	}
}
