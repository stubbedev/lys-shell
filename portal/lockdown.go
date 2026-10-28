package portal

import (
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/config"
)

// LockdownIface is the policy the frontend consults before allowing
// printing, saving, devices and so on.
const LockdownIface = "org.freedesktop.impl.portal.Lockdown"

// lockdownProps are the interface's properties, each read from the
// portal.lockdown config. The config is their only source: the
// interface marks them readwrite, but a D-Bus write is refused, and
// nothing in the frontend writes them.
var lockdownProps = []struct {
	name string
	read func(config.LockdownConfig) bool
}{
	{"disable-printing", func(l config.LockdownConfig) bool { return l.DisablePrinting }},
	{"disable-save-to-disk", func(l config.LockdownConfig) bool { return l.DisableSaveToDisk }},
	{"disable-application-handlers", func(l config.LockdownConfig) bool { return l.DisableApplicationHandlers }},
	{"disable-location", func(l config.LockdownConfig) bool { return l.DisableLocation }},
	{"disable-camera", func(l config.LockdownConfig) bool { return l.DisableCamera }},
	{"disable-microphone", func(l config.LockdownConfig) bool { return l.DisableMicrophone }},
	{"disable-sound-output", func(l config.LockdownConfig) bool { return l.DisableSoundOutput }},
}

// lockdown serves the properties from the live config.
type lockdown struct{ cfg *config.Service }

func (l lockdown) iface() dbusx.Interface {
	props := dbusx.Getters{}
	for _, p := range lockdownProps {
		props[p.name] = func() any { return p.read(l.cfg.Config().Portal.Lockdown) }
	}
	return dbusx.Interface{Name: LockdownIface, Methods: struct{}{}, Properties: props}
}

// watch emits PropertiesChanged for the properties a config change
// flips, so the frontend's cached proxy follows. The returned func
// stops.
func (l lockdown) watch(conn *dbus.Conn) (stop func()) {
	return l.cfg.Subscribe(func(old, new *config.Config) {
		changed := map[string]dbus.Variant{}
		for _, p := range lockdownProps {
			if now := p.read(new.Portal.Lockdown); now != p.read(old.Portal.Lockdown) {
				changed[p.name] = dbus.MakeVariant(now)
			}
		}
		if len(changed) > 0 {
			_ = conn.Emit(ObjectPath, "org.freedesktop.DBus.Properties.PropertiesChanged", LockdownIface, changed, []string{})
		}
	})
}
