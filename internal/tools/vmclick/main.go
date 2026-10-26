// Command vmclick clicks the left button at a point of the output
// layout through the compositor's virtual pointer, for the VM checks:
// QEMU's own pointer does not reach a Wayland seat, a
// zwlr_virtual_pointer does. The compositor maps absolute motion over
// the whole layout, so the caller names its extent.
//
//	vmclick X Y WIDTH HEIGHT
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/stubbedev/lys-lib/vinput"
)

// btnLeft is linux/input-event-codes.h's BTN_LEFT.
const btnLeft = 0x110

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vmclick:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	p, err := parse(args)
	if err != nil {
		return err
	}
	dev, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		return fmt.Errorf("connect the virtual pointer: %w", err)
	}
	defer dev.Close()
	for _, step := range []func() error{
		func() error { return dev.MotionAbsolute(p.x, p.y, p.w, p.h) },
		dev.Roundtrip,
		func() error { return dev.Button(btnLeft, true) },
		dev.Roundtrip,
		func() error { return dev.Button(btnLeft, false) },
		dev.Roundtrip,
	} {
		if err := step(); err != nil {
			return fmt.Errorf("click at %d,%d: %w", p.x, p.y, err)
		}
	}
	return nil
}

// point is a click target in a layout extent, in pixels.
type point struct{ x, y, w, h uint32 }

// parse reads the point and the layout extent; the point must lie
// inside the extent.
func parse(args []string) (point, error) {
	if len(args) != 4 {
		return point{}, errors.New("usage: vmclick X Y WIDTH HEIGHT")
	}
	var v [4]uint32
	for i, a := range args {
		n, err := strconv.ParseUint(a, 10, 32)
		if err != nil {
			return point{}, fmt.Errorf("argument %d %q: %w", i+1, a, err)
		}
		v[i] = uint32(n)
	}
	p := point{v[0], v[1], v[2], v[3]}
	if p.x >= p.w || p.y >= p.h {
		return point{}, fmt.Errorf("%d,%d lies outside the %dx%d layout", p.x, p.y, p.w, p.h)
	}
	return p, nil
}
