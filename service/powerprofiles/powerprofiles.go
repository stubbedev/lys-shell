// Package powerprofiles is the power-profiles-daemon client: the
// active profile, the available set, property-change ticks, and the
// cycle action.
package powerprofiles

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal"
)

// Profile names as the daemon spells them.
const (
	ProfilePowerSaver  = "power-saver"
	ProfileBalanced    = "balanced"
	ProfilePerformance = "performance"
)

// Canonical cycle order (helpers.rs): power-saver → balanced →
// performance.
var order = []string{ProfilePowerSaver, ProfileBalanced, ProfilePerformance}

// NextProfile is helpers.rs's next_profile: the successor of current
// in the canonical order restricted to available (empty available
// means all three), falling back to balanced.
func NextProfile(current string, available []string) string {
	cycle := order
	if len(available) > 0 {
		cycle = make([]string, 0, len(available))
		for _, candidate := range order {
			if slices.Contains(available, candidate) {
				cycle = append(cycle, candidate)
			}
		}
	}
	if len(cycle) == 0 {
		return ProfileBalanced
	}
	for i, candidate := range cycle {
		if candidate == current {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// Snapshot is one daemon state.
type Snapshot struct {
	Available bool
	Active    string
	Profiles  []string
	// PerformanceDegraded is the daemon's degradation reason ("" when
	// performance is not degraded).
	PerformanceDegraded string
}

const (
	// BusName is power-profiles-daemon's well-known name on the system bus.
	BusName     = "org.freedesktop.UPower.PowerProfiles"
	daemonPath  = "/org/freedesktop/UPower/PowerProfiles"
	daemonIface = "org.freedesktop.UPower.PowerProfiles"
	properties  = "org.freedesktop.DBus.Properties"
)

// Source is the module's seam.
type Source interface {
	// Read collects the snapshot.
	Read(ctx context.Context) (Snapshot, error)
	// SetActive switches the profile.
	SetActive(ctx context.Context, profile string) error
	// Subscribe ticks on daemon property changes. The channel closes
	// when ctx completes; stop terminates the listener and reports what
	// releasing its match met.
	Subscribe(ctx context.Context) (<-chan struct{}, func() error, error)
}

// System reads the real daemon over the system bus.
type System struct {
	mu   sync.Mutex
	conn *dbus.Conn
}

// NewOn reads the daemon over conn, which stays the caller's.
func NewOn(conn *dbus.Conn) *System { return &System{conn: conn} }

// Read collects the snapshot; an absent daemon reads as unavailable
// with the canonical cycle available.
func (s *System) Read(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snap := Snapshot{Active: ProfileBalanced}
	obj := s.conn.Object(BusName, daemonPath)
	var active string
	if err := obj.CallWithContext(ctx, properties+".Get", 0, daemonIface, "ActiveProfile").Store(&active); err != nil {
		return snap, nil
	}
	snap.Available = true
	snap.Active = active

	var degraded string
	if err := obj.CallWithContext(ctx, properties+".Get", 0, daemonIface, "PerformanceDegraded").Store(&degraded); err == nil {
		snap.PerformanceDegraded = degraded
	}
	var raw []map[string]dbus.Variant
	if err := obj.CallWithContext(ctx, properties+".Get", 0, daemonIface, "Profiles").Store(&raw); err == nil {
		for _, entry := range raw {
			if name, ok := entry["Profile"].Value().(string); ok {
				snap.Profiles = append(snap.Profiles, name)
			}
		}
	}
	return snap, nil
}

// SetActive writes the ActiveProfile property.
func (s *System) SetActive(ctx context.Context, profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj := s.conn.Object(BusName, daemonPath)
	return obj.CallWithContext(ctx, properties+".Set", 0, daemonIface, "ActiveProfile",
		dbus.MakeVariant(profile)).Err
}

// Subscribe ticks on the daemon's PropertiesChanged.
func (s *System) Subscribe(ctx context.Context) (<-chan struct{}, func() error, error) {
	ticks, stop, err := bussignal.Ticks(ctx, s.conn, dbusx.FromOwner(BusName), "powerprofiles", daemonRoutes)
	if err != nil {
		return nil, nil, fmt.Errorf("powerprofiles: %w", err)
	}
	return ticks, stop, nil
}

// daemonRoutes call changed for a change of the daemon's properties.
func daemonRoutes(changed func()) []dbusx.Route {
	return []dbusx.Route{
		dbusx.On(dbusx.PropertiesChanged, daemonPath, func(dbusx.ChangedProperties, dbus.Sequence) error {
			changed()
			return nil
		}).WithArg0(daemonIface),
	}
}
