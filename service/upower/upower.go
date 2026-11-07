// Package upower reads battery state from UPower over the system D-Bus,
// the Go counterpart of crates/wayle-battery's zbus proxy: the
// DisplayDevice aggregate, the peripherals UPower enumerates beside it,
// and live updates through PropertiesChanged, DeviceAdded and
// DeviceRemoved.
package upower

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// DeviceState mirrors UPower's device state enum
// (types.rs's DeviceState/From<u32>).
type DeviceState uint8

// Device states.
const (
	StateUnknown          DeviceState = iota
	StateCharging                     // 1
	StateDischarging                  // 2
	StateEmpty                        // 3
	StateFullyCharged                 // 4
	StatePendingCharge                // 5
	StatePendingDischarge             // 6
)

// StateFromUint32 maps UPower's wire value; anything else is Unknown.
func StateFromUint32(v uint32) DeviceState {
	switch v {
	case 1:
		return StateCharging
	case 2:
		return StateDischarging
	case 3:
		return StateEmpty
	case 4:
		return StateFullyCharged
	case 5:
		return StatePendingCharge
	case 6:
		return StatePendingDischarge
	}
	return StateUnknown
}

// WarningLevel mirrors UPower's warning level enum
// (types.rs's WarningLevel/From<u32>).
type WarningLevel uint8

// Warning levels.
const (
	WarningUnknown     WarningLevel = iota
	WarningNone                     // 1
	WarningDischarging              // 2, UPSes only
	WarningLow                      // 3
	WarningCritical                 // 4
	WarningAction                   // 5
)

// WarningFromUint32 maps UPower's wire value; anything else is Unknown.
func WarningFromUint32(v uint32) WarningLevel {
	if v >= 1 && v <= 5 {
		return WarningLevel(v)
	}
	return WarningUnknown
}

// Low reports Low, Critical, or Action: the levels the dropdown flags
// (helpers.rs's is_low_battery).
func (w WarningLevel) Low() bool {
	return w == WarningLow || w == WarningCritical || w == WarningAction
}

// Device is one battery snapshot: the org.freedesktop.UPower.Device
// properties the bar and its dropdown read.
type Device struct {
	Percentage  float64
	State       DeviceState
	TimeToEmpty time.Duration // 0 when unknown
	TimeToFull  time.Duration // 0 when unknown
	// IsPresent is UPower's IsPresent: a battery is in its bay.
	IsPresent bool
	// EnergyRate is the charge or discharge rate in watts.
	EnergyRate float64
	// Energy and EnergyFull are watt-hours now and at full charge.
	Energy     float64
	EnergyFull float64
	// Capacity is the health percentage (full against design); 0 when
	// unknown.
	Capacity     float64
	WarningLevel WarningLevel
	// ChargeEndThreshold is the charge limit percentage.
	ChargeEndThreshold       uint32
	ChargeThresholdSupported bool
	ChargeThresholdEnabled   bool
}

// propReader stores the named property of a GetAll reply into dst. A
// property that is missing or of the wrong type leaves dst untouched:
// older UPower releases lack some, and a device type lacks others.
func propReader(props map[string]dbus.Variant) func(name string, dst any) {
	return func(name string, dst any) {
		if v, ok := props[name]; ok {
			_ = v.Store(dst)
		}
	}
}

// deviceFromProps decodes a GetAll reply. A property that is missing or
// of the wrong type keeps its zero value: older UPower releases lack
// the charge-threshold properties.
func deviceFromProps(props map[string]dbus.Variant) Device {
	var d Device
	var state, warning uint32
	var tte, ttf int64
	read := propReader(props)
	read("Percentage", &d.Percentage)
	read("State", &state)
	read("TimeToEmpty", &tte)
	read("TimeToFull", &ttf)
	read("IsPresent", &d.IsPresent)
	read("EnergyRate", &d.EnergyRate)
	read("Energy", &d.Energy)
	read("EnergyFull", &d.EnergyFull)
	read("Capacity", &d.Capacity)
	read("WarningLevel", &warning)
	read("ChargeEndThreshold", &d.ChargeEndThreshold)
	read("ChargeThresholdSupported", &d.ChargeThresholdSupported)
	read("ChargeThresholdEnabled", &d.ChargeThresholdEnabled)
	d.State = StateFromUint32(state)
	d.WarningLevel = WarningFromUint32(warning)
	d.TimeToEmpty = time.Duration(tte) * time.Second
	d.TimeToFull = time.Duration(ttf) * time.Second
	return d
}

// Rank orders the levels a battery alert follows: 0 for none, 1 for
// Low, 2 for Critical and Action.
func (w WarningLevel) Rank() uint8 {
	switch w {
	case WarningLow:
		return 1
	case WarningCritical, WarningAction:
		return 2
	}
	return 0
}

// DeviceKind mirrors UPower's device Type enum.
type DeviceKind uint8

