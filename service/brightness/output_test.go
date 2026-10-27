package brightness

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

type fakeSysfs struct {
	t       *testing.T
	devices string
}

func newFakeSysfs(t *testing.T) *fakeSysfs {
	t.Helper()
	root := t.TempDir()
	f := &fakeSysfs{t: t, devices: filepath.Join(root, "devices", "pci0000:00", "0000:00:02.0")}
	drm, adapters := filepath.Join(root, "class", "drm"), filepath.Join(root, "bus", "i2c", "devices")
	for _, dir := range []string{f.devices, drm, adapters} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	origDRM, origAdapters := DRMDir, I2CAdapterDir
	DRMDir, I2CAdapterDir = drm, adapters
	t.Cleanup(func() { DRMDir, I2CAdapterDir = origDRM, origAdapters })
	return f
}

func (f *fakeSysfs) link(target, link string) {
	f.t.Helper()
	if err := os.Symlink(target, link); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeSysfs) connector(sysname string) string {
	f.t.Helper()
	dir := filepath.Join(f.devices, "drm", "card1", sysname)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.link(dir, filepath.Join(DRMDir, sysname))
	return dir
}

func (f *fakeSysfs) adapter(bus uint32, parent string) string {
	f.t.Helper()
	dir := filepath.Join(parent, i2cNodePrefix+strconv.FormatUint(uint64(bus), 10))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.link(dir, filepath.Join(I2CAdapterDir, filepath.Base(dir)))
	return dir
}

func (f *fakeSysfs) wireBacklight(name, connectorDir string) {
	f.t.Helper()
	dir := filepath.Join(connectorDir, name)
	if err := os.Rename(filepath.Join(BacklightDir, name), dir); err != nil {
		f.t.Fatal(err)
	}
	f.link(dir, filepath.Join(BacklightDir, name))
}

func TestBacklightConnectorFollowsTheSysfsLink(t *testing.T) {
	fakeBacklightDir(t, map[string]struct {
		typ      string
		cur, max uint32
	}{
		"intel_backlight": {"raw", 3000, 12000},
		"acpi_video0":     {"firmware", 50, 100},
	})
	sysfs := newFakeSysfs(t)
	sysfs.wireBacklight("intel_backlight", sysfs.connector("card1-eDP-1"))
	connectors := map[string]string{}
	for _, d := range Enumerate() {
		connectors[d.Name] = d.Connector
	}
	if connectors["intel_backlight"] != "eDP-1" {
		t.Errorf("intel_backlight lights %q, want eDP-1", connectors["intel_backlight"])
	}
	if c, ok := connectors["acpi_video0"]; !ok || c != "" {
		t.Errorf("acpi_video0 lights %q (listed %v), want no connector", c, ok)
	}
}

func TestI2CConnectorFromDPAuxAndHDMIDDCLink(t *testing.T) {
	sysfs := newFakeSysfs(t)
	sysfs.adapter(6, sysfs.connector("card1-DP-2"))
	hdmi := sysfs.adapter(3, sysfs.devices)
	sysfs.link(hdmi, filepath.Join(sysfs.connector("card1-HDMI-A-1"), "ddc"))
	sysfs.adapter(9, sysfs.devices)
	for bus, want := range map[uint32]string{6: "DP-2", 3: "HDMI-A-1", 9: ""} {
		if got, err := i2cConnector(bus); err != nil || got != want {
			t.Errorf("i2c-%d lights %q (%v), want %q", bus, got, err, want)
		}
	}
	if got, err := i2cConnector(12); err == nil {
		t.Errorf("a bus with no adapter resolved to %q", got)
	}
}

func TestConnectorNameParsesDRMSysnames(t *testing.T) {
	for sysname, want := range map[string]string{
		"card1-eDP-1":    "eDP-1",
		"card0-HDMI-A-1": "HDMI-A-1",
		"card12-DP-3":    "DP-3",
	} {
		if got, ok := connectorName(sysname); !ok || got != want {
			t.Errorf("connectorName(%q) = %q %v, want %q", sysname, got, ok, want)
		}
	}
	for _, sysname := range []string{"card1", "renderD128", "cardX-DP-1", "card-DP-1", "card1-", "i2c-6", "version"} {
		if got, ok := connectorName(sysname); ok {
			t.Errorf("connectorName(%q) = %q, want no connector", sysname, got)
		}
	}
}

func deviceNames(devices []Device) []string {
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	return names
}

func TestForOutputPicksTheDevicesWiredToTheOutput(t *testing.T) {
	devices := []Device{
		{Name: "intel_backlight", Type: TypeRaw, Connector: "eDP-1"},
		{Name: "acpi_video0", Type: TypeFirmware},
		{Name: "ddci2c-6", Type: TypeDDC, Connector: "DP-2"},
		{Name: "ddci2c-3", Type: TypeDDC, Connector: "HDMI-A-1"},
	}
	for output, want := range map[string][]string{
		"eDP-1":    {"intel_backlight"},
		"DP-2":     {"ddci2c-6"},
		"HDMI-A-1": {"ddci2c-3"},
	} {
		got, err := ForOutput(devices, output)
		if err != nil || !slices.Equal(deviceNames(got), want) {
			t.Errorf("ForOutput(%s) = %v, %v; want %v", output, deviceNames(got), err, want)
		}
	}
}

func TestForOutputFallsBackToUnwiredBacklightsOnlyForAnInternalPanel(t *testing.T) {
	devices := []Device{
		{Name: "nvidia_0", Type: TypeRaw},
		{Name: "acpi_video0", Type: TypeFirmware},
		{Name: "ddci2c-6", Type: TypeDDC, Connector: "DP-2"},
		{Name: "ddci2c-9", Type: TypeDDC},
	}
	for _, panel := range []string{"eDP-1", "LVDS-1", "DSI-1"} {
		got, err := ForOutput(devices, panel)
		if err != nil || !slices.Equal(deviceNames(got), []string{"acpi_video0"}) {
			t.Errorf("ForOutput(%s) = %v, %v; want the firmware backlight alone", panel, deviceNames(got), err)
		}
	}
	_, err := ForOutput(devices, "HDMI-A-2")
	want := "no backlight or DDC/CI monitor drives output HDMI-A-2 (outputs with one: DP-2)"
	if err == nil || err.Error() != want {
		t.Errorf("an external output without a device: %v, want %q", err, want)
	}
}

func TestForOutputKeepsTheBestBacklightKindAndEveryMonitor(t *testing.T) {
	devices := []Device{
		{Name: "raw", Type: TypeRaw, Connector: "eDP-1"},
		{Name: "platform", Type: TypePlatform, Connector: "eDP-1"},
		{Name: "ddcci", Type: TypeDDC, Connector: "eDP-1"},
	}
	got, err := ForOutput(devices, "eDP-1")
	if err != nil || !slices.Equal(deviceNames(got), []string{"platform", "ddcci"}) {
		t.Errorf("ForOutput = %v, %v; want platform and the monitor", deviceNames(got), err)
	}
}

func TestForOutputWithNoDevicesNamesNone(t *testing.T) {
	_, err := ForOutput(nil, "eDP-1")
	if err == nil || err.Error() != "no backlight or DDC/CI monitor drives output eDP-1" {
		t.Errorf("no devices: %v", err)
	}
}
