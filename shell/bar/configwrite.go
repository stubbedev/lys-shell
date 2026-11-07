package bar

import (
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/config"
)

type configStore interface {
	SetByPath(path string, value any) error
	Save() error
}

type configChange struct {
	path  string
	value any
}

// configWriter applies config changes in order on a goroutine of its own and
// saves once whatever queued meanwhile is applied, so neither the bus nor the
// fsync sits on the loop.
type configWriter struct {
	store configStore

	mu      sync.Mutex
	queue   []configChange
	writing bool
	idle    chan struct{}
}

func (w *configWriter) set(path string, value any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.queue = append(w.queue, configChange{path, value})
	if !w.writing {
		w.writing = true
		w.idle = make(chan struct{})
		go w.write()
	}
}

func (w *configWriter) write() {
	dirty := false
	for {
		w.mu.Lock()
		if len(w.queue) == 0 && !dirty {
			w.writing = false
			close(w.idle)
			w.mu.Unlock()
			return
		}
		if len(w.queue) == 0 {
			w.mu.Unlock()
			if err := w.store.Save(); err != nil {
				log.Printf("config: save: %v", err)
			}
			dirty = false
			continue
		}
		next := w.queue[0]
		w.queue = w.queue[1:]
		w.mu.Unlock()
		if err := w.store.SetByPath(next.path, next.value); err != nil {
			log.Printf("config: set %s: %v", next.path, err)
			continue
		}
		dirty = true
	}
}

// flush waits for what was asked so far to reach the disk, or for timeout.
func (w *configWriter) flush(timeout time.Duration) {
	w.mu.Lock()
	idle := w.idle
	writing := w.writing
	w.mu.Unlock()
	if !writing {
		return
	}
	select {
	case <-idle:
	case <-time.After(timeout):
		log.Printf("config: a save did not finish in %v", timeout)
	}
}

// setConfig writes through ctx.SetConfig when there is a service.
func (c ModuleContext) setConfig(path string, value any) {
	if c.SetConfig != nil {
		c.SetConfig(path, value)
	}
}

// barsAffected reports whether a reload changes anything the bars
// render. The recorder's capture settings, which its dropdown writes,
// only shape the next recording: rebuilding the bars for them would
// close the dropdown being edited, and no bar renders the emoji skin tone
// the launcher keeps from a pick. High contrast and reduce transparency
// restyle the open bars instead (accessibilityChanged).
func barsAffected(old, next *config.Config) bool {
	if old == nil || next == nil {
		return true
	}
	a, b := *old, *next
	for _, c := range []*config.Config{&a, &b} {
		c.Styling.HighContrast, c.Styling.ReduceTransparency = false, false
		r := &c.Recorder
		r.Microphone, r.MicrophoneDevice, r.SystemAudio = false, "", false
		r.WebcamEnabled, r.WebcamDevice = false, ""
		r.WebcamX, r.WebcamY = 0, 0
		c.Launcher.Emoji.SkinTone = ""
	}
	return !reflect.DeepEqual(a, b)
}

// accessibilityChanged reports a change of high contrast or reduce
// transparency, which restyles the open bars without rebuilding them.
func accessibilityChanged(old, next *config.Config) bool {
	return old.Styling.HighContrast != next.Styling.HighContrast || old.Styling.ReduceTransparency != next.Styling.ReduceTransparency
}
