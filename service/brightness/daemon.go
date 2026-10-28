package brightness

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// ServiceName and ServicePath are dev.stubbe.lys.Brightness1, which
// `lys-shell brightness` drives.
const (
	ServiceName = "dev.stubbe.lys.Brightness1"
	ServicePath = dbus.ObjectPath("/dev/stubbe/lys/Brightness")
)

const daemonTimeout = 5 * time.Second

// SetOutput moves every device driving output by change, held at
// floor, and returns their mean new level.
func SetOutput(ctx context.Context, src Source, output string, change Change, floor float64) (float64, error) {
	devices, err := src.Devices(ctx)
	if err != nil {
		return 0, fmt.Errorf("brightness: read devices: %w", err)
	}
	targets, err := ForOutput(devices, output)
	if err != nil {
		return 0, err
	}
	var sum float64
	for _, d := range targets {
		level := change.Target(d, floor)
		if err := src.Set(ctx, d.Name, level); err != nil {
			return 0, fmt.Errorf("brightness: set %s on %s: %w", d.Name, output, err)
		}
		sum += level
	}
	return sum / float64(len(targets)), nil
}

// Daemon is the dev.stubbe.lys.Brightness1 object: it changes the
// shell's own source, so the bar and the OSD follow every change.
type Daemon struct {
	src     Source
	focused func() (string, bool)
	floor   func() float64
	mu      sync.Mutex
}

// NewDaemon serves src; focused names the focused output and floor is
// the lowest level a change may leave.
func NewDaemon(src Source, focused func() (string, bool), floor func() float64) *Daemon {
	return &Daemon{src: src, focused: focused, floor: floor}
}

// Apply changes output, or the focused output when it is empty, and
// returns the output and its new level.
func (d *Daemon) Apply(output string, relative bool, percent int32) (string, float64, *dbus.Error) {
	change, err := NewChange(relative, percent)
	if err != nil {
		return "", 0, dbusx.InvalidArgs(err.Error())
	}
	if output == "" {
		name, ok := d.focused()
		if !ok {
			return "", 0, dbusx.Failed("the compositor reports no focused output; name one with --output")
		}
		output = name
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), daemonTimeout)
	defer cancel()
	level, err := SetOutput(ctx, d.src, output, change, d.floor())
	if err != nil {
		return "", 0, dbusx.Failed(err.Error())
	}
	return output, level, nil
}

// Export serves the daemon on conn under ServiceName.
func (d *Daemon) Export(conn *dbus.Conn) (func(), error) {
	return dbusx.Serve(conn, dbusx.Service{
		Name:      ServiceName,
		Path:      ServicePath,
		Interface: ServiceName,
		Methods:   d,
	})
}
