package settings

import (
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
)

// The module pages' own list editors (editors/tray_override_list,
// editors/mail_account_list).

// trayOverride is one tray_override_list row's TrayItemOverride, the
// color nil while unset.
type trayOverride struct {
	name, icon string
	color      any
}

// trayOverrideItem is the row: the name and icon entries and the
// optional color.
type trayOverrideItem struct {
	*widget.Box
	name, icon *widget.Entry
	value      *trayOverride
}

func (r trayOverrideItem) itemValue() any {
	v := *r.value
	v.name, v.icon = r.name.Text(), r.icon.Text()
	return v
}

// trayOverrideList is tray_override_list::tray_override_list: an
// override per row, a nameless one not written, an empty icon unset.
var trayOverrideList = listRow(func(config.FieldMeta) listSpec {
	t := i18n.Settings()
	return listSpec{
		addKey: "settings-map-add", rowClass: "string-map-row", blank: trayOverride{}, listCls: "string-map",
		item: func(k *kit, v any, changed func()) listItem {
			o, _ := v.(trayOverride)
			r := trayOverrideItem{Box: gapBox(widget.Row), name: k.entry(), icon: k.entry(), value: &o}
			r.name.SetPlaceholder(t.Get("settings-tray-name-placeholder"))
			r.icon.SetPlaceholder(t.Get("settings-tray-icon-placeholder"))
			r.name.SetText(o.name)
			r.icon.SetText(o.icon)
			r.name.OnChanged = func(string) { changed() }
			r.icon.OnChanged = func(string) { changed() }
			color := optionalColor(k, slot{
				get:   func() any { return o.color },
				set:   func(v any) error { o.color = v; changed(); return nil },
				unset: func() { o.color = nil; changed() },
			})
			r.Append(r.name, true)
			r.Append(r.icon, true)
			r.AppendAligned(color, false, widget.AlignCenter)
			return r
		},
		decode: func(v any) []any {
			list, _ := v.([]any)
			out := make([]any, len(list))
			for i, it := range list {
				m, _ := it.(map[string]any)
				name, _ := m["name"].(string)
				icon, _ := m["icon"].(string)
				out[i] = trayOverride{name: name, icon: icon, color: m["color"]}
			}
			return out
		},
		encode: func(items []any) any {
			out := []any{}
			for _, it := range items {
				o, _ := it.(trayOverride)
				if o.name == "" {
					continue
				}
				m := map[string]any{"name": o.name}
				if o.icon != "" {
					m["icon"] = o.icon
				}
				if o.color != nil {
					m["color"] = o.color
				}
				out = append(out, m)
			}
			return out
		},
	}
})

// mailAccountList is mail_account_list::mail_account_list: a card per
// account, its name in the header; a nameless account is not written,
// an empty icon is unset (the provider's default).
var mailAccountList = cardRow(cardSpec{
	identity: &cardField{key: "name", label: "settings-mail-account-name", editor: func(k *kit, s slot) control {
		return newLiveText(k, s, "name")
	}},
	fields: []cardField{
		{key: "query", label: "settings-mail-account-query", editor: func(k *kit, s slot) control {
			return newLiveText(k, s, "tag:unread and folder:…")
		}},
		{key: "provider", label: "settings-mail-account-provider", editor: func(k *kit, s slot) control {
			return newEnumSelect(k, s, config.MetaOf[config.MailProvider]())
		}},
		{key: "icon", label: "settings-mail-account-icon", editor: func(k *kit, s slot) control { return newIconEditor(k, s) }},
	},
	blank: func() map[string]any {
		return map[string]any{"name": "", "query": ""}
	},
	keep: func(item map[string]any) bool { name, _ := item["name"].(string); return name != "" },
	clean: func(item map[string]any) map[string]any {
		if icon, _ := item["icon"].(string); icon == "" {
			delete(item, "icon")
		}
		return item
	},
	listCls: "card-form-list", rowClass: "card-form-row", labelCls: "card-form-label",
})

func presetBlank(preset any) func() map[string]any {
	return func() map[string]any {
		m, _ := config.Encode(preset).(map[string]any)
		return m
	}
}

func presetNamed(item map[string]any) bool { name, _ := item["name"].(string); return name != "" }

func presetName() *cardField {
	return &cardField{key: "name", label: "settings-generator-preset-name", editor: func(k *kit, s slot) control {
		return newLiveText(k, s, "name")
	}}
}

func presetSpin(meta config.FieldMeta) func(k *kit, s slot) control {
	return func(k *kit, s slot) control { return newNumber(k, s, meta.Min, meta.Max, 1, 0) }
}

func presetToggle(k *kit, s slot) control { return newToggle(k, s) }

var passwordPresetList = cardRow(cardSpec{
	identity: presetName(),
	fields: []cardField{
		{key: "length", label: "settings-generator-preset-length", editor: presetSpin(config.MetaOf[config.PasswordLength]())},
		{key: "charsets", label: "settings-generator-preset-charsets", fill: true, editor: func(k *kit, s slot) control {
			return newListEditor(k, s, enumListSpec(config.MetaOf[config.PasswordCharset]()))
		}},
		{key: "avoid-ambiguous", label: "settings-generator-preset-avoid-ambiguous", editor: presetToggle},
	},
	blank:   presetBlank(config.DefaultsBitwardenPasswordPreset()),
	keep:    presetNamed,
	listCls: "card-form-list", rowClass: "card-form-row", labelCls: "card-form-label",
})

var passphrasePresetList = cardRow(cardSpec{
	identity: presetName(),
	fields: []cardField{
		{key: "words", label: "settings-generator-preset-words", editor: presetSpin(config.MetaOf[config.PassphraseWords]())},
		{key: "separator", label: "settings-generator-preset-separator", editor: func(k *kit, s slot) control {
			return newLiveText(k, s, "-")
		}},
		{key: "capitalize", label: "settings-generator-preset-capitalize", editor: presetToggle},
		{key: "number", label: "settings-generator-preset-number", editor: presetToggle},
	},
	blank:   presetBlank(config.DefaultsBitwardenPassphrasePreset()),
	keep:    presetNamed,
	listCls: "card-form-list", rowClass: "card-form-row", labelCls: "card-form-label",
})
