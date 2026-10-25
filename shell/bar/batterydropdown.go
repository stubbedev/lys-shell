package bar

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strconv"
	"sync"

	"github.com/stubbedev/lys-ui/render"
	"github.com/stubbedev/lys-ui/widget"
	"github.com/stubbedev/lys-ui/widget/css"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/upower"
)

// batteryCharging is battery_section/methods.rs's is_charging.
func batteryCharging(state upower.DeviceState) bool {
	return state == upower.StateCharging || state == upower.StatePendingCharge
}

// batteryStateLabel is methods.rs's state_label: critical first, then
// charging, plugged in, or on battery.
func batteryStateLabel(dev upower.Device) string {
	switch {
	case dev.WarningLevel.Low():
		return i18n.T("dropdown-battery-critical")
	case batteryCharging(dev.State):
		return i18n.T("dropdown-battery-charging")
	case dev.State == upower.StateFullyCharged:
		return i18n.T("dropdown-battery-plugged-in")
	default:
		return i18n.T("dropdown-battery-on-battery")
	}
}

// batteryTimeDisplay is methods.rs's time_display: the time until full
// while charging, else the time remaining; empty when unknown.
func batteryTimeDisplay(dev upower.Device) string {
	remaining, id := dev.TimeToEmpty, "dropdown-battery-time-remaining"
	if batteryCharging(dev.State) {
		remaining, id = dev.TimeToFull, "dropdown-battery-time-until-full"
	}
	seconds := int64(remaining.Seconds())
	if seconds <= 0 {
		return ""
	}
	hours, minutes := seconds/3600, (seconds%3600)/60
	duration := i18n.T("dropdown-battery-duration-m", i18n.Str("minutes", strconv.FormatInt(minutes, 10)))
	if hours > 0 {
		duration = i18n.T("dropdown-battery-duration-hm",
			i18n.Str("hours", strconv.FormatInt(hours, 10)),
			i18n.Str("minutes", fmt.Sprintf("%02d", minutes)))
	}
	return i18n.T(id, i18n.Str("duration", duration))
}

// formatWatts is helpers.rs's format_watts: one decimal under 10W.
func formatWatts(watts float64) string {
	if watts < 10 {
		return strconv.FormatFloat(watts, 'f', 1, 64) + "W"
	}
	return strconv.FormatFloat(watts, 'f', 0, 64) + "W"
}

// formatWattHours is format_watt_hours.
func formatWattHours(wh float64) string {
	if wh < 10 {
		return strconv.FormatFloat(wh, 'f', 1, 64) + " Wh"
	}
	return strconv.FormatFloat(wh, 'f', 0, 64) + " Wh"
}

// batteryLevelClass is gauge_class and hero_pct_class: "crit" at a low
// warning level, "warn" at or under 20%, else fallback ("good" for the
// gauge, "" for the hero percentage).
func batteryLevelClass(dev upower.Device, fallback string) string {
	switch {
	case dev.WarningLevel.Low():
		return "crit"
	case dev.Percentage <= 20:
		return "warn"
	}
	return fallback
}

// heroStateClass is hero_state_class.
func heroStateClass(dev upower.Device) string {
	switch {
	case dev.WarningLevel.Low():
		return "crit"
	case batteryCharging(dev.State):
		return "good"
	}
	return ""
}

// healthClass is health_class.
func healthClass(capacity float64) string {
	switch {
	case capacity <= 0:
		return "unknown"
	case capacity >= 80:
		return "good"
	case capacity >= 50:
		return "fair"
	}
	return "poor"
}

// healthValue is health_value.
func healthValue(capacity float64) string {
	if capacity <= 0 {
		return "--"
	}
	return strconv.FormatFloat(capacity, 'f', 0, 64) + "%"
}

// resumeThreshold is resume_threshold: five under the limit, floored
// at zero.
func resumeThreshold(end uint32) uint32 {
	if end < 5 {
		return 0
	}
	return end - 5
}

// batteryVariants is every variant class a battery label may carry.
var batteryVariants = []string{"crit", "warn", "good", "fair", "poor", "unknown"}

