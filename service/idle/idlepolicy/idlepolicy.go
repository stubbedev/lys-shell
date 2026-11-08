package idlepolicy

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Stage is one step of the policy, in the order a session usually reaches
// them.
type Stage uint8

// The stages.
const (
	Dim Stage = iota
	Lock
	ScreenOff
	Suspend
	stageCount
)

// Stages lists every stage in order.
var Stages = [stageCount]Stage{Dim, Lock, ScreenOff, Suspend}

var stageNames = [stageCount]string{"dim", "lock", "screen-off", "suspend"}

func (s Stage) String() string { return stageNames[s] }

// PowerSource is what the machine runs on: each has its own timeouts.
type PowerSource uint8

// The power sources.
const (
	AC PowerSource = iota
	Battery
)

func (p PowerSource) String() string {
	if p == Battery {
		return "battery"
	}
	return "ac"
}

// The bounds of a timeout: ext-idle-notify takes milliseconds in a uint32,
// and below a second a stage would fire between two keystrokes.
const (
	MinTimeout = time.Second
	MaxTimeout = time.Duration(math.MaxUint32) * time.Millisecond
)

// ErrTimeoutRange is a timeout outside MinTimeout-MaxTimeout.
var ErrTimeoutRange = errors.New("idle timeout out of range")

// Timeout is how long the session idles before a stage runs; the zero
// value is never. NewTimeout is the only way to another.
type Timeout struct{ after time.Duration }

// Never is the timeout of a stage that does not run.
var Never = Timeout{}

// NewTimeout validates d, refusing one outside MinTimeout-MaxTimeout with
// ErrTimeoutRange.
func NewTimeout(d time.Duration) (Timeout, error) {
	if d < MinTimeout || d > MaxTimeout {
		return Timeout{}, fmt.Errorf("%w: %s, valid %s-%s", ErrTimeoutRange, d, MinTimeout, MaxTimeout)
	}
	return Timeout{after: d}, nil
}

// After is the idle time before the stage runs; false for never.
func (t Timeout) After() (time.Duration, bool) { return t.after, t.after != 0 }

// Timeouts are a stage's timeouts on each power source.
type Timeouts struct{ AC, Battery Timeout }

// On is the timeout on source.
func (t Timeouts) On(source PowerSource) Timeout {
	if source == Battery {
		return t.Battery
	}
	return t.AC
}

// DimPercent is the share of its level a dimmed backlight keeps, 1-100.
type DimPercent struct{ percent uint8 }

// ErrDimPercentRange is a dim percentage outside 1-100.
var ErrDimPercentRange = errors.New("idle dim percentage out of range")

// NewDimPercent validates percent, refusing one outside 1-100 with
// ErrDimPercentRange: at 0 a dimmed panel is a dark one, which is the
// screen-off stage's job.
func NewDimPercent(percent int64) (DimPercent, error) {
	if percent < 1 || percent > 100 {
		return DimPercent{}, fmt.Errorf("%w: %d, valid 1-100", ErrDimPercentRange, percent)
	}
	return DimPercent{percent: uint8(percent)}, nil
}

// Percent is the share in percent.
func (d DimPercent) Percent() uint8 { return d.percent }

// Of is level dimmed to this share.
func (d DimPercent) Of(level float64) float64 { return level * float64(d.percent) / 100 }

// Policy is the whole configuration of the idle service.
type Policy struct {
	Timeouts        [stageCount]Timeouts
	DimPercent      DimPercent
	LockBeforeSleep bool
}

// Arm is one stage armed on a power source: it runs after After of idle.
type Arm struct {
	Stage Stage
	After time.Duration
}

// Arms are the stages that run on source, in stage order.
func (p Policy) Arms(source PowerSource) []Arm {
	arms := make([]Arm, 0, stageCount)
	for _, stage := range Stages {
		if after, ok := p.Timeouts[stage].On(source).After(); ok {
			arms = append(arms, Arm{Stage: stage, After: after})
		}
	}
	return arms
}

// FormatDuration spells d the way the config writes it: 4m, 1h, 1h30m,
// 1m30s, without the zero tails time.Duration's String keeps.
func FormatDuration(d time.Duration) string {
	s := d.String()
	if trimmed, ok := strings.CutSuffix(s, "m0s"); ok {
		s = trimmed + "m"
	}
	if trimmed, ok := strings.CutSuffix(s, "h0m"); ok {
		s = trimmed + "h"
	}
	return s
}
