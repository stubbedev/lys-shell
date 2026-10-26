package pipewire

import (
	"strings"
	"testing"
)

// A server from 1.2.0 on is taken, a pre-release suffix included; an
// older one is refused naming both versions, and a version that is not
// major.minor.micro is refused as such.
func TestServerVersionGate(t *testing.T) {
	for _, v := range []string{"1.2.0", "1.6.9", "1.4.0-rc1", "2.0.0"} {
		if err := checkServer(v); err != nil {
			t.Errorf("%s refused: %v", v, err)
		}
	}
	for v, want := range map[string]string{
		"1.1.90": "pipewire: server 1.1.90 is older than 1.2.0, the oldest whose node activation this client speaks",
		"1.0.5":  "pipewire: server 1.0.5 is older than 1.2.0",
		"0.3.85": "pipewire: server 0.3.85 is older than 1.2.0",
		"1.6":    `server version "1.6" is not major.minor.micro`,
		"1.x.0":  `server version "1.x.0" is not major.minor.micro`,
		"":       `server version "" is not major.minor.micro`,
	} {
		if err := checkServer(v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", v, err, want)
		}
	}
}
