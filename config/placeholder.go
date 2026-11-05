package config

import (
	"reflect"
	"strings"
	"sync"

	"github.com/stubbedev/lys-shell/i18n"
)

// Placeholder is one value a format field's template substitutes.
type Placeholder struct {
	// Name is the value's name in the template.
	Name string
	// Token is the text that inserts it, as the field's renderer reads it.
	Token string
	// Key is the settings message describing it.
	Key string
	// Sample is a value a preview renders in its place.
	Sample string
}

type placeholderSyntax int

const (
	jinjaSyntax placeholderSyntax = iota
	braceSyntax
)

func (s placeholderSyntax) token(name string) string {
	if s == braceSyntax {
		return "{" + name + "}"
	}
	return "{{ " + name + " }}"
}

type placeholderDecl struct {
	name, sample, spelling string
}

type formatField struct {
	syntax       placeholderSyntax
	placeholders []placeholderDecl
}

func ph(name, sample string) placeholderDecl { return placeholderDecl{name: name, sample: sample} }

func jinjaFormat(decls ...placeholderDecl) formatField {
	return formatField{syntax: jinjaSyntax, placeholders: decls}
}

func braceFormat(decls ...placeholderDecl) formatField {
	return formatField{syntax: braceSyntax, placeholders: decls}
}

var mailNotificationPlaceholders = jinjaFormat(
	ph("sender", "Ada Lovelace"), ph("subject", "Lunch on Friday?"), ph("count", "3"), ph("new", "1"),
)

var formatFields = map[string]formatField{
	"BatteryConfig.Format":    jinjaFormat(ph("percent", "45")),
	"BitwardenConfig.Format":  jinjaFormat(ph("count", "142")),
	"BrightnessConfig.Format": jinjaFormat(ph("percent", "65")),
	"CpuConfig.Format": jinjaFormat(
		ph("percent", "45"), ph("freq_ghz", "3.2"), ph("avg_freq_ghz", "2.8"), ph("max_freq_ghz", "4.1"),
		ph("temp_c", "62"), ph("temp_f", "144"),
	),
	"IdleInhibitConfig.Format":   jinjaFormat(ph("state", "On"), ph("remaining", "44:59"), ph("duration", "60")),
	"KeybindModeConfig.Format":   jinjaFormat(ph("mode", "resize")),
	"KeyboardInputConfig.Format": jinjaFormat(ph("layout", "English (US)"), ph("alias", "EN")),
	"MailConfig.Format":          jinjaFormat(ph("count", "3")),
	"MailConfig.NotifySummary":   mailNotificationPlaceholders,
	"MailConfig.NotifyBody":      mailNotificationPlaceholders,
	"MediaConfig.Format": jinjaFormat(
		ph("title", "Bohemian Rhapsody"), ph("artist", "Queen"), ph("album", "A Night at the Opera"),
		ph("status", "Playing"), ph("status_icon", "󰐊"),
	),
	"NetstatConfig.Format": jinjaFormat(
		ph("down_kib", "1536"), ph("down_mib", "1.5"), ph("down_gib", "0.00"), ph("down_auto", "1.5 MiB/s"),
		ph("up_kib", "256"), ph("up_mib", "0.2"), ph("up_gib", "0.00"), ph("up_auto", "256 KiB/s"),
		ph("interface", "wlan0"),
	),
	"NightlightConfig.Format": jinjaFormat(
		ph("status", "On"), ph("temp", "4500"), ph("gamma", "80"), ph("config_temp", "4500"), ph("config_gamma", "80"),
	),
	"PowerProfilesConfig.Format": jinjaFormat(ph("profile", "balanced")),
	"RamConfig.Format": jinjaFormat(
		ph("percent", "45"), ph("used_gib", "7.2"), ph("total_gib", "16.0"), ph("available_gib", "8.8"),
		ph("swap_percent", "12"), ph("swap_used_gib", "1.0"), ph("swap_total_gib", "8.0"),
	),
	"RecorderConfig.Format": jinjaFormat(ph("state", "Recording"), ph("elapsed", "1:23")),
	"StorageConfig.Format": jinjaFormat(
		ph("percent", "45"),
		ph("used_tib", "0.13"), ph("used_gib", "128.5"), ph("used_mib", "131584"), ph("used_auto", "128.5 GiB"),
		ph("total_tib", "0.50"), ph("total_gib", "512.0"), ph("total_mib", "524288"), ph("total_auto", "512.0 GiB"),
		ph("free_tib", "0.37"), ph("free_gib", "383.5"), ph("free_mib", "392704"), ph("free_auto", "383.5 GiB"),
		ph("filesystem", "ext4"),
	),
	"TreemanConfig.Format": jinjaFormat(
		ph("total", "7"), ph("stable", "4"), ph("up", "1"), ph("down", "1"), ph("failed", "1"),
	),
	"VolumeConfig.Format": jinjaFormat(ph("percent", "45")),
	"WeatherConfig.Format": jinjaFormat(
		ph("temp", "22"), ph("temp_unit", "°C"), ph("feels_like", "21"), ph("condition", "Partly cloudy"),
		ph("humidity", "65%"), ph("wind_speed", "12 km/h"), ph("wind_dir", "NW"), ph("high", "25"), ph("low", "18"),
	),
	"WindowTitleConfig.Format": jinjaFormat(ph("title", "README.md - VSCode"), ph("app", "firefox")),
	"WorldClockConfig.Format": jinjaFormat(
		placeholderDecl{name: "tz", sample: "14:30 UTC", spelling: "{{ tz('UTC', '%H:%M %Z') }}"},
	),
	"LauncherDrunConfig.DisplayFormat": braceFormat(
		ph("name", "Firefox"), ph("generic", "Web Browser"), ph("exec", "firefox %u"),
		ph("categories", "Network;WebBrowser;"), ph("comment", "Browse the Web"),
	),
	"LauncherWindowConfig.Format": braceFormat(
		ph("w", "1"), ph("c", "firefox"), ph("t", "GitHub"), ph("n", "firefox"), ph("r", ""),
	),
	"LauncherCombiConfig.DisplayFormat": braceFormat(ph("mode", "drun"), ph("text", "Firefox")),
}

