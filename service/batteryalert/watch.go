package batteryalert

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/upower"
)

// Config is what the watcher reads from the user's configuration on
// every change, so a reload applies to the next reading.
type Config struct {
	Notify    bool
	ChargedAt uint8
	SaverAt   SaverAt
}

// Notifier posts a critical notification; desktopnotify's Sender is
// one.
type Notifier interface {
	SendCritical(ctx context.Context, appName, summary, body, appIcon string) (uint32, error)
}

// callTimeout bounds each D-Bus round trip one change causes.
const callTimeout = 5 * time.Second

// appName is the notifications' application name.
const appName = "lys-shell"

type watcher struct {
	battery     upower.Source
	profiles    powerprofiles.Source
	notifier    Notifier
	config      func() Config
	display     Reading
	peripherals map[dbus.ObjectPath]Reading
	saver       Saver
}

// Watch follows the battery and the peripherals until ctx ends: each
// PropertiesChanged, DeviceAdded and DeviceRemoved is read and stepped
// through Next and the saver. Nothing runs between signals. profiles
// may be nil, which leaves the power profile alone.
func Watch(ctx context.Context, battery upower.Source, profiles powerprofiles.Source, notifier Notifier, config func() Config) error {
	displayTicks, stopDisplay, err := battery.Subscribe(ctx)
	if err != nil {
		return fmt.Errorf("battery alerts: follow the battery: %w", err)
	}
	peripheralTicks, stopPeripherals, err := battery.SubscribePeripherals(ctx)
	if err != nil {
		stopDisplay()
		return fmt.Errorf("battery alerts: follow the peripherals: %w", err)
	}
	w := &watcher{battery: battery, profiles: profiles, notifier: notifier, config: config, peripherals: map[dbus.ObjectPath]Reading{}}
	go func() {
		defer stopDisplay()
		defer stopPeripherals()
		w.readDisplay(ctx)
		w.readPeripherals(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-displayTicks:
				if !ok {
					return
				}
				w.readDisplay(ctx)
			case _, ok := <-peripheralTicks:
				if !ok {
					return
				}
				w.readPeripherals(ctx)
			}
		}
	}()
	return nil
}

func (w *watcher) readDisplay(ctx context.Context) {
	call, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	dev, err := w.battery.Read(call)
	if err != nil {
		log.Printf("battery alerts: %v", err)
		return
	}
	if !dev.IsPresent {
		w.display = Reading{}
		return
	}
	cur := FromDevice(dev)
	cfg := w.config()
	if level, ok := Next(w.display, cur, cfg.ChargedAt); ok && cfg.Notify {
		w.send(call, displaySummary(level), displayBody(level, cur), displayIcon(level))
	}
	w.display = cur
	w.stepSaver(call, cfg.SaverAt, cur)
}

func (w *watcher) stepSaver(ctx context.Context, at SaverAt, cur Reading) {
	if w.profiles == nil || at == SaverNever && !w.saver.Engaged {
		return
	}
	snap, err := w.profiles.Read(ctx)
	if err != nil {
		log.Printf("battery alerts: power profile: %v", err)
		return
	}
	if !snap.Available {
		return
	}
	next, profile, change := w.saver.Step(at, cur, snap.Active)
	w.saver = next
	if !change {
		return
	}
	if err := w.profiles.SetActive(ctx, profile); err != nil {
		log.Printf("battery alerts: switch to the %s profile: %v", profile, err)
	}
}

func (w *watcher) readPeripherals(ctx context.Context) {
	call, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	list, err := w.battery.Peripherals(call)
	if err != nil {
		log.Printf("battery alerts: %v", err)
		return
	}
	notify := w.config().Notify
	seen := make(map[dbus.ObjectPath]Reading, len(list))
	for _, p := range list {
		cur := FromPeripheral(p)
		if level, ok := Next(w.peripherals[p.Path], cur, 0); ok && notify {
			name := i18n.Str("device", PeripheralName(p))
			w.send(call, i18n.T(peripheralSummaryIDs[level], name), remaining(cur), PeripheralIcon(p))
		}
		seen[p.Path] = cur
	}
	w.peripherals = seen
}

func (w *watcher) send(ctx context.Context, summary, body, icon string) {
	if _, err := w.notifier.SendCritical(ctx, appName, summary, body, icon); err != nil {
		log.Printf("battery alerts: %v", err)
	}
}

var peripheralSummaryIDs = map[Level]string{
	LevelLow:      "battery-alert-device-low",
	LevelCritical: "battery-alert-device-critical",
}

func displaySummary(level Level) string {
	switch level {
	case LevelLow:
		return i18n.T("battery-alert-low")
	case LevelCritical:
		return i18n.T("battery-alert-critical")
	}
	return i18n.T("battery-alert-charged")
}

func displayBody(level Level, cur Reading) string {
	if level == LevelCharged {
		return i18n.T("battery-alert-charged-body", percentArg(cur))
	}
	return remaining(cur)
}

func displayIcon(level Level) string {
	switch level {
	case LevelLow:
		return "ld-battery-low-symbolic"
	case LevelCritical:
		return "ld-battery-warning-symbolic"
	}
	return "ld-battery-full-symbolic"
}

func remaining(cur Reading) string { return i18n.T("battery-alert-remaining", percentArg(cur)) }

func percentArg(cur Reading) i18n.Arg {
	return i18n.Str("percent", strconv.FormatInt(int64(math.Round(cur.Percentage)), 10))
}
