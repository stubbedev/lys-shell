package network

import (
	"context"
	"strings"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// leftOn is what the service holds on conn as the bus and NM see it:
// its match rules, whether its agent object answers, and whether NM
// still has it registered.
func leftOn(t *testing.T, f *fakeNM, conn *dbus.Conn) (rules []string, exported, registered bool) {
	t.Helper()
	err := f.conn.Object(conn.Names()[0], AgentPath).Call(agentIface+".CancelGetSecrets", 0, settingPath(1), "vpn").Err
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.registrations {
		registered = registered || r.sender == conn.Names()[0]
	}
	return dbustest.MatchRules(t, conn), !strings.HasPrefix(errName(err), "org.freedesktop.DBus.Error.Unknown"), registered
}

// A Start that fails after the agent is exported and registered takes
// all of it back before it returns: the system connection is the bar's
// shared one and outlives the service.
func TestAFailedStartLeavesTheSharedConnectionAsItWas(t *testing.T) {
	f := startFakeNM(t)
	conn := f.client()
	ctx, cancel := context.WithCancel(t.Context())
	registered := false
	f.onRegister = func() { registered = true; cancel() }
	svc, err := start(ctx, conn, &fakeSignIn{}, nil, fastResume)
	if err == nil || svc != nil {
		t.Fatalf("start with its context ending at the registration = %v, %v; want an error", svc, err)
	}
	if !registered {
		t.Fatal("start failed before the agent registered, so nothing tested the unwinding")
	}
	if rules, exported, reg := leftOn(t, f, conn); len(rules) != 0 || exported || reg {
		t.Fatalf("the failed start left the rules %q, an export %v, a registration %v", rules, exported, reg)
	}
}

// A started service holds its watches and its agent until its context
// ends, and then takes them back.
func TestAStoppedServiceLeavesTheSharedConnectionAsItWas(t *testing.T) {
	f := startFakeNM(t)
	conn := f.client()
	ctx, cancel := context.WithCancel(t.Context())
	if _, err := start(ctx, conn, &fakeSignIn{}, nil, fastResume); err != nil {
		t.Fatal(err)
	}
	f.registered(t)
	if rules, exported, reg := leftOn(t, f, conn); len(rules) == 0 || !exported || !reg {
		t.Fatalf("the running service holds the rules %q, an export %v, a registration %v; want all three", rules, exported, reg)
	}
	cancel()
	var rules []string
	var exported, reg bool
	waitFor(t, "the stopped service to leave the connection", func() bool {
		rules, exported, reg = leftOn(t, f, conn)
		return len(rules) == 0 && !exported && !reg
	})
}