// Device kinds, numbered as UPower's Type property.
const (
	KindUnknown DeviceKind = iota
	KindLinePower
	KindBattery
	KindUPS
	KindMonitor
	KindMouse
	KindKeyboard
	KindPDA
	KindPhone
	KindMediaPlayer
	KindTablet
	KindComputer
	KindGamingInput
	KindPen
	KindTouchpad
	KindModem
	KindNetwork
	KindHeadset
	KindSpeakers
	KindHeadphones
	KindVideo
	KindOtherAudio
	KindRemoteControl
	KindPrinter
	KindScanner
	KindCamera
	KindWearable
	KindToy
	KindBluetoothGeneric
	kindCount
)

// KindFromUint32 maps UPower's wire value; anything else is Unknown.
func KindFromUint32(v uint32) DeviceKind {
	if v < uint32(kindCount) {
		return DeviceKind(v)
	}
	return KindUnknown
}

// Peripheral is one device UPower reports beside the DisplayDevice
// aggregate: a wireless mouse, a keyboard, a controller, a headset, a
// UPS.
type Peripheral struct {
	Path         dbus.ObjectPath
	Kind         DeviceKind
	Model        string
	Percentage   float64
	State        DeviceState
	WarningLevel WarningLevel
}

// peripheralFromProps decodes one enumerated device's GetAll reply; ok
// is false for what is not a peripheral: line power, a battery powering
// the machine (the DisplayDevice aggregates those), and a device that
// is not present.
func peripheralFromProps(path dbus.ObjectPath, props map[string]dbus.Variant) (Peripheral, bool) {
	var kind, state, warning uint32
	var powerSupply, present bool
	p := Peripheral{Path: path}
	read := propReader(props)
	read("Type", &kind)
	read("PowerSupply", &powerSupply)
	read("IsPresent", &present)
	read("Model", &p.Model)
	read("Percentage", &p.Percentage)
	read("State", &state)
	read("WarningLevel", &warning)
	p.Kind = KindFromUint32(kind)
	p.State = StateFromUint32(state)
	p.WarningLevel = WarningFromUint32(warning)
	if !present || p.Kind == KindLinePower || p.Kind == KindBattery && powerSupply {
		return Peripheral{}, false
	}
	return p, true
}

// DisplayDevicePath is UPower's composite device across all batteries.
const DisplayDevicePath = "/org/freedesktop/UPower/devices/DisplayDevice"

const (
	managerPath = dbus.ObjectPath("/org/freedesktop/UPower")
	devicesPath = dbus.ObjectPath("/org/freedesktop/UPower/devices")
)

const (
	// BusName is UPower's well-known name on the system bus.
	BusName             = "org.freedesktop.UPower"
	deviceInterface     = "org.freedesktop.UPower.Device"
	propertiesInterface = "org.freedesktop.DBus.Properties"
	propertiesChanged   = propertiesInterface + ".PropertiesChanged"
)

// Source is the seam the bar module and dropdown consume; tests swap
// in a fake.
type Source interface {
	// Read returns the current device snapshot.
	Read(ctx context.Context) (Device, error)
	// Subscribe delivers a tick per PropertiesChanged on the device.
	// The returned stop function unsubscribes; the channel closes on
	// ctx completion either way.
	Subscribe(ctx context.Context) (<-chan struct{}, func(), error)
	// EnableChargeThreshold turns the charge limit on or off
	// (Device::enable_charge_threshold).
	EnableChargeThreshold(ctx context.Context, enabled bool) error
	// Peripherals returns the peripherals UPower enumerates, ordered by
	// object path.
	Peripherals(ctx context.Context) ([]Peripheral, error)
	// SubscribePeripherals delivers a tick per device added or removed
	// and per PropertiesChanged on any device but the DisplayDevice.
	SubscribePeripherals(ctx context.Context) (<-chan struct{}, func(), error)
}

// System reads the real UPower on the system bus.
type System struct {
	path dbus.ObjectPath
	conn *dbus.Conn
}

// NewOn targets the DisplayDevice over conn, which stays the caller's.
func NewOn(conn *dbus.Conn) *System {
	return &System{path: dbus.ObjectPath(DisplayDevicePath), conn: conn}
}

// Read fetches every DisplayDevice property in one GetAll.
func (s *System) Read(ctx context.Context) (Device, error) {
	props, err := s.getAll(ctx, s.path)
	if err != nil {
		return Device{}, err
	}
	return deviceFromProps(props), nil
}

func (s *System) getAll(ctx context.Context, path dbus.ObjectPath) (map[string]dbus.Variant, error) {
	var props map[string]dbus.Variant
	err := s.conn.Object(BusName, path).
		CallWithContext(ctx, propertiesInterface+".GetAll", 0, deviceInterface).Store(&props)
	if err != nil {
		return nil, fmt.Errorf("upower: GetAll %s: %w", path, err)
	}
	return props, nil
}

// EnableChargeThreshold calls the device's EnableChargeThreshold.
func (s *System) EnableChargeThreshold(ctx context.Context, enabled bool) error {
	call := s.conn.Object(BusName, s.path).CallWithContext(ctx, deviceInterface+".EnableChargeThreshold", 0, enabled)
	if call.Err != nil {
		return fmt.Errorf("upower: EnableChargeThreshold: %w", call.Err)
	}
	return nil
}

