package osd

import (
	"context"
	"log"
	"math"

	"github.com/stubbedev/lys-shell/i18n"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/keytoggles"
	"github.com/stubbedev/lys-shell/service/pulse"
)

// level is a slider's dedupe key (last_volume): the rounded level and
// the mute state.
type level struct {
	percent uint32
	muted   bool
	set     bool
}

func levelOf(percent float64, muted bool) level {
	return level{percent: uint32(math.Round(max(percent, 0))), muted: muted, set: true}
}

// audioEvents is handle_volume_changed and handle_input_volume_changed:
// the default devices' levels, each flashed only when its rounded level
// or mute state moved.
type audioEvents struct {
	out, in level
}

func (a *audioEvents) next(ctx context.Context, src pulse.Source) []Event {
	var evs []Event
	if sink, err := src.DefaultSink(ctx); err == nil {
		d := sink.Device
		pct := d.Volume.AveragePercentage()
		if l := levelOf(pct, d.Muted); l != a.out {
			a.out = l
			evs = append(evs, Slider(d.Description, VolumeIcon(pct, d.Muted), pct, d.Muted))
		}
	}
	if source, err := src.DefaultSource(ctx); err == nil {
		d := source.Device
		pct := d.Volume.AveragePercentage()
		if l := levelOf(pct, d.Muted); l != a.in {
			a.in = l
			evs = append(evs, Slider(d.Description, MicrophoneIcon(d.Muted), pct, d.Muted))
		}
	}
	return evs
}

// brightnessEvents is handle_brightness_changed: the average over every
// backlight (a brightness action drives them all), flashed when its
// rounded value moved.
type brightnessEvents struct{ last level }

func (b *brightnessEvents) next(ctx context.Context, src brightness.Source) (Event, bool) {
	devices, err := src.Devices(ctx)
	if err != nil {
		return Event{}, false
	}
	pct, ok := brightness.AveragePercentage(devices)
	if !ok {
		return Event{}, false
	}
	l := levelOf(pct, false)
	if l == b.last {
		return Event{}, false
	}
	b.last = l
	return Slider(i18n.T("osd-brightness"), BrightnessIcon, pct, false), true
}

// Layouts is the compositor's keyboard layout: Layout reads the active
// one, and each tick from Subscribe is the cue to read it again.
type Layouts interface {
	Layout() (string, error)
	Subscribe(ctx context.Context) (<-chan struct{}, func(), error)
}

// layoutEvents flashes a keyboard layout switch: the layout's name
// when it differs from the last one read. An unreadable or unknown
// layout is not a switch.
type layoutEvents struct{ last string }

func (l *layoutEvents) next(src Layouts) (Event, bool) {
	name, err := src.Layout()
	if err != nil {
		log.Printf("osd: keyboard layout: %v", err)
		return Event{}, false
	}
	if name == "" || name == l.last {
		return Event{}, false
	}
	l.last = name
	return layoutEvent(name), true
}

// Watch follows the audio, brightness, keyboard layout, and lock-key
// sources until ctx ends (watchers.rs spawn). A nil source is skipped. The levels at
// start seed the dedupe without flashing, as the Rust watchers' first
// emission lands inside the quiet period.
func (o *Osd) Watch(ctx context.Context, audio pulse.Source, light brightness.Source, layouts Layouts) {
	if audio != nil {
		if ticks, stop, err := audio.Subscribe(ctx); err == nil {
			defer stop()
			go func() {
				var a audioEvents
				a.next(ctx, audio)
				for range ticks {
					for _, ev := range a.next(ctx, audio) {
						o.Show(ev)
					}
				}
			}()
		} else {
			log.Printf("osd: audio: %v", err)
		}
	}
	if light != nil {
		if ticks, stop, err := light.Subscribe(ctx); err == nil {
			defer stop()
			go func() {
				var b brightnessEvents
				b.next(ctx, light)
				for range ticks {
					if ev, ok := b.next(ctx, light); ok {
						o.Show(ev)
					}
				}
			}()
		} else {
			log.Printf("osd: brightness: %v", err)
		}
	}
	if layouts != nil {
		if ticks, stop, err := layouts.Subscribe(ctx); err == nil {
			if stop != nil {
				defer stop()
			}
			go func() {
				var l layoutEvents
				l.next(layouts)
				for range ticks {
					if ev, ok := l.next(layouts); ok {
						o.Show(ev)
					}
				}
			}()
		} else {
			log.Printf("osd: keyboard layout: %v", err)
		}
	}
	defer keytoggles.Watch(func(e keytoggles.Event) { o.Show(toggleEvent(e)) })()
	<-ctx.Done()
}
