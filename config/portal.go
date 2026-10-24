package config

// PortalConfig is the portal section: policy for lys-shell's
// xdg-desktop-portal backend (`lys-shell portal`).
//
// Desktop-portal policy for sandboxed apps.
type PortalConfig struct {
	// Features to lock down for every app, for kiosk or shared setups.
	Lockdown LockdownConfig `cfg:"lockdown"`
	// Which apps may install launchers without asking.
	DynamicLauncher DynamicLauncherConfig `cfg:"dynamic-launcher"`
}

// DynamicLauncherConfig is the portal.dynamic-launcher section.
//
// Launcher installation policy.
type DynamicLauncherConfig struct {
	// App ids granted an install token, with which they install
	// desktop launchers with no confirmation dialog (a software center,
	// such as `org.gnome.Software`). Every other app asks the user.
	InstallTokenApps []string `cfg:"install-token-apps"`
}

// LockdownConfig is the portal.lockdown section: the Lockdown portal
// properties the frontend consults before letting an app print, save,
// or use a device. They are read from here; the D-Bus properties
// cannot be written.
//
// Features no app may use while set.
type LockdownConfig struct {
	// Refuse printing.
	DisablePrinting bool `cfg:"disable-printing"`
	// Refuse saving files through the file chooser.
	DisableSaveToDisk bool `cfg:"disable-save-to-disk"`
	// Refuse opening URIs and files in other applications.
	DisableApplicationHandlers bool `cfg:"disable-application-handlers"`
	// Refuse location access.
	DisableLocation bool `cfg:"disable-location"`
	// Refuse camera access.
	DisableCamera bool `cfg:"disable-camera"`
	// Refuse microphone access.
	DisableMicrophone bool `cfg:"disable-microphone"`
	// Refuse sound output.
	DisableSoundOutput bool `cfg:"disable-sound-output"`
}

// DefaultsPortal returns the schema defaults: nothing locked down, and
// no app installs launchers without asking.
func DefaultsPortal() PortalConfig {
	return PortalConfig{DynamicLauncher: DynamicLauncherConfig{InstallTokenApps: []string{}}}
}
