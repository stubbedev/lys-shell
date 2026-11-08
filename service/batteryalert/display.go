package batteryalert

import (
	"github.com/stubbedev/lys-desktop/i18n"
	"github.com/stubbedev/lys-desktop/service/upower"
)

type kindDisplay struct {
	icon, label string
}

var genericDisplay = kindDisplay{"ld-battery-full-symbolic", "battery-device-generic"}

var kindDisplays = map[upower.DeviceKind]kindDisplay{
	upower.KindMouse:       {"ld-mouse-symbolic", "battery-device-mouse"},
	upower.KindTouchpad:    {"ld-mouse-symbolic", "battery-device-touchpad"},
	upower.KindKeyboard:    {"ld-keyboard-symbolic", "battery-device-keyboard"},
	upower.KindGamingInput: {"ld-gamepad-2-symbolic", "battery-device-controller"},
	upower.KindHeadset:     {"ld-headset-symbolic", "battery-device-headset"},
	upower.KindHeadphones:  {"ld-headphones-symbolic", "battery-device-headphones"},
	upower.KindSpeakers:    {"ld-speaker-symbolic", "battery-device-speakers"},
	upower.KindOtherAudio:  {"ld-speaker-symbolic", "battery-device-speakers"},
	upower.KindPhone:       {"ld-smartphone-symbolic", "battery-device-phone"},
	upower.KindTablet:      {"ld-tablet-symbolic", "battery-device-tablet"},
	upower.KindPen:         {"ld-tablet-symbolic", "battery-device-pen"},
	upower.KindWearable:    {"ld-watch-symbolic", "battery-device-watch"},
	upower.KindUPS:         {"ld-plug-zap-symbolic", "battery-device-ups"},
	upower.KindPrinter:     {"ld-printer-symbolic", "battery-device-printer"},
	upower.KindCamera:      {"ld-camera-symbolic", "battery-device-camera"},
	upower.KindComputer:    {"ld-laptop-symbolic", "battery-device-computer"},
	upower.KindMonitor:     {"ld-monitor-symbolic", "battery-device-monitor"},
}

func displayOf(k upower.DeviceKind) kindDisplay {
	if d, ok := kindDisplays[k]; ok {
		return d
	}
	return genericDisplay
}

// PeripheralIcon is the themed icon for the peripheral's kind.
func PeripheralIcon(p upower.Peripheral) string { return displayOf(p.Kind).icon }

// PeripheralName is the model UPower reports, or the kind's name when
// it reports none.
func PeripheralName(p upower.Peripheral) string {
	if p.Model != "" {
		return p.Model
	}
	return i18n.T(displayOf(p.Kind).label)
}
