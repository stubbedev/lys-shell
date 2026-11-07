package bar

import (
	"context"
	"log"
	"sync"

	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/internal/clockwatch"
	"github.com/stubbedev/lys-shell/service/notifications"
	"github.com/stubbedev/lys-shell/service/recorder"
)

type sharingSignals struct {
	mu        sync.Mutex
	shares    bool
	recording bool
	svc       *notifications.Service
}

func (s *sharingSignals) set(apply func()) {
	s.mu.Lock()
	apply()
	on := s.shares || s.recording
	s.mu.Unlock()
	s.svc.SetSharing(on)
}

func followFocusInputs(application *app.Application, svc *notifications.Service, rec *recorder.State) func() {
	life, cancel := context.WithCancel(context.Background())
	var done sync.WaitGroup
	done.Go(func() {
		if err := clockwatch.Watch(life, svc.ClockJumped); err != nil {
			log.Printf("notifications: the do-not-disturb schedule will not follow clock jumps: %v", err)
		}
	})
	signals := &sharingSignals{svc: svc}
	if src, err := screenShareSource(); err == nil {
		ticks, stop := src.Subscribe()
		signals.set(func() { signals.shares = len(src.List()) > 0 })
		done.Go(func() {
			defer stop()
			for {
				select {
				case <-life.Done():
					return
				case <-ticks:
					signals.set(func() { signals.shares = len(src.List()) > 0 })
				}
			}
		})
	} else {
		log.Printf("notifications: do-not-disturb will not follow screen shares: %v", err)
	}
	if rec != nil {
		changes, stop := rec.Changes()
		signals.set(func() { signals.recording = rec.Snapshot().Active })
		done.Go(func() {
			defer stop()
			for {
				select {
				case <-life.Done():
					return
				case c, ok := <-changes:
					if !ok {
						return
					}
					signals.set(func() { signals.recording = c.Active })
				}
			}
		})
	}
	stopToplevels := func() {}
	if application.ToplevelsAvailable() {
		follow := func() { svc.SetFullscreen(anyFullscreen(application.Toplevels())) }
		stopToplevels = application.OnToplevelsChanged(follow)
		follow()
	}
	return func() {
		stopToplevels()
		cancel()
		done.Wait()
	}
}

func anyFullscreen(toplevels []app.Toplevel) bool {
	for _, t := range toplevels {
		if t.Fullscreen() {
			return true
		}
	}
	return false
}