// batteryProfile is one segment of the power-profile control.
type batteryProfile struct {
	name, icon, label string
}

var batteryProfiles = []batteryProfile{
	{powerprofiles.ProfilePowerSaver, "ld-leaf-symbolic", "dropdown-battery-profile-saver"},
	{powerprofiles.ProfileBalanced, "ld-scale-symbolic", "dropdown-battery-profile-balanced"},
	{powerprofiles.ProfilePerformance, "ld-rocket-symbolic", "dropdown-battery-profile-performance"},
}

// batteryView is the battery dropdown: the battery section (hero,
// gauge, details, charge limit, or the empty state) over the power
// profile section, each following its service while open.
type batteryView struct {
	ctx  ModuleContext
	font render.Font
	px   float64

	*widget.Box
	body *widget.Stack

	heroPct, heroState, heroTime, heroInput *widget.Label
	gauge                                   *widget.LevelBar
	drawValue, drawLabel                    *widget.Label
	capacityValue, capacityLabel            *widget.Label
	healthDot                               *widget.Box
	healthText                              *widget.Label
	chargeCard                              *widget.Box
	charge                                  *widget.Item
	chargeUnsupported                       *widget.Alert
	chargeSwitch                            *widget.Switch
	// syncing marks programmatic switch moves (block_signal).
	syncing bool

	profileButtons map[string]*widget.ToggleButton
	// marking blocks the toggles' handler while the view sets them
	// (block_signal): only a user's toggle selects a profile.
	marking             bool
	profilesUnavailable *widget.Alert
	activeProfile       string

	// device and profiles read the two services; a write's re-read goes
	// through them too, so it can never land after a newer change.
	device   *refresher[batteryRead]
	profiles *refresher[*powerprofiles.Snapshot]

	once   sync.Once
	cancel context.CancelFunc
}

func batteryDropdown(ctx ModuleContext) widget.Widget {
	font, px := dropdownFont(ctx)
	v := &batteryView{ctx: ctx, font: font, px: px, cancel: func() {}}
	v.Box = widget.NewBox(widget.Column, 0, 0)
	v.AddClass("dropdown", "battery-dropdown")
	v.Append(dropdownHeader(font, "ld-battery-full-symbolic", i18n.T("dropdown-battery-title")), false)
	content := dropdownContent()
	v.body = widget.NewStack()
	empty := emptyState(font, px, "ld-unplug-symbolic",
		i18n.T("dropdown-battery-no-battery-title"), i18n.T("dropdown-battery-no-battery-description"))
	v.body.Add("empty", empty)
	v.body.Add("battery", v.batterySection())
	content.Append(v.body, false)
	content.Append(v.profileSection(), false)
	v.Append(content, true)
	v.applyDevice(v.readDevice(context.Background()))
	v.applyProfiles(v.readProfiles(context.Background()))
	v.follow()
	return v
}

// label is an empty label at the dropdown's size; its classes (the
// lys-ui typography among them) set the rest.
func (v *batteryView) label(classes ...string) *widget.Label {
	l := widget.NewLabel(v.font, v.px, "", 0)
	l.AddClass(classes...)
	return l
}

// classed is a widget carrying classes: setVariant swaps variants on
// labels and the health-dot box alike.
type classed interface {
	widget.Widget
	AddClass(names ...string)
	RemoveClass(names ...string)
}

// setVariant swaps w's variant class to class ("" for none); the
// variant's ink is the stylesheet's state rule.
func (v *batteryView) setVariant(w classed, class string) {
	w.RemoveClass(batteryVariants...)
	if class != "" {
		w.AddClass(class)
	}
}

