// Package config loads lys-shell's user configuration: the same files the
// Rust shell reads, with the same discovery order, imports, key names,
// defaults, layering (defaults < config file < runtime overrides), and
// failure behavior. The schema is declared once on the Go types
// through `cfg` tags (see fields.go); loading, the runtime layer, the
// CLI's get/set/reset/default, and the JSON Schema all derive from it.
package config

//go:generate go run ./internal/schemadoc/cmd

import (
	"errors"
	"os"
	"path/filepath"
)

// Config is the loaded user configuration.
//
// Main configuration structure for lys-shell.
//
// Represents the complete configuration schema that can be loaded
// from YAML files. All fields have sensible defaults.
type Config struct {
	// YAML files to import and merge before this config.
	//
	// Paths are relative to the config file.
	// Imported values are overridden by values in this file.
	//
	// ```yaml
	// imports: [themes.yaml, modules/clock.yaml]
	// ```
	Imports []string `cfg:"imports,nolayer"`
	// General lys-shell settings.
	General GeneralConfig `cfg:"general"`
	// Bar layout and module placement.
	Bar BarConfig `cfg:"bar"`
	// Dropdown foldout panel sizing.
	Dropdowns DropdownsConfig `cfg:"dropdowns"`
	// Styling configuration (theme, fonts, scale).
	Styling StylingConfig `cfg:"styling"`
	// Module-specific configurations.
	ModulesConfig `cfg:"modules"`
	// On-screen display settings.
	Osd OsdConfig `cfg:"osd"`
	// Lock screen settings.
	Lock LockConfig `cfg:"lock"`
	// Greeter (display manager) settings.
	Greeter GreeterConfig `cfg:"greeter"`
	// Screen-share picker settings.
	SharePicker SharePickerConfig `cfg:"share-picker"`
	// Application launcher (rofi replacement) settings.
	Launcher LauncherConfig `cfg:"launcher"`
	// Animation settings.
	Animations AnimationsConfig `cfg:"animations"`
	// Wallpaper service settings.
	Wallpaper WallpaperConfig `cfg:"wallpaper"`
	// Desktop-portal policy for sandboxed apps.
	Portal PortalConfig `cfg:"portal"`
}

// Defaults returns the schema defaults for every section.
func Defaults() *Config {
	return &Config{
		Imports:       []string{},
		General:       DefaultsGeneral(),
		Bar:           DefaultsBar(),
		Dropdowns:     DropdownsConfig{},
		Styling:       DefaultsStyling(),
		ModulesConfig: DefaultsModules(),
		Osd:           DefaultsOsd(),
		Lock:          DefaultsLock(),
		Greeter:       DefaultsGreeter(),
		SharePicker:   DefaultsSharePicker(),
		Launcher:      DefaultsLauncher(),
		Animations:    DefaultsAnimations(),
		Wallpaper:     DefaultsWallpaper(),
		Portal:        DefaultsPortal(),
	}
}

// Dir returns the lys-shell configuration directory:
// $XDG_CONFIG_HOME/lys-shell, or ~/.config/lys-shell when XDG_CONFIG_HOME is
// unset. Both variables unset is an error — there is nowhere to look.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "lys-shell"), nil
	}
	home := os.Getenv("HOME")
	if home == "" {
		return "", errors.New("config: neither XDG_CONFIG_HOME nor HOME is set")
	}
	return filepath.Join(home, ".config", "lys-shell"), nil
}

// DiscoverMain resolves the user's main config file inside dir:
// config.yaml, else an existing config.yml. When neither exists yet,
// the config.yaml path is returned, so fresh installs and explicit
// missing-file handling agree on one path.
func DiscoverMain(dir string) string {
	main := filepath.Join(dir, "config.yaml")
	if yml := filepath.Join(dir, "config.yml"); !fileExists(main) && fileExists(yml) {
		return yml
	}
	return main
}

// LoadFile loads one main config file with its imports onto the
// defaults. A file-level failure (unreadable, unparseable, a broken
// import) returns the defaults with that error. A bad value in one field keeps
// that field's default and applies the rest; those field errors are
// returned joined, as diagnostics.
func LoadFile(path string) (*Config, error) {
	tree, err := loadTree(path)
	if err != nil {
		return Defaults(), err
	}
	cfg := Defaults()
	var diags []error
	apply := &layerApply{kind: configLayer, sink: func(d Diagnostic) { diags = append(diags, d) }}
	_ = apply.applyContainer(valueOf(cfg), tree, "")
	return cfg, errors.Join(diags...)
}
