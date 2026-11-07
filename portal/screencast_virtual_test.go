package portal

import (
	"errors"
	"slices"
	"strconv"
	"testing"

	"github.com/stubbedev/lys-lib/dbustest"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// fakeVirtual is lys-wm's virtual outputs: offered says whether it makes
// them, created and removed record what the portal asked.
type fakeVirtual struct {
	offered dbustest.Var[bool]
	created dbustest.Var[[]virtualSize]
	removed dbustest.Var[[]string]
}

func (f *fakeVirtual) offeredNow() (bool, error) { return f.offered.Load(), nil }

func (f *fakeVirtual) create(size virtualSize) (string, error) {
	var name string
	f.created.Update(func(c []virtualSize) []virtualSize {
		name = "VIRT-" + strconv.Itoa(len(c)+1)
		return append(c, size)
	})
	return name, nil
}

func (f *fakeVirtual) remove(name string) error {
	f.removed.Update(func(r []string) []string { return append(r, name) })
	return nil
}

// virtualRig is a cast rig whose lys-wm makes virtual outputs, with the
// share picker and the dialogs served; yes is the user's answer.
func virtualRig(t *testing.T, yes bool, fail ...string) (*rig, *fakeStarts, *fakeSharePicker, *fakeDialogs) {
	t.Helper()
	r, f := castRig(t, fail...)
	f.virtual.offered.Store(true)
	return r, f, serveSharePicker(t, r, "/screen:DP-1"), serveDialogs(t, r, yes)
}

func TestScreenCastOffersVirtualOnlyWhereLysWMMakesOutputs(t *testing.T) {
	r, f := castRig(t)
	if v, err := r.obj.GetProperty(ScreenCastIface + ".AvailableSourceTypes"); err != nil || v.Value() != uint32(3) {
		t.Fatalf("without virtual outputs AvailableSourceTypes = %v, %v", v, err)
	}
	if code, _ := r.interactive(t, ScreenCastIface+".CreateSession", handle, sessA, "org.app", Vardict{}); code != ResponseSuccess {
		t.Fatal("CreateSession failed")
	}
	if code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", Vardict{"types": dbus.MakeVariant(sourceVirtual)}); code != ResponseOther {
		t.Errorf("SelectSources of a virtual source lys-wm cannot make = %d, want other", code)
	}
	f.virtual.offered.Store(true)
	if v, err := r.obj.GetProperty(ScreenCastIface + ".AvailableSourceTypes"); err != nil || v.Value() != uint32(7) {
		t.Fatalf("with virtual outputs AvailableSourceTypes = %v, %v", v, err)
	}
	if code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", Vardict{"types": dbus.MakeVariant(sourceVirtual)}); code != ResponseSuccess {
		t.Errorf("SelectSources of a virtual source = %d", code)
	}
}

// A request for a virtual source alone asks the user in a consent
// dialog, never the share picker, then streams a new output of the
// default size as source type virtual; its token keeps that size alone,
// and closing the session removes the output.
func TestScreenCastVirtualStreamsANewOutputUntilTheSessionCloses(t *testing.T) {
	r, f, p, d := virtualRig(t, true)
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceVirtual), "persist_mode": dbus.MakeVariant(uint32(2))})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if asked := d.asked.Load(); len(asked) != 1 || asked[0].(portaldialogs.AccessRequest).Title != virtualConsent.Title {
		t.Errorf("dialogs asked %v, want the virtual display consent", asked)
	}
	if asked := p.asked.Load(); len(asked) != 0 {
		t.Errorf("the share picker was asked %v for a virtual source", asked)
	}
	if created := f.virtual.created.Load(); !slices.Equal(created, []virtualSize{defaultVirtual}) {
		t.Errorf("created %v, want one %v output", created, defaultVirtual)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:VIRT-1"}) {
		t.Errorf("started %v, want the new output", got)
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || streams[0].Props["source_type"].Value() != sourceVirtual {
		t.Fatalf("streams = %+v", streams)
	}
	g, ok := decodeRestore(res["restore_data"])
	if !ok || g.virtual == nil || *g.virtual != defaultVirtual || len(g.targets) != 0 {
		t.Errorf("restore_data = %v, want the virtual size alone", res["restore_data"])
	}
	if removed := f.virtual.removed.Load(); len(removed) != 0 {
		t.Fatalf("the output was removed while streaming: %v", removed)
	}
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if !f.streams.Load()[0].closed.Load() {
		t.Error("the stream outlived its session")
	}
	if removed := f.virtual.removed.Load(); !slices.Equal(removed, []string{"VIRT-1"}) {
		t.Errorf("removed %v after the session closed, want VIRT-1", removed)
	}
}

func TestScreenCastVirtualDeniedMakesNoOutput(t *testing.T) {
	r, f, _, d := virtualRig(t, false)
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceVirtual)})
	if code, _ := r.castStart(t); code != ResponseCancelled {
		t.Fatalf("a denied consent Start = %d, want cancelled", code)
	}
	if len(d.asked.Load()) != 1 {
		t.Fatal("no consent was asked")
	}
	if created, started := f.virtual.created.Load(), f.started.Load(); len(created) != 0 || len(started) != 0 {
		t.Errorf("a denied consent created %v and started %v", created, started)
	}
}

