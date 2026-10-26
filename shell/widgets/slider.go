// Package widgets is wayle-widgets: the controls the shell surfaces and
// the settings app share.
package widgets

import (
	"strconv"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"
)

// DebouncedSlider's cadences (wayle-widgets slider/debounced).
const (
	sliderThrottle = 100 * time.Millisecond
	sliderGrace    = 150 * time.Millisecond
)

// DebouncedSlider is wayle-widgets' DebouncedSlider: a slider with an optional value
// label that commits (backend writes) at most every 100ms while
// dragged, with a trailing commit of the last value and one on release,
// and ignores external values while held and for 150ms after.
type DebouncedSlider struct {
	*widget.Box
	// Knob is the slider itself (class it, read it).
	Knob  *pressSlider
	label *widget.Label
	// Format renders the value label (default "{:.0}%").
	Format func(float64) string
	invoke func(func())

	// OnCommit hears the throttled user commits on the loop.
	OnCommit func(float64)
	// Write turns a committed value, on the loop, into the backend write
	// that applies it (nil writes nothing), run off the loop one at a
	// time, the latest commit's replacing any queued while one runs;
	// outside values are ignored until the last write has landed and
	// the release grace has passed after it. A slow backend (a display
	// over DDC/CI) otherwise lands concurrent writes out of order and
	// echoes stale values onto the slider.
	Write func(float64) func()
	// OnValue sees every value change, the user's and set's alike
	// (the value-changed notify a #[watch] reads).
	OnValue      func(float64)
	programmatic bool
	dragging     bool
	dragEnded    time.Time
	lastCommit   time.Time
	trailing     *time.Timer
	now          func() time.Time

	// wmu guards the write queue: want is the latest commit's write,
	// queued whether it awaits running, writing whether a drain runs.
	// landed is when the last drain finished (loop only).
	wmu     sync.Mutex
	want    func()
	queued  bool
	writing bool
	landed  time.Time
}

// pressSlider routes the press protocol to its owner.
type pressSlider struct {
	*widget.Slider
	owner *DebouncedSlider
}

// HitTest makes the wrapper the input leaf, so SetPressed reaches it.
func (s *pressSlider) HitTest(p widget.Point) widget.Widget { return s.HitLeaf(s, p) }

// CursorName is the hover pointer: the hand, like the wrapped slider
// asks (the wrapper is the input leaf, so its own answer wins).
func (s *pressSlider) CursorName() string { return "pointer" }

// SetPressed tracks the drag; the release commits the final value.
func (s *pressSlider) SetPressed(on bool) {
	was := s.Pressed
	s.Slider.SetPressed(on)
	d := s.owner
	switch {
	case on && !was:
		d.dragging, d.dragEnded = true, time.Time{}
	case !on && was:
		d.dragging, d.dragEnded = false, d.now()
		d.cancelTrailing()
		d.commit(s.Value())
	}
}

// NewDebouncedSlider builds a 0-100 slider (every lys-shell slider is a
// percentage); with a font the
// value label shows (format, default "{:.0}%"). invoke runs the
// trailing commit on the loop.
func NewDebouncedSlider(value float64, font render.Font, px float64, color render.Color, invoke func(func())) *DebouncedSlider {
	return NewRangedSlider(0, 100, value, font, px, color, invoke)
}

// NewRangedSlider is NewDebouncedSlider over [min, max].
func NewRangedSlider(min, max, value float64, font render.Font, px float64, color render.Color, invoke func(func())) *DebouncedSlider {
	d := &DebouncedSlider{Box: widget.NewBox(widget.Row, 8, 0), invoke: invoke, now: time.Now}
	d.Format = func(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) + "%" }
	d.Knob = &pressSlider{Slider: widget.NewSlider(min, max, 0, value), owner: d}
	d.Knob.OnChanged = d.changed
	d.Append(d.Knob, true)
	if font != nil {
		d.label = widget.NewLabel(font, px, d.Format(value), color)
		d.label.SetAlignment(render.AlignEnd)
		// The label floors at its widest rendering over the range, so
		// the sliders of a section end at the same x whatever their
		// values (GTK width-chars).
		widest := len(d.Format(min))
		if n := len(d.Format(max)); n > widest {
			widest = n
		}
		d.label.SetWidthChars(widest)
		d.Append(d.label, false)
	}
	return d
}

