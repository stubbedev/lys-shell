package bar

import (
	"io"
	"strings"

	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/bitwarden/generator"
	"github.com/stubbedev/lys-shell/shell/widgets"
)

const bwPageGenerator = "generator"

type bwRules interface {
	Generate(random io.Reader) (string, error)
}

type bwPreset struct {
	name, caption, icon, field string
	rules                      bwRules
}

var bwCharsetTokens = map[generator.Charset]string{
	generator.Lowercase: "a-z",
	generator.Uppercase: "A-Z",
	generator.Digits:    "0-9",
	generator.Symbols:   "!@#",
}

func bwPresets(cfg config.BitwardenConfig) []bwPreset {
	out := make([]bwPreset, 0, len(cfg.GeneratorPasswords)+len(cfg.GeneratorPassphrases))
	for _, p := range cfg.GeneratorPasswords {
		rules := p.Rules()
		classes := rules.Charsets.List()
		tokens := make([]string, len(classes))
		for i, c := range classes {
			tokens[i] = bwCharsetTokens[c]
		}
		out = append(out, bwPreset{
			name: p.Name,
			caption: i18n.T("dropdown-bitwarden-generator-password-caption",
				i18n.Int("length", int64(rules.Length.Int())), i18n.Str("charsets", strings.Join(tokens, " "))),
			icon:  "ld-key-round-symbolic",
			field: i18n.T("dropdown-bitwarden-field-password"),
			rules: rules,
		})
	}
	for _, p := range cfg.GeneratorPassphrases {
		rules := p.Rules()
		out = append(out, bwPreset{
			name:    p.Name,
			caption: i18n.T("dropdown-bitwarden-generator-passphrase-caption", i18n.Int("words", int64(rules.Words.Int()))),
			icon:    "ld-list-symbolic",
			field:   i18n.T("dropdown-bitwarden-field-passphrase"),
			rules:   rules,
		})
	}
	return out
}

func (v *bitwardenView) buildGenerator() widget.Widget {
	page := widget.NewBox(widget.Column, widget.Current().Spacing, 0)
	card, list := itemCard()
	v.genList = list
	v.genSt = pageStack()
	v.genSt.Add("presets", scrolledCard(card, ""))
	v.genSt.Add("empty", widgets.EmptyState(v.font, v.px, "ld-shuffle-symbolic", i18n.T("dropdown-bitwarden-generator-empty"), i18n.T("dropdown-bitwarden-generator-empty-hint")))
	page.Append(v.genSt, true)
	v.genStatus = v.caption(css.DimLabel)
	page.Append(v.genStatus, false)
	page.Append(formActions(v.textButton(i18n.T("dropdown-bitwarden-back"), func() { v.setGenerating(false) }, css.Outline)), false)
	return page
}

func (v *bitwardenView) toggleGenerator() { v.setGenerating(!v.generating) }

func (v *bitwardenView) setGenerating(on bool) {
	v.generating = on
	setCaption(v.genStatus, "")
	if on {
		v.sending = false
		v.clearSendForm()
		v.copying = nil
		v.repromptPw.reset()
		v.rebuildPresets()
	}
	v.apply()
}

func (v *bitwardenView) rebuildPresets() {
	v.genList.Clear()
	v.genRows = v.genRows[:0]
	for _, p := range bwPresets(v.host.config()) {
		row := widget.NewItem(v.font, p.name, p.caption)
		row.SetSmall(true)
		row.SetEllipsize(true)
		row.SetMedia(widget.NewThemeIcon(p.icon, int(v.px*1.2)), false)
		row.SetOnActivate(func() { v.generate(p) })
		row.AddAction(v.iconButton("ld-copy-symbolic", i18n.T("dropdown-bitwarden-generate", i18n.Str("field", p.field)), func() { v.generate(p) }))
		v.genList.Append(row)
		v.genRows = append(v.genRows, row)
	}
	if len(v.genRows) == 0 {
		v.genSt.Show("empty")
		return
	}
	v.genSt.Show("presets")
}

func (v *bitwardenView) generate(p bwPreset) {
	ttl, err := v.host.copyGenerated(p.rules, v.random)
	reportCopy(v.genStatus, p.field, ttl, err)
}
