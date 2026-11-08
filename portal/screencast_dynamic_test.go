package portal

import (
	"slices"
	"testing"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// Where the compositor offers its cast target, a monitor request's
// picker offers it too, and picking it streams it as a monitor with no
// fixed place; the restore token keeps it and replays it without the
// picker.
func TestScreenCastStreamsTheCastTarget(t *testing.T) {
	r, f := castRig(t)
	f.dynamic.Store(true)
	p := serveSharePicker(t, r, "r/dynamic")
	r.castSession(t, Vardict{"persist_mode": dbus.MakeVariant(uint32(2))})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	want := sharepicker.Request{Sources: sharepicker.SourceOutputs | sharepicker.SourceRegions | sharepicker.SourceDynamic, AllowToken: true}
	if asked := p.asked.Load(); len(asked) != 1 || asked[0] != want {
		t.Errorf("picker asked %v, want %+v", asked, want)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"dynamic"}) {
		t.Errorf("started %v, want the cast target", got)
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || streams[0].Props["source_type"].Value() != sourceMonitor {
		t.Fatalf("streams = %+v", streams)
	}
	if _, placed := streams[0].Props["position"]; placed {
		t.Errorf("the cast target carries a position %v, but it moves", streams[0].Props["position"])
	}
	got, ok := decodeRestore(res["restore_data"])
	if !ok || !slices.Equal(payloads(pickedOf(got.targets)), []string{"dynamic"}) {
		t.Fatalf("restore_data = %v", res["restore_data"])
	}
	token := dbus.MakeVariant(encodeRestore(got))

	r2, f2 := castRig(t)
	f2.dynamic.Store(true)
	p2 := serveSharePicker(t, r2, "/screen:DP-1")
	r2.castSession(t, Vardict{"restore_data": token, "persist_mode": dbus.MakeVariant(uint32(2))})
	if code, _ := r2.castStart(t); code != ResponseSuccess || len(p2.asked.Load()) != 0 || !slices.Equal(f2.started.Load(), []string{"dynamic"}) {
		t.Errorf("replaying the cast target = %d, asked %v, started %v", code, p2.asked.Load(), f2.started.Load())
	}
}

// Where the compositor offers no cast target, the picker does not offer
// it, and a reply naming it anyway is refused without a stream.
func TestScreenCastRefusesTheCastTargetNotOffered(t *testing.T) {
	r, f := castRig(t)
	p := serveSharePicker(t, r, "/dynamic")
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseOther || len(f.started.Load()) != 0 {
		t.Errorf("an unoffered cast target = %d, started %v", code, f.started.Load())
	}
	want := sharepicker.Request{Sources: sharepicker.SourceOutputs | sharepicker.SourceRegions}
	if asked := p.asked.Load(); len(asked) != 1 || asked[0] != want {
		t.Errorf("picker asked %v, want %+v", asked, want)
	}
}

// A window-only request never offers the cast target, which streams as
// a monitor.
func TestPickerSourcesOfferTheCastTargetWithMonitorsOnly(t *testing.T) {
	if got := pickerSources(sourceWindow, true); got != sharepicker.SourceWindows {
		t.Errorf("a window request offers %#x", got)
	}
	if got := pickerSources(sourceMonitor, true); got&sharepicker.SourceDynamic == 0 {
		t.Errorf("a monitor request on a compositor with a cast target offers %#x", got)
	}
}