// Placeholders are the values the format field at a dot path substitutes,
// in the order its documentation lists them; nil for any other path.
func Placeholders(path string) []Placeholder {
	holder, f, ok := fieldAtPath(typeOf[Config](), path)
	if !ok {
		return nil
	}
	return placeholdersOf(holder, f)
}

func placeholdersOf(holder reflect.Type, f fieldInfo) []Placeholder {
	decl, ok := formatFields[f.owner.Name()+"."+f.goName]
	if !ok {
		return nil
	}
	prefix := i18nPrefixes[schemaName(holder)] + "-" + f.key + "-placeholder-"
	out := make([]Placeholder, len(decl.placeholders))
	for i, p := range decl.placeholders {
		token := p.spelling
		if token == "" {
			token = decl.syntax.token(p.name)
		}
		out[i] = Placeholder{Name: p.name, Token: token, Key: prefix + strings.ReplaceAll(p.name, "_", "-"), Sample: p.sample}
	}
	return out
}

var englishSettings = sync.OnceValue(func() *i18n.Loader {
	l, err := i18n.Select(i18n.ConfigAssets(), []i18n.LangID{i18n.MustLangID("en-US")})
	if err != nil {
		panic("config: the embedded en-US settings do not load: " + err.Error())
	}
	return l
})

func placeholderSchema(ps []Placeholder) []any {
	out := make([]any, len(ps))
	for i, p := range ps {
		out[i] = Schema{"name": p.Name, "token": p.Token, "sample": p.Sample, "description": englishSettings().Get(p.Key)}
	}
	return out
}

func withPlaceholderDocs(doc string, ps []Placeholder) string {
	var b strings.Builder
	b.WriteString("## Placeholders\n\n")
	for _, p := range ps {
		b.WriteString("- `" + p.Token + "` - " + englishSettings().Get(p.Key) + "\n")
	}
	section := strings.TrimSuffix(b.String(), "\n")
	if before, after, ok := strings.Cut(doc, "\n\n## Examples"); ok {
		return before + "\n\n" + section + "\n\n## Examples" + after
	}
	if doc == "" {
		return section
	}
	return doc + "\n\n" + section
}
