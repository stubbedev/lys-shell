package pipewire

import (
	"strings"
	"testing"
)

// A server from 1.2.0 on, a pre-release suffix included, speaks the
// version 1 activation and client-node v6; one from 1.0.0 the version 0
// activation and client-node v5. An older one is refused naming both
// versions, and a version that is not major.minor.micro as such.
func TestServerVersionGate(t *testing.T) {
	for v, want := range map[string]activationABI{
		"1.2.0":     {activationVersion, 6},
		"1.6.9":     {activationVersion, 6},
		"1.4.0-rc1": {activationVersion, 6},
		"2.0.0":     {activationVersion, 6},
		"1.1.90":    {0, 5},
		"1.0.5":     {0, 5},
		"1.0.0":     {0, 5},
	} {
		if got, err := abiFor(v); err != nil || got != want {
			t.Errorf("%s: %+v, %v; want %+v", v, got, err, want)
		}
	}
	for v, want := range map[string]string{
		"0.3.85": "pipewire: server 0.3.85 is older than 1.0.0, the oldest whose node activation this client speaks",
		"0.3.65": "pipewire: server 0.3.65 is older than 1.0.0",
		"1.6":    `server version "1.6" is not major.minor.micro`,
		"1.x.0":  `server version "1.x.0" is not major.minor.micro`,
		"":       `server version "" is not major.minor.micro`,
	} {
		if _, err := abiFor(v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", v, err, want)
		}
	}
}
