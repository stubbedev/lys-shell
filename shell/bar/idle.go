package bar

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-desktop/service/idle/idlepolicy"
	"github.com/stubbedev/lys-desktop/service/upower"
	"github.com/stubbedev/lys-lib/logind"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/idle"
)

// The waits of the idle policy's non-logind calls: how long lock-before-
// sleep waits for the lock screen, and one read of the power source.
const (
	sleepLockWait = 5 * time.Second
	upowerTimeout = 5 * time.Second
)

type idlePolicy struct {
	svc    *idle.Service
	comp   *idle.Compositor
	system *dbus.Conn
	login  *idle.Login

	mu        sync.Mutex
	cfg       config.IdleConfig
	source    idlepolicy.PowerSource
	stopSleep func()
}

func startIdlePolicy(system *dbus.Conn, backlights idle.Backlights, cfg config.IdleConfig) (*idlePolicy, func()) {
	comp, err := idle.Connect()
	if err != nil {
		log.Printf("idle: the built-in idle policy will not run: %v", err)
		return nil, func() {}
	}
	p := &idlePolicy{comp: comp, system: system, login: idleLogin(system)}
	actions := idle.System{Backlights: backlights, Login: p.login, Outputs: comp}
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

func idleLogin(system *dbus.Conn) *idle.Login {
	if system == nil {
		return nil
	}
	m := logind.NewManager(system)
	session, err := m.GetSession(context.Background(), logind.AutoSession)
	if err != nil {
		log.Printf("idle: the lock and suspend stages will fail: %v", err)
		return nil
	}
	return &idle.Login{Manager: m, Session: session}
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
	if p.login == nil {
		log.Print("idle: lock-before-sleep needs logind, which is not connected")
		return
	}
	stop, err := idle.LockBeforeSleep(*p.login, sleepLockWait)
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

func readPowerSource(ctx context.Context, battery *upower.System) idlepolicy.PowerSource {
	reading, cancel := context.WithTimeout(ctx, upowerTimeout)
	defer cancel()
	on, err := battery.OnBattery(reading)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("idle: power source: %v; the AC timeouts apply", err)
		}
		return idlepolicy.AC
	}
	if on {
		return idlepolicy.Battery
	}
	return idlepolicy.AC
}
