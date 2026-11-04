package bar

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/render"
	"github.com/stubbedev/lys-ui/widget"

	analyzer "github.com/stubbedev/lys-shell/cava"
	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/styling"
)

// spectrumHeightRem is the visualizer's natural thickness (the icon-lg
// token, $base-icon-lg).
const spectrumHeightRem = 1.6

// cavaRate is the fixed capture rate the plan is built for
// (service.rs's DEFAULT_SAMPLERATE).
const cavaRate = 44100

// Renderer constants (rendering/*.rs).
const (
	cavaMinBarHeight  = 2.0   // MIN_BAR_HEIGHT
	cavaMinWaveHeight = 2.0   // MIN_WAVE_HEIGHT
	cavaPeakCap       = 2.0   // PEAK_CAP_HEIGHT
	cavaPeakGravity   = 0.015 // PEAK_GRAVITY, per frame
	// cavaStill is the change in a bar, as a fraction of its height,
	// too small to reach a pixel on any bar.
	cavaStill = 1.0 / 1024
)

// spectrum paints the analyzer frame the way the Rust module's cairo
// draw func does: bars, a filled wave, or bars with falling peak caps,
// laid along the bar (rotated a quarter turn on a vertical bar) with
// the internal padding at both ends.
type spectrum struct {
	widget.Base
	spectrumFrame
	dir      config.CavaDirection
	color    render.Color
	width    float64
	gap      float64
	pad      float64
	length   int
	thick    int
	vertical bool
}

// newSpectrum builds the painter for one resolved cava config with
// bars values a frame.
func newSpectrum(cfg config.CavaConfig, bars int, color render.Color, scale float64, vertical bool) *spectrum {
	pad := cfg.InternalPadding.ResolvePx(styling.RemBase, scale)
	return &spectrum{
		spectrumFrame: newSpectrumFrame(bars, cfg.Style),
		dir:           cfg.Direction,
		color:         color,
		width:         float64(cfg.BarWidth),
		gap:           float64(cfg.BarGap),
		pad:           pad,
		length:        cavaWidgetLength(bars, int(cfg.BarWidth), int(cfg.BarGap), pad),
		thick:         int(math.Round(spectrumHeightRem * styling.RemBase * scale)),
		vertical:      vertical,
	}
}

// cavaWidgetLength is calculate_widget_length: every bar and gap plus
// the padding at both ends, at least one pixel.
func cavaWidgetLength(bars, barWidth, barGap int, padding float64) int {
	total := float64(bars*barWidth) + float64(max(bars-1, 0)*barGap) + float64(2*padding)
	return max(int(math.Round(total)), 1)
}

// spectrumFrame is what a frame leaves on screen: the bars and their caps.
type spectrumFrame struct {
	values []float64
	peaks  []float64
	style  config.CavaStyle
}

func newSpectrumFrame(bars int, style config.CavaStyle) spectrumFrame {
	return spectrumFrame{values: make([]float64, bars), peaks: make([]float64, bars), style: style}
}

// advance swaps in the latest values and, in the peaks style, lets each cap
// rise to its bar or fall by the gravity (update_peak). It reports whether the
// frame moved what is on screen; one that did not keeps the painted values, so
// a slow drift still repaints once it adds up.
func (f *spectrumFrame) advance(values []float64) bool {
	moved := false
	for i, v := range values {
		if math.Abs(v-f.values[i]) >= cavaStill {
			moved = true
			break
		}
	}
	if moved {
		copy(f.values, values)
	}
	if f.style == config.CavaStylePeaks {
		for i, v := range f.values {
			if v >= f.peaks[i] {
				f.peaks[i] = v
				continue
			}
			f.peaks[i] = max(f.peaks[i]-cavaPeakGravity, 0)
			moved = true
		}
	}
	return moved
}

// SetFrame advances to the frame and asks for a paint when it moved.
func (s *spectrum) SetFrame(values []float64) bool {
	moved := s.advance(values)
	if moved {
		s.Invalidate()
	}
	return moved
}

// size is the natural size: the length along the bar, the thickness
// across it.
func (s *spectrum) size() widget.Size {
	if s.vertical {
		return widget.Size{W: s.thick, H: s.length}
	}
	return widget.Size{W: s.length, H: s.thick}
}

// Measure reports the fixed natural size as its minimum too: a
// visualizer squeezed shorter would misrepresent the bar layout.
func (s *spectrum) Measure(axis widget.Axis, _ int) widget.Sizes {
	return widget.FixedSizes(s.size().Along(axis))
}

// Arrange records the rect.
func (s *spectrum) Arrange(r render.Rect) { s.Base.Arrange(r) }

// canvas is the draw func's coordinate space: u along the bar from the
// padding's start, v across it from the top; a vertical bar turns it a
// quarter (translate to the bottom, rotate -90°), so u runs upward and
// v rightward.
type spectrumCanvas struct {
	r        render.Rect
	vertical bool
	w, h     float64 // the canvas extent along u and v
}

