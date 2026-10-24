package bar

import (
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/i18n"
)

func TestHyprsunsetLabel(t *testing.T) {
	// Running: the live temp and gamma.
	got := hyprsunsetLabel("{{ status }} {{ temp }}K {{ gamma }}%", true, 4500, 90, 5000, 100)
	if got != i18n.T("bar-hyprsunset-on")+" 4500K 90%" {
		t.Errorf("= %q, want On 4500K 90%%", got)
	}
	// Off: the status word with -- placeholders.
	got = hyprsunsetLabel("{{ status }} {{ temp }} {{ gamma }}", false, 4500, 90, 5000, 100)
	if got != i18n.T("bar-hyprsunset-off")+" -- --" {
		t.Errorf("= %q, want Off -- --", got)
	}
	// The configured values land on their own placeholders.
	got = hyprsunsetLabel("set {{ config_temp }}/{{ config_gamma }}", false, 0, 0, 3500, 80)
	if got != "set 3500/80" {
		t.Errorf("= %q", got)
	}
}

func TestLoadFileAppliesHyprsunset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	good := "modules:\n  hyprsunset:\n    temperature: 3500\n    gamma: 80\n    format: \"{{ status }}\"\n    icon-on: ld-moon-symbolic\n"
	if err := osWrite(path, good); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.Hyprsunset.Temperature != 3500 || c.Hyprsunset.Gamma != 80 {
		t.Errorf("config = %+v", c.Hyprsunset)
	}
	if c.Hyprsunset.IconOn != "ld-moon-symbolic" {
		t.Errorf("icon-on = %q", c.Hyprsunset.IconOn)
	}
	if c.Hyprsunset.Clicks().LeftClick.String() != ":toggle" {
		t.Errorf("left-click = %q", c.Hyprsunset.Clicks().LeftClick.String())
	}

	for _, bad := range []string{
		// The documented ranges are not enforced (plain u32/f64 in the
		// Rust schema); a wrong type is.
		"modules:\n  hyprsunset:\n    temperature: warm\n",
		"modules:\n  hyprsunset:\n    latitude: true\n",
		"modules:\n  hyprsunset:\n    gamma: -1\n",
	} {
		if err := osWrite(path, bad); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadFile(path); err == nil {
			t.Errorf("%q: want a load error", bad)
		}
	}
}
