package sharepicker

import (
	"slices"
	"testing"
)

func TestParseReply(t *testing.T) {
	token, targets, ok := ParseReply("r/screen:DP-1;window:firefox@instance-3;region:HDMI-A-1@10,-20,800,600")
	var got []string
	for _, t := range targets {
		got = append(got, t.Payload())
	}
	if !ok || !token || !slices.Equal(got, []string{"screen:DP-1", "window:firefox@instance-3", "region:HDMI-A-1@10,-20,800,600"}) {
		t.Errorf("= %v %v %v", token, got, ok)
	}
	if want := (Target{Kind: TargetRegion, Name: "HDMI-A-1", X: 10, Y: -20, Width: 800, Height: 600}); targets[2] != want {
		t.Errorf("region = %+v, want %+v", targets[2], want)
	}
	if token, _, ok := ParseReply("/screen:DP-1"); !ok || token {
		t.Errorf("no r flag: token %v ok %v, want no token", token, ok)
	}
	for _, bad := range []string{"", "garbage", "/screen:", "/window:", "/region:DP-1@1,2,3", "/region:DP-1@1,2,0,5", "/region:@1,2,3,4", "/bogus:x", "/screen:DP-1;bogus"} {
		if _, _, ok := ParseReply(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestJoinTargetsRoundTrips(t *testing.T) {
	in := []Target{{Kind: TargetRegion, Name: "DP-2", X: 5, Y: 6, Width: 640, Height: 480}, {Kind: TargetWindow, Name: "w1"}, {Kind: TargetOutput, Name: "eDP-1"}}
	out, ok := ParseTargets(JoinTargets(in))
	if !ok || !slices.Equal(out, in) {
		t.Errorf("round trip = %+v %v, want %+v", out, ok, in)
	}
}
