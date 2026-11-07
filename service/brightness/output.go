package brightness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-shell/internal/drmconn"
)

// DRMDir is the sysfs class directory of DRM cards and connectors; a
// var so tests can repoint it.
var DRMDir = drmconn.Dir

// I2CAdapterDir lists every I²C adapter as a link into its device
// tree; a var so tests can repoint it.
var I2CAdapterDir = "/sys/bus/i2c/devices"

func ancestorConnector(path string) string {
	for dir := path; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if name, ok := drmconn.Name(filepath.Base(dir)); ok {
			return name
		}
	}
	return ""
}

func backlightConnector(name string) (string, error) {
	path, err := filepath.EvalSymlinks(filepath.Join(BacklightDir, name))
	if err != nil {
		return "", fmt.Errorf("brightness: resolve backlight %s: %w", name, err)
	}
	return ancestorConnector(path), nil
}

func i2cConnector(bus uint32) (string, error) {
	adapter, err := filepath.EvalSymlinks(filepath.Join(I2CAdapterDir, i2cNodePrefix+strconv.FormatUint(uint64(bus), 10)))
	if err != nil {
		return "", fmt.Errorf("brightness: resolve i2c adapter %d: %w", bus, err)
	}
	if name := ancestorConnector(adapter); name != "" {
		return name, nil
	}
	entries, err := os.ReadDir(DRMDir)
	if err != nil {
		return "", fmt.Errorf("brightness: list DRM connectors: %w", err)
	}
	for _, entry := range entries {
		name, ok := drmconn.Name(entry.Name())
		if !ok {
			continue
		}
		if ddc, err := filepath.EvalSymlinks(filepath.Join(DRMDir, entry.Name(), "ddc")); err == nil && ddc == adapter {
			return name, nil
		}
	}
	return "", nil
}

func internalPanel(output string) bool {
	for _, prefix := range []string{"eDP-", "LVDS-", "DSI-"} {
		if strings.HasPrefix(output, prefix) {
			return true
		}
	}
	return false
}

func typeRank(t Type) int {
	switch t {
	case TypeFirmware:
		return 0
	case TypePlatform:
		return 1
	}
	return 2
}

// ForOutput picks the devices that drive output: the backlights and
// DDC/CI monitors wired to its connector, else, for an internal panel,
// the backlights wired to no connector. Of several backlights only the
// best kind is kept (firmware, then platform, then raw), as the kernel's
// backlight ABI advises.
func ForOutput(devices []Device, output string) ([]Device, error) {
	picked := slices.DeleteFunc(slices.Clone(devices), func(d Device) bool { return d.Connector != output })
	if len(picked) == 0 && internalPanel(output) {
		picked = slices.DeleteFunc(slices.Clone(devices), func(d Device) bool { return d.Connector != "" || d.Type == TypeDDC })
	}
	if len(picked) == 0 {
		return nil, noDeviceError(devices, output)
	}
	best := -1
	for _, d := range picked {
		if d.Type != TypeDDC && (best < 0 || typeRank(d.Type) < best) {
			best = typeRank(d.Type)
		}
	}
	return slices.DeleteFunc(picked, func(d Device) bool {
		return d.Type != TypeDDC && typeRank(d.Type) != best
	}), nil
}

func noDeviceError(devices []Device, output string) error {
	var wired []string
	for _, d := range devices {
		if d.Connector != "" && !slices.Contains(wired, d.Connector) {
			wired = append(wired, d.Connector)
		}
	}
	if len(wired) == 0 {
		return fmt.Errorf("no backlight or DDC/CI monitor drives output %s", output)
	}
	slices.Sort(wired)
	return fmt.Errorf("no backlight or DDC/CI monitor drives output %s (outputs with one: %s)", output, strings.Join(wired, ", "))
}
