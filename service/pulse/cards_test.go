package pulse

import (
	"context"
	"slices"
	"testing"

	"github.com/stubbedev/lys-shell/service/pulse/native"
	"github.com/stubbedev/lys-shell/service/pulse/pulsetest"
)

func headset() native.CardInfo {
	return native.CardInfo{
		Index: 7, Name: "bluez_card.00_11", Driver: "module-bluez5-device.c",
		Profiles: []native.CardProfile{
			{Name: "a2dp-sink", Description: "High Fidelity Playback (A2DP Sink)", Sinks: 1, Priority: 40, Available: true},
			{Name: "headset-head-unit", Description: "Headset Head Unit (HSP/HFP)", Sinks: 1, Sources: 1, Priority: 30, Available: true},
			{Name: "off", Description: "Off", Available: true},
		},
		ActiveProfile: "a2dp-sink",
		Props:         native.PropList{"device.description": "Headset"},
		Ports:         []native.CardPort{{Name: "headset-output", Description: "Headset", Priority: 0, Available: 2, Direction: 1, Profiles: []string{"a2dp-sink", "headset-head-unit"}}},
	}
}

func TestCardsAreListedAndAProfileSwitchIsOneCallAndFollowed(t *testing.T) {
	for _, version := range []uint32{native.ProtocolVersion, 28, 26} {
		srv := pulsetest.New(t)
		srv.SetVersion(version)
		srv.PutCard(headset())
		s := connect(t, srv)
		cards := s.Cards()
		if len(cards) != 1 || cards[0].ActiveProfile != "a2dp-sink" || len(cards[0].Profiles) != 3 || cards[0].Ports[0].Profiles[1] != "headset-head-unit" {
			t.Fatalf("version %d: %+v", version, cards)
		}
		before := len(srv.Calls())
		if err := s.SetCardProfile(context.Background(), 7, "headset-head-unit"); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the profile change", func() bool { return s.Cards()[0].ActiveProfile == "headset-head-unit" })
		calls := srv.Calls()[before:]
		if slices.Index(calls, native.CmdSetCardProfile) < 0 || count(calls, native.CmdSetCardProfile) != 1 {
			t.Fatalf("version %d: calls %v, want one SET_CARD_PROFILE", version, calls)
		}
		if err := s.SetCardProfile(context.Background(), 7, "no-such-profile"); err == nil {
			t.Fatal("an unknown profile was taken")
		}
	}
}

func count(calls []uint32, cmd uint32) int {
	n := 0
	for _, c := range calls {
		if c == cmd {
			n++
		}
	}
	return n
}
