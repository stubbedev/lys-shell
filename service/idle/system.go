package idle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/stubbedev/lys-desktop/service/idle/idlepolicy"
	"github.com/stubbedev/lys-lib/logind"

	"github.com/stubbedev/lys-shell/service/brightness"
)

// Backlights are the brightness devices the dim stage lowers.
type Backlights interface {
	Devices(ctx context.Context) ([]brightness.Device, error)
	Set(ctx context.Context, name string, percent float64) error
}

// backlightTimeout bounds one dim or restore of every backlight.
const backlightTimeout = 5 * time.Second

// Login is logind as the stages use it: its manager and the user's
// session, on one system bus connection.
type Login struct {
	Manager logind.Manager
	Session logind.Session
}

// Outputs turn the outputs off; the returned restore turns them on.
type Outputs interface {
	PowerOff() (restore func() error, err error)
}

// System carries the stages out on the session: the backlights dim,
// logind locks and suspends, and the compositor powers the outputs off.
// A nil Backlights has nothing to dim; a nil Login cannot lock or
// suspend, which the stage reports.
type System struct {
	Backlights Backlights
	Login      *Login
	Outputs    Outputs
}

// ErrNoLogind is a lock or suspend without logind on the system bus.
var ErrNoLogind = errors.New("idle: logind is not connected")

// Dim lowers every backlight to share of its level; the restore puts back
// each level exactly as it was.
func (s System) Dim(share idlepolicy.DimPercent) (func() error, error) {
	if s.Backlights == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), backlightTimeout)
	defer cancel()
	devices, err := s.Backlights.Devices(ctx)
	if err != nil {
		return nil, fmt.Errorf("idle: read the backlights: %w", err)
	}
	for _, d := range devices {
		if e := s.Backlights.Set(ctx, d.Name, share.Of(d.Percentage())); e != nil {
			err = errors.Join(err, fmt.Errorf("idle: dim %s: %w", d.Name, e))
		}
	}
	return func() error { return s.restoreLevels(devices) }, err
}

func (s System) restoreLevels(devices []brightness.Device) error {
	ctx, cancel := context.WithTimeout(context.Background(), backlightTimeout)
	defer cancel()
	var err error
	for _, d := range devices {
		if e := s.Backlights.Set(ctx, d.Name, d.Percentage()); e != nil {
			err = errors.Join(err, fmt.Errorf("idle: restore %s: %w", d.Name, e))
		}
	}
	return err
}

// Lock asks logind to lock the session.
func (s System) Lock() error {
	if s.Login == nil {
		return ErrNoLogind
	}
	return s.Login.Session.Lock(context.Background())
}

// ScreenOff powers every output off.
func (s System) ScreenOff() (func() error, error) {
	if s.Outputs == nil {
		return nil, ErrNoOutputPower
	}
	return s.Outputs.PowerOff()
}

// Suspend asks logind to suspend.
func (s System) Suspend() error {
	if s.Login == nil {
		return ErrNoLogind
	}
	return s.Login.Manager.Power(context.Background(), logind.Suspend)
}