// Subscribe matches PropertiesChanged for the DisplayDevice, and UPower
// gaining an owner, and ticks the channel per signal. The matches are
// removed by stop.
func (s *System) Subscribe(ctx context.Context) (<-chan struct{}, func(), error) {
	return s.subscribe(ctx, propertiesOf(s.path), ownerAppeared)
}

// Peripherals enumerates UPower's devices and reads each one, keeping
// the peripherals.
func (s *System) Peripherals(ctx context.Context) ([]Peripheral, error) {
	var paths []dbus.ObjectPath
	if err := s.conn.Object(BusName, managerPath).CallWithContext(ctx, BusName+".EnumerateDevices", 0).Store(&paths); err != nil {
		return nil, fmt.Errorf("upower: EnumerateDevices: %w", err)
	}
	out := make([]Peripheral, 0, len(paths))
	for _, path := range paths {
		props, err := s.getAll(ctx, path)
		if err != nil {
			return nil, err
		}
		if p, ok := peripheralFromProps(path, props); ok {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Peripheral) int { return cmp.Compare(a.Path, b.Path) })
	return out, nil
}

// SubscribePeripherals matches DeviceAdded and DeviceRemoved on UPower
// and PropertiesChanged under its devices, and UPower gaining an owner.
// The matches are removed by stop.
func (s *System) SubscribePeripherals(ctx context.Context) (<-chan struct{}, func(), error) {
	return s.subscribe(ctx,
		signalFilter{
			match: []dbus.MatchOption{dbus.WithMatchObjectPath(managerPath), dbus.WithMatchInterface(BusName)},
			accept: func(sig *dbus.Signal) bool {
				return sig.Path == managerPath && (sig.Name == BusName+".DeviceAdded" || sig.Name == BusName+".DeviceRemoved")
			},
		},
		signalFilter{
			match: []dbus.MatchOption{dbus.WithMatchPathNamespace(devicesPath), dbus.WithMatchInterface(propertiesInterface)},
			accept: func(sig *dbus.Signal) bool {
				return sig.Path != DisplayDevicePath && sig.Name == propertiesChanged
			},
		},
		ownerAppeared)
}

// OnBattery reads UPower's OnBattery: the machine runs on its battery.
func (s *System) OnBattery(ctx context.Context) (bool, error) {
	var on bool
	err := s.conn.Object(BusName, managerPath).CallWithContext(ctx, propertiesInterface+".Get", 0, BusName, "OnBattery").Store(&on)
	if err != nil {
		return false, fmt.Errorf("upower: read OnBattery: %w", err)
	}
	return on, nil
}

// SubscribeOnBattery ticks per PropertiesChanged of UPower itself, which
// carries OnBattery. The match is removed by stop.
func (s *System) SubscribeOnBattery(ctx context.Context) (<-chan struct{}, func(), error) {
	return s.subscribe(ctx, propertiesOf(managerPath))
}

// signalFilter is one match rule on the bus and the check a delivered
// signal must pass, since the connection hands every subscriber every
// signal it receives.
type signalFilter struct {
	match  []dbus.MatchOption
	accept func(*dbus.Signal) bool
}

// ownerAppeared filters UPower starting or restarting, after which
// everything read before is stale.
var ownerAppeared = signalFilter{
	match: dbusx.OwnerMatch(BusName),
	accept: func(sig *dbus.Signal) bool {
		_, owner, ok := dbusx.NameOwnerChange(sig, BusName)
		return ok && owner != ""
	},
}

// propertiesOf filters PropertiesChanged on path.
func propertiesOf(path dbus.ObjectPath) signalFilter {
	return signalFilter{
		match: []dbus.MatchOption{dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(propertiesInterface)},
		accept: func(sig *dbus.Signal) bool {
			return sig.Path == path && sig.Name == propertiesChanged
		},
	}
}

func (s *System) subscribe(ctx context.Context, filters ...signalFilter) (<-chan struct{}, func(), error) {
	removeMatches := func(added []signalFilter) {
		for _, f := range added {
			_ = s.conn.RemoveMatchSignal(f.match...)
		}
	}
	for i, f := range filters {
		if err := s.conn.AddMatchSignal(f.match...); err != nil {
			removeMatches(filters[:i])
			return nil, nil, fmt.Errorf("upower: match signal: %w", err)
		}
	}
	accepted := func(sig *dbus.Signal) bool {
		return slices.ContainsFunc(filters, func(f signalFilter) bool { return f.accept(sig) })
	}
	ticks := make(chan struct{}, 1)
	signals := make(chan *dbus.Signal, 16)
	s.conn.Signal(signals)
	go func() {
		defer close(ticks)
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-signals:
				if !ok {
					return
				}
				if !accepted(sig) {
					continue
				}
				select {
				case ticks <- struct{}{}:
				default:
				}
			}
		}
	}()
	stop := func() {
		removeMatches(filters)
		s.conn.RemoveSignal(signals)
	}
	return ticks, stop, nil
}
