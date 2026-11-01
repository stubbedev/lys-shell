package bar

import (
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/nightlight"
)

func TestNightlightLabel(t *testing.T) {
	// Running: the live temp and gamma, which a transition moves away
	// from the configured ones.
	live, err := nightlight.NewSetting(5200, 95)
	if err != nil {
		t.Fatal(err)
	}
	got := nightlightLabel("{{ status }} {{ temp }}K {{ gamma }}%", &live, 4500, 90)
	if got != i18n.T("bar-nightlight-on")+" 5200K 95%" {
		t.Errorf("= %q, want On 5200K 95%%", got)
	}
	// Off: the status word with -- placeholders.
	got = nightlightLabel("{{ status }} {{ temp }} {{ gamma }}", nil, 4500, 90)
	if got != i18n.T("bar-nightlight-off")+" -- --" {
		t.Errorf("= %q, want Off -- --", got)
	}
	// The configured values land on their own placeholders.
	got = nightlightLabel("set {{ config_temp }}/{{ config_gamma }}", nil, 3500, 80)
	if got != "set 3500/80" {
		t.Errorf("= %q", got)
	}
}

func TestLoadFileAppliesNightlight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	good := "modules:\n  nightlight:\n    temperature: 3500\n    gamma: 80\n    format: \"{{ status }}\"\n    icon-on: ld-moon-symbolic\n"
	if err := osWrite(path, good); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.Nightlight.Temperature != 3500 || c.Nightlight.Gamma != 80 {
		t.Errorf("config = %+v", c.Nightlight)
	}
	if c.Nightlight.IconOn != "ld-moon-symbolic" {
		t.Errorf("icon-on = %q", c.Nightlight.IconOn)
	}
	if c.Nightlight.Clicks().LeftClick.String() != ":toggle" {
		t.Errorf("left-click = %q", c.Nightlight.Clicks().LeftClick.String())
	}

	for _, bad := range []string{
		// The documented ranges are not enforced (plain u32/f64 in the
		// Rust schema); a wrong type is.
		"modules:\n  nightlight:\n    temperature: warm\n",
		"modules:\n  nightlight:\n    latitude: true\n",
		"modules:\n  nightlight:\n    gamma: -1\n",
	} {
		if err := osWrite(path, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFile(path); err == nil {
			t.Errorf("%q: want a load error", bad)
		}
	}
}
