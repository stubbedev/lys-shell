package bar

import "github.com/stubbedev/lys-shell/config"

// rootClasses are apply_css_classes (bar/methods.rs) beyond the bar
// element and its edge, which lys-ui's Bar carries: the connector, and
// floating when an inset pulls the bar off its edges.
func rootClasses(connector string, cfg *config.Config) []string {
	var classes []string
	if connector != "" {
		classes = append(classes, connector)
	}
	if !cfg.Bar.InsetEdge.IsZero() || !cfg.Bar.InsetEnds.IsZero() {
		classes = append(classes, "floating")
	}
	return classes
}
