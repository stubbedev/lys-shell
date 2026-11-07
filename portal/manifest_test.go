package portal

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/portaldialogs"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/wallpaper"
	"github.com/stubbedev/lys-shell/shell/filechooser"
	"github.com/stubbedev/lys-shell/shell/printdialog"
	"github.com/stubbedev/lys-shell/shell/screenshot"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

const implPrefix = "org.freedesktop.impl.portal."

// pendingInterfaces are declared in the manifests but not yet served
// by the Go backend; the list only shrinks.
var pendingInterfaces []string

// mounted is the short names of the portal interfaces the backend
// serves; the shell's own (screen shares) are not the frontend's.
func mounted(t *testing.T) []string {
	t.Helper()
	t.Setenv(lyswm.SocketEnv, "")
	var names []string
	for _, iface := range New(nil, config.Load("", config.DiscardDiagnostics)).interfaces() {
		if name, ok := strings.CutPrefix(iface.Name, implPrefix); ok {
			names = append(names, name)
		}
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
// the backend mounts: a drifted list silently
// drops an interface at runtime.
func TestManifestsMatchTheMountedInterfaces(t *testing.T) {
	want := slices.Sorted(slices.Values(append(mounted(t), pendingInterfaces...)))
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

func declared(t *testing.T, portal string) (dbusName string, interfaces []string) {
	t.Helper()
	for line := range strings.Lines(readResource(t, portal)) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "DBusName":
			dbusName = value
		case "Interfaces":
			for iface := range strings.SplitSeq(value, ";") {
				if name, ok := strings.CutPrefix(iface, implPrefix); ok {
					interfaces = append(interfaces, name)
				}
			}
		}
	}
	return dbusName, interfaces
}

func TestInputCaptureIsDeclaredForLysWMAlone(t *testing.T) {
	if slices.Contains(mounted(t), "InputCapture") {
		t.Error("InputCapture is mounted without lys-wm")
	}
	for _, manifest := range []string{"lys-shell.portal", "lys-shell-portals.conf"} {
		if strings.Contains(readResource(t, manifest), implPrefix+"InputCapture") {
			t.Errorf("%s declares InputCapture, which every desktop would then export", manifest)
		}
	}
	name, ifaces := declared(t, "lys-shell-lys-wm.portal")
	if shellName, _ := declared(t, "lys-shell.portal"); name != shellName || name != BusName {
		t.Errorf("lys-shell-lys-wm.portal names %q, want the backend's %q", name, BusName)
	}
	if !slices.Equal(ifaces, []string{"InputCapture"}) {
		t.Errorf("lys-shell-lys-wm.portal declares %v, want InputCapture alone", ifaces)
	}
	if strings.Contains(readResource(t, "lys-shell-lys-wm.portal"), "UseIn") {
		t.Error("lys-shell-lys-wm.portal volunteers through UseIn; only lys-wm-portals.conf routes to it")
	}
	for _, installer := range []string{"../nix/lys-shell.nix", "../nix/hm-module.nix", "../packaging/debian/rules", "../packaging/rpm/lys-shell.spec.in", "../scripts/ci/build-archive.sh"} {
		b, err := os.ReadFile(installer)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "lys-shell-lys-wm.portal") {
			t.Errorf("%s does not install lys-shell-lys-wm.portal", installer)
		}
	}
}

// TestVersionOnlyWhereTheSpecDefinesOne pins the version property to
// the impl interfaces whose XML defines it (xdg-desktop-portal 1.22):
// the others must not export one the frontend never reads.
func TestVersionOnlyWhereTheSpecDefinesOne(t *testing.T) {
	versioned := []string{"Clipboard", "DynamicLauncher", "GlobalShortcuts", "InputCapture", "Notification", "RemoteDesktop", "ScreenCast", "Screenshot", "Secret", "Settings", "Usb"}
	t.Setenv(lyswm.SocketEnv, "")
	b := New(nil, config.Load("", config.DiscardDiagnostics))
	b.input = newInputCapture(nil, b.sessions, nil, nil, nil)
	for _, iface := range b.interfaces() {
		name := strings.TrimPrefix(iface.Name, implPrefix)
		_, has := iface.Properties["version"]
		if want := slices.Contains(versioned, name); has != want {
			t.Errorf("%s exports version: %v, want %v", name, has, want)
		}
	}
}

// shellServices are the shell's services the portal shows dialogs
// through.
var shellServices = []string{
	filechooser.ServiceName, printdialog.ServiceName, sharepicker.ServiceName,
	portaldialogs.ServiceName, screenshot.ServiceName, wallpaper.ServiceName,
}

// TestShellServicesAreActivatable pins one D-Bus activation file per
// shell service, each starting the shell's unit, and every package
// installing them all: a request arriving while the shell is down then
// starts it.
func TestShellServicesAreActivatable(t *testing.T) {
	const glob = "dev.stubbe.lys.*.service"
	files, err := filepath.Glob("../resources/" + glob)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".service"))
	}
	if want := slices.Sorted(slices.Values(shellServices)); !slices.Equal(names, want) {
		t.Errorf("activation files for %v, want %v", names, want)
	}
	for _, name := range shellServices {
		want := "[D-BUS Service]\nName=" + name + "\nExec=/usr/bin/lys-shell shell\nSystemdService=lys-shell.service\n"
		if got := readResource(t, name+".service"); got != want {
			t.Errorf("%s.service =\n%s\nwant\n%s", name, got, want)
		}
	}
	for _, installer := range []string{"../nix/lys-shell.nix", "../packaging/debian/rules", "../packaging/rpm/lys-shell.spec.in", "../scripts/ci/build-archive.sh"} {
		b, err := os.ReadFile(installer)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), glob) {
			t.Errorf("%s does not install %s", installer, glob)
		}
	}
	if spec := readResource(t, "../packaging/rpm/lys-shell.spec.in"); !strings.Contains(spec, "%{_datadir}/dbus-1/services/"+glob) {
		t.Errorf("the rpm does not list %s among its files", glob)
	}
}