func (s *spectrum) canvas() spectrumCanvas {
	r := s.Bounds()
	c := spectrumCanvas{r: r, vertical: s.vertical, w: float64(r.W), h: float64(r.H)}
	if s.vertical {
		c.w, c.h = float64(r.H), float64(r.W)
	}
	return c
}

// point maps a canvas point to the widget's logical pixels.
func (c spectrumCanvas) point(u, v float64) (float64, float64) {
	if c.vertical {
		return float64(c.r.X) + v, float64(c.r.Y) + c.w - u
	}
	return float64(c.r.X) + u, float64(c.r.Y) + v
}

// fillRect fills a canvas rect as a path, so fractional bar geometry
// lands as cairo would put it.
func (c spectrumCanvas) fillRect(cv *render.Canvas, u, v, w, h float64, col render.Color) {
	var p render.Path
	p.MoveTo(c.point(u, v))
	p.LineTo(c.point(u+w, v))
	p.LineTo(c.point(u+w, v+h))
	p.LineTo(c.point(u, v+h))
	p.Close()
	cv.FillPath(&p, col)
}

// barOrigin is bar_origin_y.
func barOrigin(dir config.CavaDirection, barHeight, canvasHeight float64) float64 {
	switch dir {
	case config.CavaDirectionReverse:
		return 0
	case config.CavaDirectionMirror:
		return (canvasHeight - barHeight) / 2
	}
	return canvasHeight - barHeight
}

// Paint is the draw func: nothing before the first frame, then the
// configured style.
func (s *spectrum) Paint(cv *render.Canvas) {
	if len(s.values) == 0 {
		return
	}
	c := s.canvas()
	switch s.style {
	case config.CavaStyleWave:
		s.paintWave(cv, c)
	default:
		s.paintBars(cv, c)
	}
}

// paintBars is draw_bars, and draw_peak_bars's caps in the peaks style.
func (s *spectrum) paintBars(cv *render.Canvas, c spectrumCanvas) {
	stride := s.width + s.gap
	for i, amp := range s.values {
		u := s.pad + float64(float64(i)*stride)
		h := math.Min(math.Max(amp*c.h, cavaMinBarHeight), c.h)
		c.fillRect(cv, u, barOrigin(s.dir, h, c.h), s.width, h, s.color)
		if s.style != config.CavaStylePeaks {
			continue
		}
		peak := float64(s.peaks[i] * c.h)
		if peak <= h {
			continue
		}
		capH := math.Min(cavaPeakCap, c.h)
		switch s.dir {
		case config.CavaDirectionReverse:
			c.fillRect(cv, u, peak, s.width, capH, s.color)
		case config.CavaDirectionMirror:
			center := float64(c.h / 2)
			c.fillRect(cv, u, center-float64(peak/2)-capH, s.width, capH, s.color)
			c.fillRect(cv, u, center+float64(peak/2), s.width, capH, s.color)
		default:
			c.fillRect(cv, u, c.h-peak-capH, s.width, capH, s.color)
		}
	}
}

// paintWave is draw_wave: a curve through each value, the control
// points halfway between neighbors, closed along the base (or, mirrored,
// back along its reflection).
func (s *spectrum) paintWave(cv *render.Canvas, c spectrumCanvas) {
	n := len(s.values)
	width := math.Max(c.w-float64(2*s.pad), 0)
	spacing := width
	if n > 1 {
		spacing = width / float64(n-1)
	}
	minAmp := cavaMinWaveHeight / c.h
	y := func(amp float64) float64 {
		amp = math.Max(amp, minAmp)
		switch s.dir {
		case config.CavaDirectionReverse:
			return float64(c.h * amp)
		case config.CavaDirectionMirror:
			return float64(c.h * (1 - amp) / 2)
		}
		return float64(c.h * (1 - amp))
	}
	var p render.Path
	at := func(u, v float64) (float64, float64) { return c.point(s.pad+u, v) }
	p.MoveTo(at(0, y(s.values[0])))
	curve := func(u0, v0, u1, v1 float64) {
		mid := float64((u0 + u1) / 2)
		x1, y1 := at(mid, v0)
		x2, y2 := at(mid, v1)
		x3, y3 := at(u1, v1)
		p.CubeTo(x1, y1, x2, y2, x3, y3)
	}
	for i := 1; i < n; i++ {
		curve(float64(float64(i-1)*spacing), y(s.values[i-1]), float64(float64(i)*spacing), y(s.values[i]))
	}
	switch s.dir {
	case config.CavaDirectionReverse:
		p.LineTo(at(width, 0))
		p.LineTo(at(0, 0))
	case config.CavaDirectionMirror:
		center := float64(c.h / 2)
		mirror := func(i int) float64 { return center + float64(math.Max(s.values[i], minAmp)*c.h/2) }
		p.LineTo(at(float64(float64(n-1)*spacing), mirror(n-1)))
		for i := n - 2; i >= 0; i-- {
			curve(float64(float64(i+1)*spacing), mirror(i+1), float64(float64(i)*spacing), mirror(i))
		}
	default:
		p.LineTo(at(width, c.h))
		p.LineTo(at(0, c.h))
	}
	p.Close()
	cv.FillPath(&p, s.color)
}