// ValueLabel hands back the formatted value label, for the caller to
// class the way its stylesheet names it (audio-slider-value,
// brightness-slider-value, ...).
func (d *DebouncedSlider) ValueLabel() *widget.Label { return d.label }

// changed is the value_changed handler: user moves update the label
// and commit through the throttle; programmatic ones do not commit.
func (d *DebouncedSlider) changed(v float64) {
	if d.label != nil {
		d.label.SetText(d.Format(v))
	}
	if d.OnValue != nil {
		d.OnValue(v)
	}
	if d.programmatic {
		return
	}
	if d.lastCommit.IsZero() || d.now().Sub(d.lastCommit) >= sliderThrottle {
		d.cancelTrailing()
		d.commit(v)
		return
	}
	d.cancelTrailing()
	d.trailing = time.AfterFunc(sliderThrottle, func() {
		d.invoke(func() {
			d.trailing = nil
			d.commit(v)
		})
	})
}

func (d *DebouncedSlider) commit(v float64) {
	d.lastCommit = d.now()
	if d.OnCommit != nil {
		d.OnCommit(v)
	}
	if d.Write != nil {
		if write := d.Write(v); write != nil {
			d.enqueue(write)
		}
	}
}

// enqueue queues write, starting a drain when none runs.
func (d *DebouncedSlider) enqueue(write func()) {
	d.wmu.Lock()
	d.want, d.queued = write, true
	start := !d.writing
	d.writing = true
	d.wmu.Unlock()
	if start {
		go d.drain()
	}
}

// drain runs the latest queued write until none is left, then marks
// the writes landed on the loop.
func (d *DebouncedSlider) drain() {
	for {
		d.wmu.Lock()
		if !d.queued {
			d.writing = false
			d.wmu.Unlock()
			d.invoke(func() { d.landed = d.now() })
			return
		}
		write := d.want
		d.want, d.queued = nil, false
		d.wmu.Unlock()
		write()
	}
}

// writePending reports whether a write runs or awaits.
func (d *DebouncedSlider) writePending() bool {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	return d.writing || d.queued
}

func (d *DebouncedSlider) cancelTrailing() {
	if d.trailing != nil {
		d.trailing.Stop()
		d.trailing = nil
	}
}

// interacting is user_is_interacting: held, writing, or released (or
// its last write landed) under 150ms ago.
func (d *DebouncedSlider) interacting() bool {
	if d.dragging || d.writePending() {
		return true
	}
	since := d.dragEnded
	if d.landed.After(since) {
		since = d.landed
	}
	return !since.IsZero() && d.now().Sub(since) < sliderGrace
}

// Set is set_value_external: an outside value, ignored while the user
// interacts.
func (d *DebouncedSlider) Set(v float64) {
	if d.interacting() {
		return
	}
	d.programmatic = true
	d.Knob.SetValue(v)
	d.programmatic = false
}

// Value is the slider's current value.
func (d *DebouncedSlider) Value() float64 { return d.Knob.Value() }

// SetFormat replaces the value label's format and re-renders it.
func (d *DebouncedSlider) SetFormat(format func(float64) string) {
	d.Format = format
	if d.label != nil {
		d.label.SetText(format(d.Value()))
	}
}

// Label is the value label, nil without a font.
func (d *DebouncedSlider) Label() *widget.Label { return d.label }

// SetClock replaces the clock the throttle and the release grace read
// (a test seam).
func (d *DebouncedSlider) SetClock(now func() time.Time) { d.now = now }
