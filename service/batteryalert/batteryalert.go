// Package batteryalert decides when a battery deserves a notification
// and when the power profile follows the battery: pure functions over
// consecutive UPower readings, so every transition is a table test.
package batteryalert

import (
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/upower"
)

// Reading is the part of a UPower device the alerts follow.
type Reading struct {
	Warning    upower.WarningLevel
	Percentage float64
	State      upower.DeviceState
}

// FromDevice is the DisplayDevice's reading.
func FromDevice(d upower.Device) Reading {
	return Reading{Warning: d.WarningLevel, Percentage: d.Percentage, State: d.State}
}

// FromPeripheral is a peripheral's reading.
func FromPeripheral(p upower.Peripheral) Reading {
	return Reading{Warning: p.WarningLevel, Percentage: p.Percentage, State: p.State}
}

// OnAC reports a battery being charged or held full: what plugging in
// looks like from the device.
func (r Reading) OnAC() bool {
	switch r.State {
	case upower.StateCharging, upower.StateFullyCharged, upower.StatePendingCharge:
		return true
	}
	return false
}

// Level is what an alert announces.
type Level uint8

// Alert levels.
const (
	LevelLow Level = iota + 1
	LevelCritical
	LevelCharged
)

// Next is the alert the step from prev to cur raises, if any. Low and
// Critical follow UPower's WarningLevel, so the thresholds are UPower's
// PercentageLow and PercentageCritical: one alert on entering each, none
// while staying, and since plugging in clears the warning, unplugging
// again re-arms both. Charged fires once when a battery charging
// crosses chargedAt; zero turns it off.
func Next(prev, cur Reading, chargedAt uint8) (Level, bool) {
	if r := cur.Warning.Rank(); r > prev.Warning.Rank() {
		if r == upower.WarningCritical.Rank() {
			return LevelCritical, true
		}
		return LevelLow, true
	}
	limit := float64(chargedAt)
	if chargedAt > 0 && prev.OnAC() && cur.OnAC() && prev.Percentage < limit && cur.Percentage >= limit {
		return LevelCharged, true
	}
	return 0, false
}

// SaverAt is the warning level that switches to power-saver.
type SaverAt uint8

// Saver thresholds.
const (
	SaverNever SaverAt = iota
	SaverLow
	SaverCritical
)

// Saver is the automatic power-saver's memory: whether it switched,
// and the profile it switched away from ("" when power-saver was
// already active, so there is nothing to give back).
type Saver struct {
	Engaged bool
	Restore string
}

// Step is the saver after cur with active the current profile, and the
// profile to switch to, if any: power-saver on reaching at while on
// battery, the remembered profile once the battery is on AC again.
func (s Saver) Step(at SaverAt, cur Reading, active string) (Saver, string, bool) {
	switch {
	case s.Engaged && cur.OnAC():
		return Saver{}, s.Restore, s.Restore != ""
	case s.Engaged, at == SaverNever, cur.OnAC(), cur.Warning.Rank() < at.warning().Rank():
		return s, "", false
	case active == powerprofiles.ProfilePowerSaver:
		return Saver{Engaged: true}, "", false
	}
	return Saver{Engaged: true, Restore: active}, powerprofiles.ProfilePowerSaver, true
}

func (a SaverAt) warning() upower.WarningLevel {
	if a == SaverLow {
		return upower.WarningLow
	}
	return upower.WarningCritical
}