// A virtual source asked beside monitors and windows has no card in the
// share picker: the picker offers only those, and no output is made.
func TestScreenCastVirtualBesideOtherTypesHasNoPickerCard(t *testing.T) {
	r, f, p, d := virtualRig(t, true)
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceMonitor | sourceWindow | sourceVirtual)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	want := sharepicker.Request{Sources: sharepicker.SourceOutputs | sharepicker.SourceRegions | sharepicker.SourceWindows}
	if asked := p.asked.Load(); len(asked) != 1 || asked[0] != want {
		t.Errorf("picker asked %v, want %+v", asked, want)
	}
	if len(d.asked.Load()) != 0 || len(f.virtual.created.Load()) != 0 || !slices.Equal(f.started.Load(), []string{"screen:DP-1"}) {
		t.Errorf("consent %v, created %v, started %v", d.asked.Load(), f.virtual.created.Load(), f.started.Load())
	}
}

// An output removed elsewhere ends its stream, which ends the session
// as any lost source does.
func TestScreenCastVirtualOutputRemovedElsewhereClosesTheSession(t *testing.T) {
	r, f, _, _ := virtualRig(t, true)
	if err := r.client.AddMatchSignal(dbus.WithMatchInterface(SessionIface)); err != nil {
		t.Fatal(err)
	}
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceVirtual)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	f.streams.Load()[0].vanish()
	r.waitClosed(t, sessA)
	if _, live := r.backend.sessions.get(sessA); live {
		t.Error("the session outlived its virtual output")
	}
}

// A token replays a virtual output of the size it kept without asking;
// a virtual output that will not stream is removed again.
func TestScreenCastVirtualReplaysItsSizeAndRemovesAnOutputThatFails(t *testing.T) {
	r, f, p, d := virtualRig(t, true)
	kept := virtualSize{2560, 1440}
	token := dbus.MakeVariant(encodeRestore(restoreGrant{virtual: &kept}))
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceVirtual), "restore_data": token, "persist_mode": dbus.MakeVariant(uint32(2))})
	code, res := r.castStart(t)
	if code != ResponseSuccess || len(d.asked.Load()) != 0 || len(p.asked.Load()) != 0 {
		t.Fatalf("Start = %d, consent %v, picker %v: a token replays without asking", code, d.asked.Load(), p.asked.Load())
	}
	if created := f.virtual.created.Load(); !slices.Equal(created, []virtualSize{kept}) {
		t.Errorf("created %v, want the kept %v", created, kept)
	}
	if g, ok := decodeRestore(res["restore_data"]); !ok || g.virtual == nil || *g.virtual != kept {
		t.Errorf("reissued %v", res["restore_data"])
	}

	r2, f2, _, _ := virtualRig(t, true, "screen:VIRT-1")
	r2.castSession(t, Vardict{"types": dbus.MakeVariant(sourceVirtual)})
	if code, _ := r2.castStart(t); code != ResponseOther {
		t.Fatalf("a virtual output that does not stream: Start = %d, want other", code)
	}
	if created, removed := f2.virtual.created.Load(), f2.virtual.removed.Load(); len(created) != 1 || !slices.Equal(removed, []string{"VIRT-1"}) {
		t.Errorf("created %v, removed %v: want the failed output removed", created, removed)
	}
}

// A virtual token for a session that does not take virtual sources is
// none: the picker asks.
func TestScreenCastVirtualTokenNeedsTheVirtualType(t *testing.T) {
	r, f, p, _ := virtualRig(t, true)
	token := dbus.MakeVariant(encodeRestore(restoreGrant{virtual: &virtualSize{800, 600}}))
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceMonitor), "restore_data": token, "multiple": dbus.MakeVariant(true)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if len(p.asked.Load()) != 1 || len(f.virtual.created.Load()) != 0 {
		t.Errorf("picker asked %d times, created %v", len(p.asked.Load()), f.virtual.created.Load())
	}
}

func TestVirtualTokensKeepOnlyASize(t *testing.T) {
	size := virtualSize{1280, 800}
	tok := encodeRestore(restoreGrant{virtual: &size})
	if tok.Version != restoreV3 {
		t.Fatalf("token version %d", tok.Version)
	}
	g, ok := decodeRestore(onWire(t, tok))
	if !ok || g.virtual == nil || *g.virtual != size || len(g.targets) != 0 || g.devices != 0 {
		t.Fatalf("decoded %+v %v", g, ok)
	}
	v2 := restoreToken{restoreVendor, restoreV2, dbus.MakeVariant(tokenGrant{Targets: []tokenTarget{{Kind: sharepicker.TargetOutput, Name: "DP-1"}}})}
	if g, ok := decodeRestore(onWire(t, v2)); !ok || g.virtual != nil || !slices.Equal(payloads(pickedOf(g.targets)), []string{"screen:DP-1"}) {
		t.Errorf("a version 2 token decoded to %+v %v", g, ok)
	}
	for name, grant := range map[string]tokenGrantV3{
		"empty size":  {Virtual: []tokenSize{{0, 600}}},
		"two outputs": {Virtual: []tokenSize{{800, 600}, {800, 600}}},
		"bad target":  {Targets: []tokenTarget{{Kind: 7, Name: "x"}}},
	} {
		if g, ok := decodeRestore(onWire(t, restoreToken{restoreVendor, restoreV3, dbus.MakeVariant(grant)})); ok {
			t.Errorf("%s decoded: %+v", name, g)
		}
	}
}

func TestAvailableSourcesSurviveAFailedProbe(t *testing.T) {
	c := &screenCast{virtualOffered: func() (bool, error) { return false, errors.New("lys-wm: read the createoutput answer: EOF") }}
	if got := c.availableSources(); got != pickedSources {
		t.Fatalf("a failed probe offers %#x", got)
	}
}