// batterySection builds battery_content on lys-ui's parts: the hero's
// h1 percentage beside its state, the level bar, the details under it,
// and the charge limit as an Item in a card, or an alert where the
// battery has none.
func (v *batteryView) batterySection() widget.Widget {
	th := widget.Current()
	col := widget.NewBox(widget.Column, 2*th.Spacing, 0)

	hero := widget.NewBox(widget.Row, 2*th.Spacing, 0)
	hero.AddClass("battery-hero")
	v.heroPct = v.label("battery-hero-pct", css.H1, css.Numeric)
	hero.Append(v.heroPct, false)
	meta := widget.NewBox(widget.Column, th.Spacing/2, 0)
	meta.AddClass("battery-hero-meta")
	v.heroState = v.label("battery-hero-state", css.Heading)
	v.heroTime = v.label("battery-hero-time", css.DimLabel)
	v.heroInput = v.label("battery-hero-time", css.DimLabel)
	meta.Append(v.heroState, false)
	meta.Append(v.heroTime, false)
	meta.Append(v.heroInput, false)
	hero.AppendAligned(meta, true, widget.AlignCenter) // set_valign Center

	v.gauge = widget.NewLevelBar(0)
	v.gauge.AddClass("battery-gauge")

	// details is a CenterBox: the capacity column sits geometrically
	// centered, not stretched between two expanding boxes.
	v.drawValue, v.drawLabel = v.detailLabels()
	v.capacityValue, v.capacityLabel = v.detailLabels()
	details := widget.NewCenterBox(
		v.detail(v.drawValue, v.drawLabel, render.AlignStart),
		v.detail(v.capacityValue, v.capacityLabel, render.AlignStart),
		v.healthDetail())
	details.AddClass("battery-details")
	col.Append(dropdownGroup(hero, v.gauge, details), false)

	v.charge = widget.NewItem(v.font, "", "")
	v.charge.SetSmall(true)
	v.charge.AddClass("charge-limit")
	v.chargeSwitch = widget.NewSwitch(false)
	v.chargeSwitch.OnChanged = v.chargeToggled
	v.charge.AddAction(v.chargeSwitch)
	v.chargeCard = widget.NewBox(widget.Column, 0, 0)
	v.chargeCard.AddClass(css.Card)
	v.chargeCard.Append(v.charge, false)
	v.chargeUnsupported = v.infoNote("charge-limit-not-supported", i18n.T("dropdown-battery-charge-limit-not-supported"))
	col.Append(dropdownSection(sectionLabel(v.font, i18n.T("dropdown-battery-charge-limit")), v.chargeCard, v.chargeUnsupported), false)
	return col
}

// detailLabels is one detail's value, in the heading weight, and its
// caption.
func (v *batteryView) detailLabels() (value, label *widget.Label) {
	value = v.label("battery-detail-value", css.Heading)
	label = v.label("battery-detail-label", css.Caption, css.DimLabel)
	return value, label
}

// detail is one battery-detail column: the value over the label, the
// text alignment set where Rust sets set_halign. The capacity column
// keeps the default — the CenterBox centers the slot, not the text.
func (v *batteryView) detail(value, label *widget.Label, align render.Alignment) *widget.Box {
	box := widget.NewBox(widget.Column, 0, 0)
	box.AddClass("battery-detail")
	value.SetAlignment(align)
	label.SetAlignment(align)
	box.Append(value, false)
	box.Append(label, false)
	return box
}

// healthDetail is the end slot: the health dot, which the stylesheet
// colors by its variant, beside the value, the label under,
// both end-aligned.
func (v *batteryView) healthDetail() *widget.Box {
	th := widget.Current()
	health := widget.NewBox(widget.Row, th.Spacing/2, 0) // health_indicator
	v.healthDot = statusDot("health-dot")
	var healthLabel *widget.Label
	v.healthText, healthLabel = v.detailLabels()
	healthLabel.SetText(i18n.T("dropdown-battery-health"))
	health.AppendAligned(v.healthDot, false, widget.AlignCenter) // set_valign Center
	health.Append(v.healthText, false)
	box := widget.NewBox(widget.Column, 0, 0)
	box.AddClass("battery-detail")
	box.AppendAligned(health, false, widget.AlignEnd) // set_halign End
	healthLabel.SetAlignment(render.AlignEnd)
	box.Append(healthLabel, false)
	return box
}

// infoNote is the note the Rust sections show when a feature is
// missing: lys-ui's alert with the info icon.
func (v *batteryView) infoNote(class, text string) *widget.Alert {
	a := widget.NewAlert(v.font, text, "")
	a.SetIcon(widget.NewThemeIcon("ld-info-symbolic", itemIconPx))
	a.AddClass(class)
	return a
}

