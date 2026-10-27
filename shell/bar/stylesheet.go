package bar

import "github.com/stubbedev/lys-shell/config"

// floating reports whether an inset pulls the bar off its edges: the
// floating class of apply_css_classes (bar/methods.rs), which the bar
// carries beside its element, edge and connector.
func floating(cfg *config.Config) bool {
	return !cfg.Bar.InsetEdge.IsZero() || !cfg.Bar.InsetEnds.IsZero()
}
