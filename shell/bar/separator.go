package bar

import (
	"math"
	"strconv"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling"
)

// separatorModule is lys-ui's BarSeparator, a rule across the bar
// between its items, its color, thickness and length the separator
// config's.
type separatorModule struct {
	rule *widget.BarSeparator
}

func newSeparator(ctx ModuleContext) (Module, error) {
	rule := widget.NewBarSeparator()
	rule.SetInlineStyle(separatorCSS(ctx.Config))
	return &separatorModule{rule: rule}, nil
}

// separatorCSS is the rule's inline declarations. A BarSeparator's
// min-width and min-height are its box's: across a horizontal bar the
// width is the thickness and the height the length, across a vertical
// one the other way round.
func separatorCSS(cfg *config.Config) string {
	sep := cfg.Separator
	thick := strconv.Itoa(int(sep.Size)) + "px"
	length := strconv.Itoa(int(math.Round(sep.Length.ResolvePx(styling.RemBase, float64(cfg.Bar.Scale))))) + "px"
	width, height := thick, length
	if cfg.Bar.Location.IsVertical() {
		width, height = length, thick
	}
	color := styling.ResolveColorCSS(sep.Color, config.DefaultsSeparator().Color, cfg.Styling.ColorExtractor.ThemeProvider)
	return "color: " + color + "; min-width: " + width + "; min-height: " + height + ";"
}

func (m *separatorModule) Root() widget.Widget { return m.rule }

// ownsChrome keeps the separator out of the module-button wrapper: a
// rule is not a button.
func (m *separatorModule) ownsChrome() {}
