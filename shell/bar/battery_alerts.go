package bar

import (
	"context"
	"log"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/desktopnotify"
	"github.com/stubbedev/lys-shell/service/batteryalert"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/upower"
)

// startBatteryAlerts runs the low-battery notifications and the
// automatic power-saver for the session, whether or not a battery
// module is placed: a battery running flat matters either way.
func startBatteryAlerts(battery upower.Source, profiles powerprofiles.Source, session *dbus.Conn, current func() *config.Config) func() {
	ctx, cancel := context.WithCancel(context.Background())
	err := batteryalert.Watch(ctx, battery, profiles, desktopnotify.NewSender(session), func() batteryalert.Config {
		return batteryAlertConfig(current())
	})
	if err != nil {
		log.Printf("battery alerts: off: %v", err)
	}
	return cancel
}

func batteryAlertConfig(cfg *config.Config) batteryalert.Config {
	return batteryalert.Config{
		Notify:    cfg.Battery.NotifyLow,
		ChargedAt: uint8(cfg.Battery.NotifyChargedAt),
		SaverAt:   saverAt(cfg.PowerProfiles.AutoSaverAt),
	}
}

func saverAt(level config.AutoSaverLevel) batteryalert.SaverAt {
	switch level {
	case config.AutoSaverLow:
		return batteryalert.SaverLow
	case config.AutoSaverCritical:
		return batteryalert.SaverCritical
	}
	return batteryalert.SaverNever
}
