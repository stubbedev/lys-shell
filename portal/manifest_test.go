package portal

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-shell/config"
)

const implPrefix = "org.freedesktop.impl.portal."

// pendingInterfaces are declared in the manifests but not yet served
// by the Go backend; the list only shrinks.
var pendingInterfaces []string

// mounted is the short names of the interfaces the backend serves.
func mounted() []string {
	var names []string
	for _, iface := range New(nil, config.Load("", config.DiscardDiagnostics)).interfaces() {
		names = append(names, strings.TrimPrefix(iface.Name, implPrefix))
	}
	slices.Sort(names)
	return names
}

func readResource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../resources/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestManifestsMatchTheMountedInterfaces pins lys-shell.portal (what the
// frontend discovers) and lys-shell-portals.conf (how it routes) to what
// the backend mounts, as manifest.rs does: a drifted list silently
// drops an interface at runtime.
func TestManifestsMatchTheMountedInterfaces(t *testing.T) {
	want := slices.Sorted(slices.Values(append(mounted(), pendingInterfaces...)))
	for i := 1; i < len(want); i++ {
		if want[i] == want[i-1] {
			t.Fatalf("%s is both mounted and pending", want[i])
		}
	}

	var declared []string
	for line := range strings.Lines(readResource(t, "lys-shell.portal")) {
		if list, ok := strings.CutPrefix(strings.TrimSpace(line), "Interfaces="); ok {
			for iface := range strings.SplitSeq(list, ";") {
				if name, ok := strings.CutPrefix(iface, implPrefix); ok {
					declared = append(declared, name)
				}
			}
		}
	}
	slices.Sort(declared)
	if !slices.Equal(declared, want) {
		t.Errorf("lys-shell.portal Interfaces= %v\nwant %v", declared, want)
	}

	conf := readResource(t, "lys-shell-portals.conf")
	var routed []string
	for line := range strings.Lines(conf) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), implPrefix); ok {
			if name, target, ok := strings.Cut(rest, "="); ok && strings.TrimSpace(target) == "lys-shell" {
				routed = append(routed, name)
			}
		}
	}
	slices.Sort(routed)
	if !slices.Equal(routed, want) {
		t.Errorf("lys-shell-portals.conf routes %v\nwant %v", routed, want)
	}
	if !strings.Contains(conf, "\ndefault=lys-shell\n") || strings.Contains(conf, "=gtk") {
		t.Error("portals.conf must default to lys-shell and delegate nothing to gtk")
	}
}

// InputCapture cannot capture on any compositor lys-shell targets, so it
// is neither declared nor mounted: the frontend then reports the portal
// unavailable instead of routing apps to sessions that never capture.
func TestInputCaptureIsNotOffered(t *testing.T) {
	if slices.Contains(mounted(), "InputCapture") {
		t.Error("InputCapture is mounted")
	}
	for _, manifest := range []string{"lys-shell.portal", "lys-shell-portals.conf"} {
		if strings.Contains(readResource(t, manifest), implPrefix+"InputCapture") {
			t.Errorf("%s declares InputCapture", manifest)
		}
	}
}
