package bar

import (
	"errors"
	"strings"

	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
)

// screenshotModule is the screenshot capture button
// (wayle-bar-hardware/src/modules/screenshot): an icon and an optional
// static label whose bindings run `lys-shell screenshot region|output|window`
// (in-process through the builtin) by default.
type screenshotModule struct {
	btn *barButton
}

func newScreenshot(ctx ModuleContext) (Module, error) {
	cfg := ctx.Config.Screenshot
	label := ""
	if cfg.LabelShow {
		label = cfg.Label
	}
	icon := moduleIcon(ctx, config.IconWith(cfg.IconShow, cfg.Icon, cfg.IconColor))
	if icon == nil && label == "" {
		return nil, errors.New("screenshot: neither the icon nor a label is shown")
	}
	return &screenshotModule{btn: newBarButton(ctx, icon, label)}, nil
}

func (m *screenshotModule) Root() widget.Widget { return m.btn }

// screenshotBuiltin reads a `screenshot [mode [target]]` builtin verb
// the way dropdown_registry.rs's try_screenshot does: the mode defaults
// to region, the target to none. ok is false for any other verb.
func screenshotBuiltin(verb string) (mode, target string, ok bool) {
	parts := strings.Fields(verb)
	if len(parts) == 0 || parts[0] != "screenshot" {
		return "", "", false
	}
	mode = "region"
	if len(parts) > 1 {
		mode = parts[1]
	}
	if len(parts) > 2 {
		target = parts[2]
	}
	return mode, target, true
}