// profileSection builds PowerProfileSection: the label, the three
// segments, and the daemon-missing note. The segments are toggle
// buttons in a box carrying lys-ui's togglegroup element, so they take
// its segmented-control look while each stays its own toggle: the
// daemon can withhold one, and an unknown profile checks none.
func (v *batteryView) profileSection() widget.Widget {
	seg := widget.NewBox(widget.Row, 0, 0)
	seg.SetElement("togglegroup")
	seg.AddClass("profile-seg")
	// set_homogeneous (power_profile/mod.rs:49): the segments equalize
	// to the widest.
	seg.SetHomogeneous(true)
	v.profileButtons = make(map[string]*widget.ToggleButton, len(batteryProfiles))
	var first *widget.ToggleButton
	for _, p := range batteryProfiles {
		content := widget.NewButtonContent(v.font, v.px, p.icon, i18n.T(p.label))
		name := p.name
		// Grouped gtk::ToggleButtons: activating one releases the
		// others, and the toggled handler selects on activation only.
		b := widget.NewToggleButton(content, 0, 0)
		b.AddClass("profile-seg-btn")
		b.OnToggled = func(active bool) {
			if active && !v.marking {
				v.selectProfile(name)
			}
		}
		if first == nil {
			first = b
		} else {
			b.SetGroup(first)
		}
		v.profileButtons[name] = b
		seg.Append(b, true)
	}
	v.profilesUnavailable = v.infoNote("power-profile-not-available", i18n.T("dropdown-battery-power-profile-not-available"))
	return dropdownSection(sectionLabel(v.font, i18n.T("dropdown-battery-power-profile")), seg, v.profilesUnavailable)
}

func (v *batteryView) readDevice(ctx context.Context) (upower.Device, bool) {
	if v.ctx.Battery == nil {
		return upower.Device{}, false
	}
	dev, err := v.ctx.Battery.Read(ctx)
	if err != nil {
		log.Printf("battery: %v", err)
		return upower.Device{}, false
	}
	return dev, true
}

// batteryRead is one device read: ok is false when the read failed.
type batteryRead struct {
	dev upower.Device
	ok  bool
}

// applyDevice is refresh_battery_state plus the view's #[watch]es.
func (v *batteryView) applyDevice(dev upower.Device, ok bool) {
	if !ok || !dev.IsPresent {
		v.body.Show("empty")
		return
	}
	v.body.Show("battery")
	charging := batteryCharging(dev.State)

	v.heroPct.SetText(strconv.Itoa(int(dev.Percentage)) + "%")
	v.setVariant(v.heroPct, batteryLevelClass(dev, ""))
	v.heroState.SetText(batteryStateLabel(dev))
	v.setVariant(v.heroState, heroStateClass(dev))
	remaining := batteryTimeDisplay(dev)
	v.heroTime.SetText(remaining)
	v.heroTime.SetVisible(remaining != "")
	v.heroInput.SetText(i18n.T("dropdown-battery-input-watts", i18n.Str("watts", formatWatts(dev.EnergyRate))))
	v.heroInput.SetVisible(charging && dev.EnergyRate > 0)

	v.gauge.SetValue(min(max(dev.Percentage, 0), 100) / 100)
	v.gauge.RemoveClass(batteryVariants...)
	v.gauge.AddClass(batteryLevelClass(dev, "good"))

	v.drawValue.SetText(formatWatts(dev.EnergyRate))
	v.capacityValue.SetText(formatWattHours(dev.EnergyFull))
	if charging {
		v.drawLabel.SetText(i18n.T("dropdown-battery-input"))
		v.capacityLabel.SetText(i18n.T("dropdown-battery-charged"))
	} else {
		v.drawLabel.SetText(i18n.T("dropdown-battery-draw"))
		v.capacityLabel.SetText(i18n.T("dropdown-battery-capacity"))
	}
	v.setVariant(v.healthDot, healthClass(dev.Capacity))
	v.healthText.SetText(healthValue(dev.Capacity))

	v.chargeCard.SetVisible(dev.ChargeThresholdSupported)
	v.chargeUnsupported.SetVisible(!dev.ChargeThresholdSupported)
	v.charge.SetTitle(i18n.T("dropdown-battery-limit-to",
		i18n.Str("threshold", strconv.FormatUint(uint64(dev.ChargeEndThreshold), 10))))
	v.charge.SetDescription(i18n.T("dropdown-battery-resumes-at",
		i18n.Str("threshold", strconv.FormatUint(uint64(resumeThreshold(dev.ChargeEndThreshold)), 10))))
	v.syncing = true
	v.chargeSwitch.SetOn(dev.ChargeThresholdEnabled)
	v.syncing = false
}

