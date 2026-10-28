package brightness

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/intconv"
)

// Change is a validated brightness request in whole percent: an
// absolute level in 0..=100 or a signed step from the current level.
type Change struct {
	relative bool
	percent  int32
}

// Relative is a step of delta percent from the current level.
func Relative(delta int32) Change { return Change{relative: true, percent: delta} }

// Absolute is a level of percent, which must be in 0..=100.
func Absolute(percent int32) (Change, error) {
	if percent < 0 || percent > 100 {
		return Change{}, fmt.Errorf("%d is not in 0..=100", percent)
	}
	return Change{percent: percent}, nil
}

// NewChange is Relative or Absolute by relative.
func NewChange(relative bool, percent int32) (Change, error) {
	if relative {
		return Relative(percent), nil
	}
	return Absolute(percent)
}

var errChangeSyntax = errors.New("expected N to set, +N to raise or -N to lower, with N a whole percent")

// ParseChange reads "N", "+N" or "-N" with N in ASCII digits.
func ParseChange(raw string) (Change, error) {
	sign, digits := byte(0), raw
	if raw != "" && (raw[0] == '+' || raw[0] == '-') {
		sign, digits = raw[0], raw[1:]
	}
	if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return Change{}, errChangeSyntax
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	var percent int32
	if err == nil {
		percent, err = intconv.Exact[int32](n)
	}
	if err != nil {
		return Change{}, fmt.Errorf("%s is too large", digits)
	}
	switch sign {
	case '+':
		return Relative(percent), nil
	case '-':
		return Relative(-percent), nil
	}
	return Absolute(percent)
}

// IsRelative reports whether the change is a step.
func (c Change) IsRelative() bool { return c.relative }

// Percent is the level or the signed step.
func (c Change) Percent() int32 { return c.percent }

// Target is the percentage d moves to: the level, or the step from d's
// rounded level, held between floor and 100. A positive floor never
// rounds to a dark panel: it is at least one raw step of d.
func (c Change) Target(d Device, floor float64) float64 {
	target := float64(c.percent)
	if c.relative {
		target += math.Round(d.Percentage())
	}
	return min(max(target, deviceFloor(d, floor)), 100)
}

func deviceFloor(d Device, floor float64) float64 {
	if floor <= 0 || d.Max == 0 {
		return max(floor, 0)
	}
	return max(floor, 100/float64(d.Max))
}
