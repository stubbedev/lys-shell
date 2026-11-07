package bar

import (
	"context"
	"errors"
	"log"
	"sync"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/idle"
	"github.com/stubbedev/lys-shell/service/logind"
	"github.com/stubbedev/lys-shell/service/upower"
)

type idlePolicy struct {
	svc     *idle.Service
	comp    *idle.Compositor
	system  *dbus.Conn
	session *logind.Client

	mu        sync.Mutex
	cfg       config.IdleConfig
	source    idle.PowerSource
	stopSleep func()
}

func startIdlePolicy(system *dbus.Conn, backlights idle.Backlights, cfg config.IdleConfig) (*idlePolicy, func()) {
	comp, err := idle.Connect()
	if err != nil {
		log.Printf("idle: the built-in idle policy will not run: %v", err)
		return nil, func() {}
	}
	p := &idlePolicy{comp: comp, system: system}
	if system != nil {
		ctx, cancel := context.WithTimeout(context.Background(), logind.CallTimeout)
		p.session, err = logind.NewClient(ctx, system)
		cancel()
		if err != nil {
			log.Printf("idle: the lock and suspend stages will fail: %v", err)
		}
	}
	actions := idle.System{Backlights: backlights, Outputs: comp}
	if p.session != nil {
		actions.Session, actions.Bus = p.session, system
	}
	p.svc = idle.New(comp, actions)
	life, stopPower := context.WithCancel(context.Background())
	powerDone := p.followPowerSource(life)
	p.configure(cfg)
	return p, func() {
		stopPower()
		<-powerDone
		p.mu.Lock()
		p.stopSleepLocked()
		p.mu.Unlock()
		if err := p.svc.Close(); err != nil {
			log.Printf("idle: %v", err)
		}
		comp.Close()
	}
}

func (p *idlePolicy) configure(cfg config.IdleConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	if err := p.svc.Configure(cfg.Policy(), cfg.Enable, p.source); err != nil {
		log.Printf("idle: %v", err)
	}
	p.stopSleepLocked()
	if !cfg.Enable || !cfg.LockBeforeSleep {
		return
	}
	if p.session == nil {
		log.Print("idle: lock-before-sleep needs logind, which is not connected")
		return
	}
	stop, err := idle.LockBeforeSleep(p.system, p.session, logind.CallTimeout)
	if err != nil {
		log.Printf("idle: %v", err)
		return
	}
	p.stopSleep = stop
}

func (p *idlePolicy) stopSleepLocked() {
	if p.stopSleep != nil {
		p.stopSleep()
		p.stopSleep = nil
	}
}

func (p *idlePolicy) followPowerSource(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if p.system == nil {
		close(done)
		return done
	}
	battery := upower.NewOn(p.system)
	ticks, stop, err := battery.SubscribeOnBattery(ctx)
	if err != nil {
		log.Printf("idle: power source: %v; the AC timeouts apply", err)
		close(done)
		return done
	}
	p.source = readPowerSource(ctx, battery)
	go func() {
		defer close(done)
		defer stop()
		for range ticks {
			source := readPowerSource(ctx, battery)
			p.mu.Lock()
			p.source = source
			err := p.svc.SetPowerSource(source)
			p.mu.Unlock()
			if err != nil {
				log.Printf("idle: %v", err)
			}
		}
	}()
	return done
}

func readPowerSource(ctx context.Context, battery *upower.System) idle.PowerSource {
	reading, cancel := context.WithTimeout(ctx, logind.CallTimeout)
	defer cancel()
	on, err := battery.OnBattery(reading)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("idle: power source: %v; the AC timeouts apply", err)
		}
		return idle.AC
	}
	if on {
		return idle.Battery
	}
	return idle.AC
}
