package config

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

var semanticPaths = map[string]string{
	"styling.theming-monitor":                     "Monitor",
	"launcher.monitor":                            "Monitor",
	"launcher.icon-theme":                         "IconTheme",
	"login.greeter.cursor-theme":                  "CursorTheme",
	"launcher.filebrowser.directory":              "Directory",
	"launcher.search.commands-dir":                "Directory",
	"modules.recorder.output-directory":           "Directory",
	"modules.screenshot.output-directory":         "Directory",
	"launcher.terminal":                           "Program",
	"launcher.ssh.client":                         "Program",
	"launcher.drun.url-launcher":                  "Program",
	"launcher.run.run-command":                    "Command",
	"launcher.run.shell-command":                  "Command",
	"launcher.run.list-command":                   "Command",
	"launcher.ssh.command":                        "Command",
	"launcher.filebrowser.command":                "Command",
	"modules.power.lock-command":                  "Command",
	"modules.power.shutdown-command":              "Command",
	"modules.dashboard.dropdown-poweroff-command": "Command",
	"modules.netstat.interface":                   "NetInterface",
	"modules.cpu.temp-sensor":                     "TempSensor",
	"modules.cava.source":                         "AudioSource",
	"login.lock.pam-service":                      "PamService",
	"modules.weather.visual-crossing-key":         "Secret",
	"modules.weather.weatherapi-key":              "Secret",
}

func TestFieldNamesWhatAListableStringHolds(t *testing.T) {
	for path, want := range semanticPaths {
		m, ok := Field(path)
		if !ok || m.Type != want || m.Kind != FieldText || !slices.Equal(m.Variants, semanticStrings[want].variants) {
			t.Errorf("Field(%s) = %+v %v, want %s text with variants %q", path, m, ok, want, semanticStrings[want].variants)
		}
	}
	if m, _ := Field("modules.cpu.temp-sensor"); !slices.Equal(m.Variants, []string{"auto"}) {
		t.Errorf("temp-sensor variants = %q, want auto", m.Variants)
	}
	if m, _ := Field("launcher.search.web-url"); m.Type != "string" || m.Variants != nil {
		t.Errorf("an untyped string = %+v, want a plain string", m)
	}
	if m, _ := Field("modules.notifications.popup-monitor"); m.Type != "PopupMonitor" || !slices.Equal(m.Variants, []string{"primary"}) {
		t.Errorf("popup-monitor = %+v, want PopupMonitor with primary", m)
	}
}

func TestTypedStringsLoadTheirOldValues(t *testing.T) {
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "launcher:\n  terminal: foot\n  icon-theme: Papirus\n  monitor: DP-1\nmodules:\n  cpu:\n    temp-sensor: Tctl\n  weather:\n    weatherapi-key: $WEATHER_KEY\nlogin:\n  lock:\n    pam-service: login\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Launcher.Terminal != "foot" || cfg.Launcher.IconTheme != "Papirus" || cfg.Launcher.Monitor != "DP-1" || cfg.CPU.TempSensor != "Tctl" ||
		cfg.Weather.WeatherapiKey == nil || *cfg.Weather.WeatherapiKey != "$WEATHER_KEY" || cfg.Login.Lock.PamService != "login" {
		t.Errorf("typed strings loaded as %+v", cfg)
	}
	if _, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "launcher:\n  terminal: 3\n")); err == nil {
		t.Error("a number loaded into a Program")
	}
}

func TestTheSchemaNamesTheTypedStrings(t *testing.T) {
	var schema struct {
		Defs map[string]struct {
			Type       any
			AnyOf      []map[string]any `json:"anyOf"`
			Variants   []string         `json:"x-variants"`
			Properties map[string]struct {
				Ref string `json:"$ref"`
			}
		} `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(GenerateSchema()), &schema); err != nil {
		t.Fatal(err)
	}
	if ref := schema.Defs["LauncherConfig"].Properties["icon-theme"].Ref; ref != "#/$defs/IconTheme" {
		t.Errorf("icon-theme = %q, want the IconTheme def", ref)
	}
	if d := schema.Defs["TempSensor"]; d.Type != "string" || !slices.Equal(d.Variants, []string{"auto"}) {
		t.Errorf("TempSensor def = %+v", d)
	}
	if ref := schema.Defs["WeatherConfig"].Properties["weatherapi-key"].Ref; ref != "#/$defs/Nullable_Secret" || len(schema.Defs["Nullable_Secret"].AnyOf) != 2 {
		t.Errorf("weatherapi-key = %q, Nullable_Secret = %+v", ref, schema.Defs["Nullable_Secret"])
	}
}

func TestATypeOptionNeedsADeclaredTypeOnAString(t *testing.T) {
	for name, typ := range map[string]any{
		"undeclared": struct {
			X string `cfg:"x,type=Nope"`
		}{},
		"not a string": struct {
			X uint32 `cfg:"x,type=Program"`
		}{},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: the field was accepted", name)
				}
			}()
			collectFields(reflect.TypeOf(typ), nil)
		}()
	}
}
