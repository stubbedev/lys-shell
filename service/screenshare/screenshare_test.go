package screenshare

import (
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/bussignal/bussignaltest"
)

type portal struct{ shares []Share }

func (p portal) List() ([]Share, *dbus.Error)   { return p.shares, nil }
func (portal) Stop(dbus.ObjectPath) *dbus.Error { return nil }

func waitList(t *testing.T, s *Shares, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(s.List()) != n {
		if time.Now().After(deadline) {
			t.Fatalf("shares %+v, want %d", s.List(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSharesFollowThePortalComingAndGoing(t *testing.T) {
	bus := dbustest.Start(t)
	shares, err := Follow(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer shares.Close()
	if got := shares.List(); len(got) != 0 {
		t.Fatalf("shares with no portal: %+v", got)
	}
	ticks, stop := shares.Subscribe()
	defer stop()
	release, err := dbusx.Serve(bus.Conn(t), dbusx.Service{
		Name: BusName, Path: Path, Interface: Interface,
		Methods: portal{shares: []Share{{Session: "/s/1", Kind: KindScreenCast, Targets: []Target{{Kind: TargetOutput, Name: "DP-1"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitList(t, shares, 1)
	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Fatal("no change was told")
	}
	release()
	waitList(t, shares, 0)
}

// The shares read the portal's signals off a channel of their own and
// keep none of the descriptors one carries: each is closed once the
// signal is handled.
func TestSharesCloseTheDescriptorsOfTheSignalsTheyRead(t *testing.T) {
	bus := dbustest.Start(t)
	shares, err := Follow(bus.Conn(t))
	if err != nil {
		t.Fatal(err)
	}
	defer shares.Close()
	served := bus.Conn(t)
	release, err := dbusx.Serve(served, dbusx.Service{Name: BusName, Path: Path, Interface: Interface, Methods: portal{}})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	fd, closed := bussignaltest.Descriptor(t)
	if err := served.Emit(Path, Interface+".Handed", fd); err != nil {
		t.Fatal(err)
	}
	closed("the shares handled a signal carrying a descriptor")
}