// HitTest misses: the visualizer takes no input.
func (s *spectrum) HitTest(widget.Point) widget.Widget { return nil }

// cavaModule is one bar's spectrum, fed by the analyzer its config shares.
type cavaModule struct {
	paint    *spectrum
	analyzer *cavaAnalyzer
}

// cavaAnalyzer is the plan and its capture, run on the frame timer's goroutine
// at the configured framerate while there is sound or the bars are still
// falling, and parked otherwise: one capture and one FFT however many bars show
// it, frames reaching each bar's loop latest-only. samples and frame are the
// tick's buffers, reused frame to frame.
type cavaAnalyzer struct {
	plan   *analyzer.Plan
	source analyzer.Input
	shared spectrumFrame
	hub    *pollHub

	frameEvery time.Duration
	every      func(time.Duration, func()) func()

	mu      sync.Mutex
	samples []float64
	frame   []float64
	// stopTick is the running frame timer, nil while parked.
	stopTick func()
	retired  bool
	bars     map[*cavaBarSlot]struct{}
	released func()
}

// cavaBarSlot hands frames to one bar's loop: the newest frame, with one call
// waiting at a time.
type cavaBarSlot struct {
	paint *spectrum
	post  func(func())
	apply func()

	mu      sync.Mutex
	values  []float64
	spare   []float64
	waiting bool
}

func newCavaBarSlot(paint *spectrum, post func(func())) *cavaBarSlot {
	s := &cavaBarSlot{paint: paint, post: post, values: make([]float64, len(paint.values)), spare: make([]float64, len(paint.values))}
	s.apply = func() {
		s.mu.Lock()
		s.values, s.spare = s.spare, s.values
		s.waiting = false
		frame := s.values
		s.paint.SetFrame(frame)
		s.mu.Unlock()
	}
	return s
}

func (s *cavaBarSlot) give(frame []float64) {
	s.mu.Lock()
	copy(s.spare, frame)
	queue := !s.waiting
	s.waiting = true
	s.mu.Unlock()
	if queue {
		s.post(s.apply)
	}
}

func goEvery(every time.Duration, fn func()) func() {
	done := make(chan struct{})
	stop := sync.OnceFunc(func() { close(done) })
	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fn()
			}
		}
	}()
	return stop
}

func (a *cavaAnalyzer) join(life context.Context, slot *cavaBarSlot) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bars[slot] = struct{}{}
	context.AfterFunc(life, func() { a.leave(slot) })
	a.resumeLocked()
}

func (a *cavaAnalyzer) leave(slot *cavaBarSlot) {
	a.mu.Lock()
	delete(a.bars, slot)
	last := len(a.bars) == 0
	if last {
		a.retired = true
		if a.stopTick != nil {
			a.stopTick()
			a.stopTick = nil
		}
	}
	a.mu.Unlock()
	if last {
		if a.released != nil {
			a.released()
		}
		a.source.Stop()
	}
}

// resume starts the frame timer; sound reaching a parked analyzer lands here,
// and once its last bar left it stays stopped.
func (a *cavaAnalyzer) resume() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resumeLocked()
}

func (a *cavaAnalyzer) resumeLocked() {
	if a.retired || a.stopTick != nil {
		return
	}
	a.stopTick = a.every(a.frameEvery, a.tick)
}

// tick analyzes what arrived and hands on what moved: with no new samples the
// bars still fall off. Once silence leaves nothing moving the analyzer parks, so
// a quiet session costs no frames. The capture keeps itself attached across
// default changes and server kills.
func (a *cavaAnalyzer) tick() {
	a.mu.Lock()
	if a.stopTick == nil {
		a.mu.Unlock()
		return
	}
	a.samples = a.source.Drained(a.samples)
	if a.hub != nil && a.hub.locked.Load() {
		a.mu.Unlock()
		return
	}
	a.frame = a.plan.Execute(a.frame, a.samples)
	moved := a.shared.advance(a.frame)
	park := !moved && analyzer.Silent(a.samples)
	if moved {
		for slot := range a.bars {
			slot.give(a.frame)
		}
	}
	if park {
		a.stopTick()
		a.stopTick = nil
	}
	a.mu.Unlock()
	if park {
		a.source.NotifySound(a.resume)
	}
}

