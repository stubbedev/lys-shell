package appicons

import "testing"

func TestLookup(t *testing.T) {
	// The name is lowercased before matching.
	if icon, ok := Lookup("Firefox"); !ok || icon != "si-firefox-symbolic" {
		t.Errorf("Firefox = %q, %v", icon, ok)
	}
	if icon, ok := Lookup("spotify"); !ok || icon != "si-spotify-symbolic" {
		t.Errorf("spotify = %q, %v", icon, ok)
	}
	// Wildcard entries match inside longer names.
	if icon, ok := Lookup("com.obsproject.Studio"); !ok || icon != "si-obsstudio-symbolic" {
		t.Errorf("obs = %q, %v", icon, ok)
	}
	// Mixed-case patterns match in either case, as icons.rs lowercases
	// the compiled table too.
	for _, name := range []string{"Slack", "slack", "Tor Browser", "tor browser"} {
		if _, ok := Lookup(name); !ok {
			t.Errorf("%q did not resolve against its mixed-case pattern", name)
		}
	}
	if _, ok := Lookup("no-such-app-anywhere"); ok {
		t.Error("an unknown name resolved")
	}
}
