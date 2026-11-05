package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/i18n"
)

func isFormatKey(key string) bool {
	return key == "format" || strings.HasSuffix(key, "-format") || strings.HasPrefix(key, "notify-")
}

func TestEveryFormatFieldDeclaresItsPlaceholders(t *testing.T) {
	strftimeType := reflect.TypeFor[StrftimeFormat]()
	formats := 0
	leafPaths(typeOf[Config](), "", func(path string, _ reflect.Type) {
		_, f, _ := fieldAtPath(typeOf[Config](), path)
		ps := Placeholders(path)
		if f.typ.Kind() != reflect.String || enumVariants(f.typ) != nil || !isFormatKey(f.key) {
			if ps != nil {
				t.Errorf("%s: %v, but it is not a format string", path, ps)
			}
			return
		}
		formats++
		if len(ps) == 0 {
			t.Errorf("%s: a format field declares no placeholders", path)
		}
		tokens := map[string]bool{}
		for _, p := range ps {
			if p.Name == "" || !strings.Contains(p.Token, p.Name) || tokens[p.Token] {
				t.Errorf("%s: placeholder %+v is unnamed, misspelt or repeated", path, p)
			}
			tokens[p.Token] = true
			if !i18n.Settings().Has(p.Key) || !englishSettings().Has(p.Key) {
				t.Errorf("%s: %s is not in the settings FTL", path, p.Key)
			}
		}
	})
	if formats < 20 {
		t.Errorf("only %d format fields; the walk missed the schema", formats)
	}
	if ps := Placeholders("modules.clock.format"); ps != nil || reflect.TypeOf(Defaults().Clock.Format) != strftimeType {
		t.Errorf("modules.clock.format = %v, want a StrftimeFormat with no placeholders", ps)
	}
	for _, path := range []string{"modules.battery.icon-name", "modules.battery", "nope.format", ""} {
		if ps := Placeholders(path); ps != nil {
			t.Errorf("Placeholders(%q) = %v, want none", path, ps)
		}
	}
}

func TestFieldCarriesTheFormatPlaceholders(t *testing.T) {
	m, ok := Field("modules.battery.format")
	want := []Placeholder{{Name: "percent", Token: "{{ percent }}", Key: "settings-modules-battery-format-placeholder-percent", Sample: "45"}}
	if !ok || m.Kind != FieldText || !reflect.DeepEqual(m.Placeholders, want) {
		t.Errorf("Field(modules.battery.format) = %+v %v, want text with %+v", m, ok, want)
	}
	if m, _ := Field("launcher.window.format"); len(m.Placeholders) == 0 || m.Placeholders[0].Token != "{w}" {
		t.Errorf("launcher.window.format placeholders = %+v, want the brace tokens", m.Placeholders)
	}
	if m, _ := Field("modules.mail.notify-body"); len(m.Placeholders) != 4 || m.Placeholders[0].Key != "settings-modules-mail-notify-body-placeholder-sender" {
		t.Errorf("modules.mail.notify-body placeholders = %+v, want its own keys", m.Placeholders)
	}
	if m, _ := Field("modules.battery.icon-name"); m.Placeholders != nil {
		t.Errorf("icon-name placeholders = %+v, want none", m.Placeholders)
	}
}

func TestStrftimeFieldsSayTheyTakeAPattern(t *testing.T) {
	for _, path := range []string{"modules.clock.format", "login.clock-format", "login.date-format", "modules.screenshot.filename-format"} {
		if m, ok := Field(path); !ok || m.Kind != FieldText || m.Type != "StrftimeFormat" {
			t.Errorf("Field(%s) = %+v %v, want StrftimeFormat text", path, m, ok)
		}
	}
	f, err := ParseStrftimeFormat("%H:%M on %A")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Format(time.Date(2026, 3, 5, 9, 7, 0, 0, time.UTC)); got != "09:07 on Thursday" {
		t.Errorf("sample = %q", got)
	}
	if _, err := ParseStrftimeFormat("%Q"); err == nil {
		t.Error("an unsupported specifier parsed")
	}
}

func TestAClockFormatLoadsAsBeforeAndABadOneKeepsTheDefault(t *testing.T) {
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  clock:\n    format: \"%H:%M\"\n"))
	if err != nil || cfg.Clock.Format.String() != "%H:%M" || cfg.Clock.Format.Layout() == nil {
		t.Errorf("clock format = %q, %v", cfg.Clock.Format, err)
	}
	cfg, err = LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  clock:\n    format: \"%Q\"\n"))
	var d Diagnostic
	if !errors.As(err, &d) || d.Field("Field") != "modules.clock.format" {
		t.Errorf("bad clock format = %v, want a diagnostic for its field", err)
	}
	if cfg.Clock.Format.String() != Defaults().Clock.Format.String() {
		t.Errorf("bad clock format left %q, want the default", cfg.Clock.Format)
	}
}

func TestTheSchemaCarriesThePlaceholdersAndTypes(t *testing.T) {
	var schema struct {
		Defs map[string]struct {
			Format     string
			Properties map[string]struct {
				Ref          string `json:"$ref"`
				Description  string
				Placeholders []struct{ Name, Token, Sample, Description string } `json:"x-placeholders"`
			}
		} `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(GenerateSchema()), &schema); err != nil {
		t.Fatal(err)
	}
	battery := schema.Defs["BatteryConfig"].Properties["format"]
	if len(battery.Placeholders) != 1 || battery.Placeholders[0] != (struct{ Name, Token, Sample, Description string }{"percent", "{{ percent }}", "45", "Battery level (0-100)"}) {
		t.Errorf("battery format placeholders = %+v", battery.Placeholders)
	}
	section := "## Placeholders\n\n- `{{ percent }}` - Battery level (0-100)\n\n## Examples"
	if !strings.Contains(battery.Description, section) {
		t.Errorf("battery format description = %q, want the generated section before the examples", battery.Description)
	}
	if icon := schema.Defs["BatteryConfig"].Properties["icon-name"]; icon.Placeholders != nil {
		t.Errorf("battery icon-name carries placeholders %+v", icon.Placeholders)
	}
	clock := schema.Defs["ClockConfig"].Properties["format"]
	if clock.Ref != "#/$defs/StrftimeFormat" || schema.Defs["StrftimeFormat"].Format != "strftime" {
		t.Errorf("clock format = %+v, StrftimeFormat def = %+v", clock, schema.Defs["StrftimeFormat"])
	}
}
