package network

import (
	"context"
	"log"
	"slices"
	"strings"
	"time"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/feed"
)

// This file watches the network a tunnel rides on (vpn/underlay.rs).
//
// NetworkManager keeps a VPN activated for as long as its plugin process
// runs, and openconnect keeps running while it retries a gateway it
// cannot reach. So when the network under the tunnel loses its address
// - wifi roaming to another access point restarts DHCP, and a slow
// router can leave the machine without a lease for minutes - NM, and
// with it the row, still says connected while nothing gets through.
// NM's own state does not move either: the wifi device stays activated
// through a DHCP restart, and the connectivity check runs minutes apart.
//
// So while a tunnel is up, the network under it is looked at directly:
// does the connection carrying the traffic still hold an address? After
// stallAfter looks running without one, the tunnels that were up show
// as reconnecting, with the reason. When the address is back, a tunnel
// NM still runs shows connected again (openconnect reconnects on its own
// within its window), and one that dropped meanwhile is restored the way
// a suspend restores it, unless someone turned it off.

const (
	// stallAfter is how many looks running without an address make the
	// tunnels stalled. One miss is a DHCP renewal swapping its lease,
	// which loses nothing.
	stallAfter = 2
	// waitingDetail is a stalled tunnel's caption.
	waitingDetail = "Waiting for the network"
)

// lookChange is what one look at the network changes.
type lookChange int

const (
	lookNothing lookChange = iota
	// lookLost: the network has just been missing for stallAfter looks.
	lookLost
	// lookBack: the network is back after having been lost.
	lookBack
)

// nextLook folds one look into the run of looks without an address:
// the new run, and what it changes.
func nextLook(misses int, usable bool) (int, lookChange) {
	if usable {
		if misses >= stallAfter {
			return 0, lookBack
		}
		return 0, lookNothing
	}
	misses++
	if misses == stallAfter {
		return misses, lookLost
	}
	return misses, lookNothing
}

// afterStall is what becomes of a stalled tunnel once the network is
// back.
type afterStall int

const (
	// stallUp: NM still runs it; it is connected again (or getting
	// there).
	stallUp afterStall = iota
	// stallRestore: it dropped while the network was gone; bring it
	// back.
	stallRestore
	// stallStayDown: someone turned it off meanwhile; it stays off.
	stallStayDown
)

// settleStall decides a stalled tunnel's fate from NM's state for it
// now (running false when NM no longer runs it) and whether someone
// turned it off.
func settleStall(state ActiveState, running, turnedOff bool) afterStall {
	if turnedOff {
		return stallStayDown
	}
	if running && (state == ActiveActivated || state == ActiveActivating) {
		return stallUp
	}
	return stallRestore
}

// isRoutable is whether an address off NM's AddressData can carry a
// tunnel: any IPv4 address, or an IPv6 one beyond the link.
func isRoutable(address string, ipv6 bool) bool {
	return address != "" && (!ipv6 || !strings.HasPrefix(strings.ToLower(address), "fe80"))
}

// watchUnderlay looks at the network every timing.underlayLook while a
// tunnel is up or stalled, until ctx ends.
func (s *VPNService) watchUnderlay(ctx context.Context) {
	ticker := time.NewTicker(s.timing.underlayLook)
	defer ticker.Stop()
	misses := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !s.anyUpOrStalled() {
			misses = 0
			continue
		}
		var change lookChange
		misses, change = nextLook(misses, s.nm.networkUsable(ctx))
		switch {
		case change == lookLost:
			log.Printf("network: the network under the VPN lost its address; showing the VPN as reconnecting")
			s.stall()
		case change == lookBack:
			log.Printf("network: the network under the VPN is back")
			s.recover(ctx)
		case misses >= stallAfter:
			s.stall()
		}
	}
}

// anyUpOrStalled reports whether a tunnel rides the network now.
func (s *VPNService) anyUpOrStalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.state.IsConnected() || e.stalled {
			return true
		}
	}
	return false
}

// stall shows every tunnel that is up as reconnecting. Said again on
// every look while the network stays gone: NM's own transitions may
// have set a tunnel connected since, from a state that is no longer
// true.
func (s *VPNService) stall() {
	s.mu.Lock()
	for _, e := range s.entries {
		if e.state.IsConnected() || e.stalled {
			e.stalled, e.state, e.detail = true, VPNConnecting, waitingDetail
		}
	}
	s.mu.Unlock()
	feed.Notify(&s.changes)
}

// recover settles every stalled tunnel once the network is back:
// connected again when NM still runs it, restored when it dropped, left
// off when turned off.
func (s *VPNService) recover(ctx context.Context) {
	running := map[string]ActiveState{}
	if active, err := s.nm.activeStates(ctx); err == nil {
		for uuid, conn := range active {
			running[uuid] = conn.state
		}
	}
	var owed []string
	s.mu.Lock()
	for _, e := range s.entries {
		if !e.stalled {
			continue
		}
		e.stalled, e.detail = false, ""
		state, ok := running[e.uuid]
		switch settleStall(state, ok, e.turnedOff) {
		case stallUp:
			e.state = vpnStateOf(state)
		case stallRestore:
			e.state = VPNDisconnected
			owed = append(owed, e.uuid)
		case stallStayDown:
			e.state = VPNDisconnected
		}
	}
	s.mu.Unlock()
	feed.Notify(&s.changes)
	if len(owed) > 0 {
		s.startRestore(ctx, owed, 0)
	}
}

// networkUsable reports whether the connection carrying the traffic
// holds an address a tunnel can ride: the primary connection, or - when
// a VPN carries the default route itself - the connection under it.
// When NM cannot be asked the network counts as usable: a tunnel is
// never shown stalled on a guess.
func (n nm) networkUsable(ctx context.Context) bool {
	var active dbus.ObjectPath
	if err := n.prop(ctx, nmPath, nmIface, "PrimaryConnection", &active); err != nil {
		return true
	}
	if active == rootPath {
		return false
	}
	var vpn bool
	if err := n.prop(ctx, active, activeIface, "Vpn", &vpn); err != nil {
		return true
	}
	if vpn {
		var base dbus.ObjectPath
		if err := n.prop(ctx, active, activeIface, "SpecificObject", &base); err != nil || base == rootPath {
			return true
		}
		active = base
	}
	return n.configRoutable(ctx, active, "Ip4Config", ip4ConfigIface, false) ||
		n.configRoutable(ctx, active, "Ip6Config", ip6ConfigIface, true)
}

// configRoutable reports whether the active connection's IP config of
// one family holds a routable address.
func (n nm) configRoutable(ctx context.Context, active dbus.ObjectPath, property, iface string, ipv6 bool) bool {
	var config dbus.ObjectPath
	if err := n.prop(ctx, active, activeIface, property, &config); err != nil || config == rootPath {
		return false
	}
	return slices.ContainsFunc(n.configAddresses(ctx, config, iface), func(address string) bool {
		return isRoutable(address, ipv6)
	})
}
