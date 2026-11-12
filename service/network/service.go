package network

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Service is the network service beyond the bar's status read
// (service.rs's NetworkService): saved profiles, the wifi device, the
// secret agent, and the VPNs.
type Service struct {
	// Settings is the live list of saved profiles.
	Settings *Settings
	// Wifi is the wireless device, nil when there is none.
	Wifi *Wifi
	// Agent is the secret agent's prompt state: the credential prompt
	// NM is blocked on, answered with Submit or Cancel.
	Agent *Agent
	// VPN is every VPN profile and its live state.
	VPN *VPNService
}

// Start brings the service up on conn (the system bus) until ctx ends.
// The secret agent is served before anything can activate: a VPN that
// needs a password has nowhere to ask otherwise. openconnect VPNs sign
// in natively (NativeSignIn); other secrets come from vault when it has
// them (nil for no vault), else from the user.
func Start(ctx context.Context, conn *dbus.Conn, vault Vault) (*Service, error) {
	return start(ctx, conn, NativeSignIn, vault, defaultResumeTiming)
}

// start brings the service up under a context of its own. What it puts
// on conn (the agent export, the match rules, the signal channels) is
// taken back when ctx ends, and right away, before start returns, when a
// step fails: conn is the bar's shared system connection, which outlives
// the service.
func start(ctx context.Context, conn *dbus.Conn, signIn SignIn, vault Vault, timing resumeTiming) (*Service, error) {
	life, cancel := context.WithCancel(ctx)
	var held unwind
	fail := func(err error) (*Service, error) {
		cancel()
		return nil, errors.Join(err, held.run())
	}
	n := nm{conn: conn}
	settings, stopSettings, err := newSettings(life, n)
	if err != nil {
		return fail(fmt.Errorf("cannot initialize network service: cannot initialize Settings: %w", err))
	}
	held.push(stopSettings)
	svc := &Service{Settings: settings, Agent: NewAgent()}
	if device, ok := findWifiDevice(life, n); ok {
		svc.Wifi = &Wifi{nm: n, settings: settings, Device: device}
	}
	stopAgent, err := serveAgent(life, conn, &agentObject{agent: svc.Agent, signIn: signIn, vault: vault, active: n.profileState, budget: RequestBudget})
	if err != nil {
		return fail(err)
	}
	held.push(stopAgent)
	vpn, stopVPN, err := newVPNService(life, n, settings, svc.Agent, signIn, timing)
	if err != nil {
		return fail(err)
	}
	held.push(stopVPN)
	svc.VPN = vpn
	context.AfterFunc(ctx, func() {
		cancel()
		if err := held.run(); err != nil {
			log.Printf("network: stop: %v", err)
		}
	})
	return svc, nil
}

// unwind is what a started service holds on the shared connection, taken
// back last first.
type unwind []func() error

func (u *unwind) push(stop func() error) { *u = append(*u, stop) }

func (u unwind) run() error {
	errs := make([]error, 0, len(u))
	for _, stop := range slices.Backward(u) {
		errs = append(errs, stop())
	}
	return errors.Join(errs...)
}
