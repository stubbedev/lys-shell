package drmconn

import "testing"

func TestNameParsesDRMSysnames(t *testing.T) {
	for sysname, want := range map[string]string{
		"card1-eDP-1":    "eDP-1",
		"card0-HDMI-A-1": "HDMI-A-1",
		"card12-DP-3":    "DP-3",
	} {
		if got, ok := Name(sysname); !ok || got != want {
			t.Errorf("Name(%q) = %q %v, want %q", sysname, got, ok, want)
		}
	}
	for _, sysname := range []string{"card1", "renderD128", "cardX-DP-1", "card-DP-1", "card1-", "i2c-6", "version"} {
		if got, ok := Name(sysname); ok {
			t.Errorf("Name(%q) = %q, want no connector", sysname, got)
		}
	}
}
