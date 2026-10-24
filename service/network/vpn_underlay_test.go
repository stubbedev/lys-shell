package network

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/wayle/service/network/openconnect"
	"github.com/stubbedev/wayle/service/network/secrets"
)

func TestANetworkMissingForTheStallRunIsLostOnce(t *testing.T) {
	misses, change := nextLook(0, false)
	if misses != 1 || change != lookNothing {
		t.Fatalf("first miss = (%d, %v), want (1, nothing): one miss is a lease swap", misses, change)
	}
	misses, change = nextLook(misses, false)
	if misses != stallAfter || change != lookLost {
		t.Fatalf("second miss = (%d, %v), want (%d, lost)", misses, change, stallAfter)
	}
	misses, change = nextLook(misses, false)
	if misses != stallAfter+1 || change != lookNothing {
		t.Errorf("third miss = (%d, %v): lost is said once, not on every look", misses, change)
	}
}

func TestANetworkBackAfterBeingLostIsBackAndABriefMissIsNothing(t *testing.T) {
	for _, tc := range []struct {
		misses int
		want   lookChange
	}{{stallAfter + 3, lookBack}, {1, lookNothing}, {0, lookNothing}} {
		if misses, change := nextLook(tc.misses, true); misses != 0 || change != tc.want {
			t.Errorf("usable after %d misses = (%d, %v), want (0, %v)", tc.misses, misses, change, tc.want)
		}
	}
}

func TestAStalledTunnelSettlesByNMsStateAndTheTurnOff(t *testing.T) {
	for _, tc := range []struct {
		state     ActiveState
		running   bool
		turnedOff bool
		want      afterStall
	}{
		{ActiveActivated, true, false, stallUp},
		{ActiveActivating, true, false, stallUp},
		{ActiveUnknown, false, false, stallRestore},
		{ActiveDeactivated, true, false, stallRestore},
		{ActiveUnknown, false, true, stallStayDown},
		{ActiveActivated, true, true, stallStayDown},
	} {
		if got := settleStall(tc.state, tc.running, tc.turnedOff); got != tc.want {
			t.Errorf("settleStall(%v, running %v, off %v) = %v, want %v", tc.state, tc.running, tc.turnedOff, got, tc.want)
		}
	}
}

func TestAnyIPv4AddressCarriesATunnelButALinkLocalIPv6OneDoesNot(t *testing.T) {
	for _, tc := range []struct {
		address string
		ipv6    bool
		want    bool
	}{
		{"192.168.1.166", false, true},
		{"2a02:aa7:4000:1::5", true, true},
		{"fe80::1c2b:3cff:fe4d:5e6f", true, false},
		{"FE80::1", true, false},
		{"", false, false},
	} {
		if got := isRoutable(tc.address, tc.ipv6); got != tc.want {
			t.Errorf("isRoutable(%q, %v) = %v, want %v", tc.address, tc.ipv6, got, tc.want)
		}
	}
}

// stallWork connects the "work" tunnel, then takes the address from the
// network under it and waits for the row to show it.
func stallWork(t *testing.T, fx *serviceFixture) dbus.ObjectPath {
	t.Helper()
	up := fx.nm.startActive("work", ActiveActivated)
	fx.waitRow(t, "work", "connected", func(r VPN) bool { return r.State == VPNConnected })
	time.Sleep(5 * fastResume.underlayLook)
	if row := fx.row(t, "work"); row.State != VPNConnected {
		t.Fatalf("with an address the row is %+v, want connected", row)
	}
	fx.nm.setUnderlay()
	fx.waitRow(t, "work", "reconnecting while the network is gone", func(r VPN) bool {
		return r.State == VPNConnecting && r.Detail == waitingDetail
	})
	return up
}

func TestATunnelOverALostNetworkIsReconnectingUntilTheAddressIsBack(t *testing.T) {
	fx := startService(t, withProfiles(gpConnection("work")))
	stallWork(t, fx)
	if got := fx.svc.VPN.Aggregate(); got != VPNConnecting {
		t.Errorf("aggregate = %v, want connecting", got)
	}
	fx.nm.setUnderlay("192.168.1.170")
	fx.waitRow(t, "work", "connected again", func(r VPN) bool { return r.State == VPNConnected && r.Detail == "" })
	if got := fx.activations(); len(got) != 0 {
		t.Errorf("activations = %v: NM still ran the tunnel, nothing to restore", got)
	}
}

func TestATunnelThatDroppedWhileTheNetworkWasGoneIsRestored(t *testing.T) {
	fx := startService(t, withProfiles(gpConnection("work")))
	fx.signIn.onAuthenticate(func(context.Context, openconnect.Profile, bool, bool, secrets.Prompter) (map[string]string, error) {
		return map[string]string{"cookie": "c"}, nil
	})
	up := stallWork(t, fx)
	fx.nm.dropActive(up)
	time.Sleep(5 * fastResume.underlayLook)
	if row := fx.row(t, "work"); row.State != VPNConnecting {
		t.Errorf("dropped while stalled: row %+v, want still reconnecting", row)
	}
	fx.nm.setUnderlay("192.168.1.170")
	waitFor(t, "the restore", func() bool { return len(fx.activations()) == 1 })
	if got := fx.activations(); !slices.Equal(got, []dbus.ObjectPath{settingPath(1)}) {
		t.Errorf("activations = %v, want the dropped tunnel", got)
	}
}

func TestATunnelTurnedOffWhileTheNetworkWasGoneStaysOff(t *testing.T) {
	fx := startService(t, withProfiles(gpConnection("work")))
	stallWork(t, fx)
	must(t, fx.svc.VPN.Disconnect(t.Context(), "work"))
	fx.nm.setUnderlay("192.168.1.170")
	fx.waitRow(t, "work", "off", func(r VPN) bool { return r.State == VPNDisconnected && r.Detail == "" })
	time.Sleep(5 * fastResume.underlayLook)
	if got := fx.activations(); len(got) != 0 {
		t.Errorf("activations = %v: a tunnel turned off stays off", got)
	}
}