// chargeToggled is handle_charge_limit_toggled: the write runs off the
// loop and the section re-reads after it.
func (v *batteryView) chargeToggled(enabled bool) {
	if v.syncing || v.ctx.Battery == nil {
		return
	}
	src := v.ctx.Battery
	go func() {
		ctx := context.Background()
		if err := src.EnableChargeThreshold(ctx, enabled); err != nil {
			log.Printf("battery: charge threshold toggle: %v", err)
		}
		if v.device != nil {
			v.device.request()
		}
	}()
}

// readProfiles reads the daemon; nil when it is absent, so every
// segment disables (ServiceUnavailable).
func (v *batteryView) readProfiles(ctx context.Context) *powerprofiles.Snapshot {
	if v.ctx.PowerProfiles == nil {
		return nil
	}
	snap, err := v.ctx.PowerProfiles.Read(ctx)
	if err != nil || !snap.Available {
		return nil
	}
	return &snap
}

// applyProfiles marks the active segment and disables the ones the
// daemon does not offer; none offered shows the note.
func (v *batteryView) applyProfiles(snap *powerprofiles.Snapshot) {
	var available []string
	v.activeProfile = powerprofiles.ProfileBalanced
	if snap != nil {
		available = snap.Profiles
		v.activeProfile = snap.Active
	}
	for _, p := range batteryProfiles {
		has := slices.Contains(available, p.name)
		v.profileButtons[p.name].SetEnabled(has)
		v.markProfile(p.name, has && p.name == v.activeProfile)
	}
	v.profilesUnavailable.SetVisible(len(available) == 0)
}

// markProfile sets a segment's toggle without selecting anything (the
// #[block_signal] set_active); :checked selects the sheet's accent
// rule.
func (v *batteryView) markProfile(name string, active bool) {
	v.marking = true
	v.profileButtons[name].SetActive(active)
	v.marking = false
}

// selectProfile is select_profile: the segment toggles at once, the
// write runs off the loop, and the daemon's answer settles it.
func (v *batteryView) selectProfile(name string) {
	src := v.ctx.PowerProfiles
	if src == nil || name == v.activeProfile {
		return
	}
	for _, p := range batteryProfiles {
		v.markProfile(p.name, p.name == name)
	}
	v.activeProfile = name
	go func() {
		ctx := context.Background()
		if err := src.SetActive(ctx, name); err != nil {
			log.Printf("battery: power profile switch: %v", err)
		}
		if v.profiles != nil {
			v.profiles.request()
		}
	}()
}

// follow tracks both services until the dropdown closes.
func (v *batteryView) follow() {
	life, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	if src := v.ctx.Battery; src != nil {
		v.device = followTicks(v.ctx, life, "battery", src.Subscribe,
			func(ctx context.Context) batteryRead {
				dev, ok := v.readDevice(ctx)
				return batteryRead{dev, ok}
			},
			func(r batteryRead) { v.applyDevice(r.dev, r.ok) })
	}
	if src := v.ctx.PowerProfiles; src != nil {
		v.profiles = followTicks(v.ctx, life, "power profiles", src.Subscribe, v.readProfiles, v.applyProfiles)
	}
}

// dropdownClosed implements dropdownCloser.
func (v *batteryView) dropdownClosed() { v.once.Do(func() { v.cancel() }) }