// cavaBars is the bar count and channel count the service runs with:
// stereo splits the bars between left and right, so an odd count is
// rounded up (adjusted_for_stereo).
func cavaBars(cfg config.CavaConfig) (bars, channels int) {
	bars, channels = int(cfg.Bars), 1
	if cfg.Stereo {
		channels = 2
		if bars%2 != 0 {
			log.Printf("cava: odd bar count %d rounded up to %d for stereo output", bars, bars+1)
			bars++
		}
	}
	return bars, channels
}

// cavaInputSupported reports the inputs the Go shell captures, the set
// the Rust shell's libcava is built with: PipeWire and PulseAudio (both
// through the PulseAudio protocol, pipewire-pulse on a PipeWire
// system), a FIFO and squeezelite's shared memory. libcava's others
// are compiled out there, and selecting one exits the Rust shell; here
// the module fails to load with the reason.
func cavaInputSupported(input config.CavaInput) bool {
	switch input {
	case config.CavaInputPipeWire, config.CavaInputPulse, config.CavaInputFifo, config.CavaInputShmem:
		return true
	}
	return false
}

// cavaInput builds the configured capture.
func cavaInput(ctx ModuleContext, cfg config.CavaConfig, channels, capacity int) (analyzer.Input, error) {
	switch cfg.Input {
	case config.CavaInputFifo:
		return analyzer.NewFifoInput(cfg.Source, channels, capacity)
	case config.CavaInputShmem:
		return analyzer.NewShmemInput(cfg.Source, channels, capacity)
	}
	if ctx.Pulse == nil {
		return nil, errCavaNoAudio
	}
	return analyzer.NewSource(ctx.Pulse, cfg.Source, channels, capacity)
}

func newCava(ctx ModuleContext) (Module, error) {
	cfg := ctx.Config.Cava
	if !cavaInputSupported(cfg.Input) {
		return nil, fmt.Errorf("cava: the %q input is not built in (pipe-wire, pulse, fifo and shmem are)", cfg.Input)
	}
	// monstercat and waves are passed to libcava's config by the Rust
	// service but only its output stage (never run there) reads them,
	// so they change nothing in either shell.
	bars, channels := cavaBars(cfg)
	plan, err := analyzer.NewPlan(bars/channels, cavaRate, channels, float64(cfg.NoiseReduction), true, int(cfg.LowCutoff), int(cfg.HighCutoff))
	if err != nil {
		return nil, err
	}
	vertical := ctx.Config.Bar.Location.IsVertical()
	paint := newSpectrum(cfg, bars, resolveModuleColor(ctx, cfg.Color), float64(ctx.Config.Bar.Scale), vertical)
	module := &cavaModule{paint: paint}
	if ctx.App == nil {
		module.analyzer = &cavaAnalyzer{plan: plan}
		return module, nil
	}
	an, err := sharedCavaAnalyzer(ctx, cfg, plan, bars, channels)
	if err != nil {
		return nil, err
	}
	module.analyzer = an
	an.join(ctx.Life(), newCavaBarSlot(paint, ctx.Invoke))
	return module, nil
}

// sharedCavaAnalyzer is the snapshot's one analyzer, its capture starting on
// the first bar that asks.
func sharedCavaAnalyzer(ctx ModuleContext, cfg config.CavaConfig, plan *analyzer.Plan, bars, channels int) (*cavaAnalyzer, error) {
	key := configKey("cava", ctx.Config)
	build := func() (*cavaAnalyzer, error) {
		source, err := cavaInput(ctx, cfg, channels, plan.InputSize())
		if err != nil {
			return nil, err
		}
		if err := source.Start(); err != nil {
			return nil, err
		}
		return newCavaAnalyzer(plan, source, bars, cfg.Style, ctx.Polls, time.Second/time.Duration(cfg.Framerate), goEvery), nil
	}
	if ctx.Polls == nil {
		return build()
	}
	return ctx.Polls.cavaAnalyzer(key, build)
}

func newCavaAnalyzer(plan *analyzer.Plan, source analyzer.Input, bars int, style config.CavaStyle, hub *pollHub, frame time.Duration, every func(time.Duration, func()) func()) *cavaAnalyzer {
	return &cavaAnalyzer{plan: plan, source: source, shared: newSpectrumFrame(bars, style), hub: hub, frameEvery: frame, every: every, bars: map[*cavaBarSlot]struct{}{}}
}

var errCavaNoAudio = errors.New("cava: no audio server connection")

func (m *cavaModule) Root() widget.Widget { return m.paint }

// resolveModuleColor resolves a module color value against the palette.
func resolveModuleColor(ctx ModuleContext, cv config.ColorValue) render.Color {
	color, ok := styling.ResolveColor(cv, ctx.Style.palette)
	if !ok {
		return render.Color(0)
	}
	return color
}
