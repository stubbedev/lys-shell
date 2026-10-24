package portal

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stubbedev/lys-ui/capture"

	"github.com/stubbedev/lys-shell/internal/dbustest"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// fakeSharePicker is the shell's dev.stubbe.lys.SharePicker1.
type fakeSharePicker struct {
	gate
	reply dbustest.Var[string]
	asked dbustest.Var[[]any]
}

func (f *fakeSharePicker) Pick(c portalreq.Caller, r sharepicker.Request) (string, *dbus.Error) {
	f.asked.Update(func(a []any) []any { return append(a, r) })
	if f.enter(c) {
		return "", nil
	}
	return f.reply.Load(), nil
}

func (f *fakeSharePicker) Dismiss(handle string) *dbus.Error {
	f.gate.Dismiss(handle)
	return nil
}

func serveSharePicker(t *testing.T, r *rig, reply string) *fakeSharePicker {
	t.Helper()
	f := &fakeSharePicker{}
	f.reply.Store(reply)
	conn := r.bus.Conn(t)
	if err := conn.Export(f, sharepicker.ServicePath, sharepicker.ServiceName); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(sharepicker.ServiceName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	return f
}

// fakeStream is a started stream; vanish ends it as a closed window
// would.
type fakeStream struct {
	node   uint32
	closed dbustest.Var[bool]
	ended  chan struct{}
}

func (s *fakeStream) NodeID() uint32         { return s.node }
func (s *fakeStream) Size() (int32, int32)   { return 1920, 1080 }
func (s *fakeStream) Ended() <-chan struct{} { return s.ended }
func (s *fakeStream) Close()                 { s.closed.Store(true) }
func (s *fakeStream) vanish()                { close(s.ended) }

// fakeStarts records stream starts; fail names payloads that will not
// start. The outputs are a 2x-scaled DP-1 left of a 1x DP-2; noOutputs
// makes their geometry unavailable.
type fakeStarts struct {
	started   dbustest.Var[[]string]
	streams   dbustest.Var[[]*fakeStream]
	fail      map[string]bool
	noOutputs dbustest.Var[bool]
}

func (f *fakeStarts) outputs() ([]outputGeometry, error) {
	if f.noOutputs.Load() {
		return nil, errors.New("no compositor")
	}
	return []outputGeometry{
		{name: "DP-1", x: 0, y: 0, width: 1280, height: 720},
		{name: "DP-2", x: 1280, y: 0, width: 1920, height: 1080},
	}, nil
}

func (f *fakeStarts) start(t captureTarget, cursor bool, fps uint32) (screenStream, error) {
	f.started.Update(func(s []string) []string {
		return append(s, t.payload()+map[bool]string{true: "+cursor", false: ""}[cursor])
	})
	if f.fail[t.payload()] {
		return nil, errors.New("gone")
	}
	s := &fakeStream{node: uint32(40 + len(f.streams.Load())), ended: make(chan struct{})}
	f.streams.Update(func(all []*fakeStream) []*fakeStream { return append(all, s) })
	_ = fps
	return s, nil
}

func castRig(t *testing.T, fail ...string) (*rig, *fakeStarts) {
	f := &fakeStarts{fail: map[string]bool{}}
	for _, p := range fail {
		f.fail[p] = true
	}
	r := newRig(t, func(b *Backend) {
		b.screenCast = newScreenCast(b.conn, b.sessions, f.start, f.outputs)
	})
	return r, f
}

func (r *rig) castSession(t *testing.T, options Vardict) {
	t.Helper()
	if code, _ := r.interactive(t, ScreenCastIface+".CreateSession", handle, sessA, "org.app", Vardict{}); code != ResponseSuccess {
		t.Fatalf("CreateSession = %d", code)
	}
	if code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", options); code != ResponseSuccess {
		t.Fatalf("SelectSources = %d", code)
	}
}

func (r *rig) castStart(t *testing.T) (uint32, map[string]dbus.Variant) {
	t.Helper()
	return r.interactive(t, ScreenCastIface+".Start", handle, sessA, "org.app", "", Vardict{})
}

// castStreams reads Start's streams.
func castStreams(t *testing.T, res map[string]dbus.Variant) []castStream {
	t.Helper()
	var streams []castStream
	if err := dbus.Store([]any{res["streams"].Value()}, &streams); err != nil {
		t.Fatal(err)
	}
	return streams
}

// pair reads an (ii) stream property.
func pair(v dbus.Variant) [2]int32 {
	p, _ := v.Value().([]any)
	if len(p) != 2 {
		return [2]int32{-1, -1}
	}
	a, _ := p[0].(int32)
	b, _ := p[1].(int32)
	return [2]int32{a, b}
}

func payloads(targets []captureTarget) []string {
	out := make([]string, len(targets))
	for i, t := range targets {
		out[i] = t.payload()
	}
	return out
}

// Neither virtual monitors nor cursor metadata are offered.
func TestScreenCastProperties(t *testing.T) {
	r, _ := castRig(t)
	for name, want := range map[string]uint32{"AvailableSourceTypes": 3, "AvailableCursorModes": 3, "version": 4} {
		if v, err := r.obj.GetProperty(ScreenCastIface + "." + name); err != nil || v.Value() != want {
			t.Errorf("%s = %v, %v", name, v, err)
		}
	}
}

// With no types and no cursor mode the picker offers monitors (outputs
// and regions) and the pointer is hidden, as the spec defaults them; a
// monitor stream carries its logical position and size.
func TestScreenCastPicksAndStreams(t *testing.T) {
	r, f := castRig(t)
	p := serveSharePicker(t, r, "r/screen:DP-1")
	r.castSession(t, Vardict{"persist_mode": dbus.MakeVariant(uint32(2))})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:DP-1"}) {
		t.Errorf("started %v, want the pointer hidden", got)
	}
	want := sharepicker.Request{Sources: sharepicker.SourceOutputs | sharepicker.SourceRegions, AllowToken: true}
	if asked := p.asked.Load(); len(asked) != 1 || asked[0] != want {
		t.Errorf("picker asked %v, want %+v", asked, want)
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || streams[0].Node != 40 || streams[0].Props["source_type"].Value() != sourceMonitor {
		t.Fatalf("streams = %+v", streams)
	}
	if pos, size := pair(streams[0].Props["position"]), pair(streams[0].Props["size"]); pos != [2]int32{0, 0} || size != [2]int32{1280, 720} {
		t.Errorf("position %v size %v, want the 2x output's logical (0,0) 1280x720", pos, size)
	}
	if res["persist_mode"].Value() != uint32(2) {
		t.Errorf("persist_mode = %v", res["persist_mode"])
	}
	if got, ok := decodeRestore(res["restore_data"]); !ok || !slices.Equal(payloads(got), []string{"screen:DP-1"}) {
		t.Errorf("restore_data = %v", res["restore_data"])
	}
	if !r.sessionRuns(sessA, 40) {
		t.Error("the session does not list its stream")
	}

	// Closing the session stops its streams.
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if !f.streams.Load()[0].closed.Load() {
		t.Error("the stream outlived its session")
	}
}

// sessionRuns reports whether the live session at path runs node.
func (r *rig) sessionRuns(path dbus.ObjectPath, node uint32) bool {
	s, live := r.backend.sessions.get(path)
	if !live {
		return false
	}
	_, ok := r.backend.screenCast.stream(s, node)
	return ok
}

// A region sits at its output's position plus its offset; a window's
// place is not known, so it carries neither; without output geometry
// the streams still start, without positions.
func TestScreenCastStreamGeometry(t *testing.T) {
	r, _ := castRig(t)
	serveSharePicker(t, r, "/region:DP-2@10,20,640,480;window:w1")
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceMonitor | sourceWindow), "multiple": dbus.MakeVariant(true)})
	code, res := r.castStart(t)
	streams := castStreams(t, res)
	if code != ResponseSuccess || len(streams) != 2 {
		t.Fatalf("Start = %d, %+v", code, streams)
	}
	region, window := streams[0].Props, streams[1].Props
	if region["source_type"].Value() != sourceMonitor || pair(region["position"]) != [2]int32{1290, 20} || pair(region["size"]) != [2]int32{640, 480} {
		t.Errorf("region props %v", region)
	}
	if _, has := window["position"]; has || window["source_type"].Value() != sourceWindow {
		t.Errorf("window props %v", window)
	}
	if _, has := window["size"]; has {
		t.Errorf("a window claims a logical size: %v", window)
	}
	r2, f2 := castRig(t)
	f2.noOutputs.Store(true)
	serveSharePicker(t, r2, "/screen:DP-1")
	r2.castSession(t, Vardict{})
	code, res = r2.castStart(t)
	if streams := castStreams(t, res); code != ResponseSuccess || len(streams) != 1 {
		t.Fatalf("without geometry Start = %d, %+v", code, streams)
	} else if _, has := streams[0].Props["position"]; has {
		t.Errorf("a position without geometry: %v", streams[0].Props)
	}
}

// A type or cursor mode not offered fails SelectSources; a picker reply
// of a type not asked for fails Start.
func TestScreenCastRefusesWhatIsNotOffered(t *testing.T) {
	r, f := castRig(t)
	if code, _ := r.interactive(t, ScreenCastIface+".CreateSession", handle, sessA, "org.app", Vardict{}); code != ResponseSuccess {
		t.Fatal("CreateSession failed")
	}
	for _, options := range []Vardict{
		{"types": dbus.MakeVariant(uint32(4))},
		{"types": dbus.MakeVariant(uint32(0))},
		{"cursor_mode": dbus.MakeVariant(uint32(4))},
		{"cursor_mode": dbus.MakeVariant(cursorHidden | cursorEmbedded)},
	} {
		if code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", options); code != ResponseOther {
			t.Errorf("SelectSources %v = %d, want other", options, code)
		}
	}
	serveSharePicker(t, r, "/window:w1")
	if code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", Vardict{"types": dbus.MakeVariant(sourceMonitor)}); code != ResponseSuccess {
		t.Fatal("SelectSources failed")
	}
	if code, _ := r.castStart(t); code != ResponseOther || len(f.started.Load()) != 0 {
		t.Errorf("a window for a monitor-only request = %d, started %v", code, f.started.Load())
	}
}

func TestScreenCastWithoutATokenGrant(t *testing.T) {
	r, f := castRig(t)
	p := serveSharePicker(t, r, "/window:kitty-1")
	r.castSession(t, Vardict{"persist_mode": dbus.MakeVariant(uint32(1)), "cursor_mode": dbus.MakeVariant(cursorEmbedded), "types": dbus.MakeVariant(sourceWindow)})
	_, res := r.castStart(t)
	if _, ok := res["restore_data"]; ok {
		t.Error("a token the user did not grant")
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"window:kitty-1+cursor"}) {
		t.Errorf("started %v: an embedded cursor", got)
	}
	if asked := p.asked.Load(); len(asked) != 1 || asked[0].(sharepicker.Request).Sources != sharepicker.SourceWindows {
		t.Errorf("picker asked %v, want windows only", asked)
	}
}

// A token keeps every source of the session and replays them all
// without the picker.
func TestScreenCastReplaysARestoreToken(t *testing.T) {
	r, f := castRig(t)
	p := serveSharePicker(t, r, "/screen:HDMI-A-1")
	kept := []captureTarget{{kind: targetRegion, name: "DP-2", x: 5, y: 6, width: 640, height: 480}, {kind: targetWindow, name: "w1"}}
	token := dbus.MakeVariant(encodeRestore(kept))
	r.castSession(t, Vardict{"restore_data": token, "persist_mode": dbus.MakeVariant(uint32(2)), "types": dbus.MakeVariant(sourceMonitor | sourceWindow)})
	code, res := r.castStart(t)
	if code != ResponseSuccess || len(p.asked.Load()) != 0 {
		t.Fatalf("Start = %d, picker asked %v: a token replays without one", code, p.asked.Load())
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"region:DP-2@5,6,640,480", "window:w1"}) {
		t.Errorf("started %v", got)
	}
	if got, _ := decodeRestore(res["restore_data"]); !slices.Equal(payloads(got), payloads(kept)) {
		t.Errorf("reissued %v", res["restore_data"])
	}
	// The same token for a monitor-only request does not replay its
	// window: the picker asks instead.
	r2, f2 := castRig(t)
	p2 := serveSharePicker(t, r2, "/screen:DP-1")
	r2.castSession(t, Vardict{"restore_data": token})
	if code, _ := r2.castStart(t); code != ResponseSuccess || len(p2.asked.Load()) != 1 || !slices.Equal(f2.started.Load(), []string{"screen:DP-1"}) {
		t.Errorf("a token of another type = %d, asked %d, started %v", code, len(p2.asked.Load()), f2.started.Load())
	}
}

func TestScreenCastRepromptsAGoneRestoreTarget(t *testing.T) {
	r, f := castRig(t, "screen:DP-9")
	p := serveSharePicker(t, r, "/screen:DP-1")
	r.castSession(t, Vardict{"restore_data": dbus.MakeVariant(encodeRestore([]captureTarget{{kind: targetOutput, name: "DP-9"}}))})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if got := f.started.Load(); !slices.Equal(got, []string{"screen:DP-9", "screen:DP-1"}) || len(p.asked.Load()) != 1 {
		t.Errorf("started %v, picker asked %d times", got, len(p.asked.Load()))
	}
}

func TestScreenCastCancels(t *testing.T) {
	r, f := castRig(t)
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseCancelled {
		t.Errorf("no shell = %d, want cancelled", code)
	}
	serveSharePicker(t, r, "")
	if code, _ := r.castStart(t); code != ResponseCancelled || len(f.started.Load()) != 0 {
		t.Errorf("picker cancel = %d, started %v", code, f.started.Load())
	}
}

func TestScreenCastStartsAllOrNothing(t *testing.T) {
	r, f := castRig(t, "screen:HDMI-A-1")
	p := serveSharePicker(t, r, "/screen:DP-1;screen:HDMI-A-1")
	r.castSession(t, Vardict{"multiple": dbus.MakeVariant(true)})
	if code, _ := r.castStart(t); code != ResponseOther {
		t.Fatalf("Start = %d, want other", code)
	}
	if asked := p.asked.Load(); !asked[0].(sharepicker.Request).Multiple {
		t.Errorf("not multi-select: %v", asked)
	}
	if s := f.streams.Load(); len(s) != 1 || !s[0].closed.Load() {
		t.Error("the stream started before the failure kept running")
	}
	if r.sessionRuns(sessA, 40) {
		t.Error("the session lists a rolled-back stream")
	}
}

func TestParsePickerReply(t *testing.T) {
	token, targets, ok := parsePickerReply("r/screen:DP-1;window:firefox@instance-3;region:HDMI-A-1@10,-20,800,600")
	var got []string
	for _, t := range targets {
		got = append(got, t.payload())
	}
	if !ok || !token || !slices.Equal(got, []string{"screen:DP-1", "window:firefox@instance-3", "region:HDMI-A-1@10,-20,800,600"}) {
		t.Errorf("= %v %v %v", token, got, ok)
	}
	if targets[2].sourceType() != sourceMonitor || targets[1].sourceType() != sourceWindow || targets[0].sourceType() != sourceMonitor {
		t.Error("source types: a region is a monitor's")
	}
	for _, bad := range []string{"", "garbage", "/screen:", "/window:", "/region:DP-1@1,2,3", "/region:DP-1@1,2,0,5", "/region:@1,2,3,4", "/bogus:x", "/screen:DP-1;bogus"} {
		if _, _, ok := parsePickerReply(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestRestoreTokens(t *testing.T) {
	foreign := dbus.MakeVariant(restoreToken{"other-portal", 1, dbus.MakeVariant("screen:DP-1")})
	if _, ok := decodeRestore(foreign); ok {
		t.Error("a foreign token decoded")
	}
	if _, ok := decodeRestore(dbus.MakeVariant(uint32(42))); ok {
		t.Error("a non-structure decoded")
	}
}

func TestEffectiveFPS(t *testing.T) {
	for _, tc := range [][3]int64{{60, 0, 60}, {60, 144000, 60}, {60, 30000, 30}, {60, 500, 1}, {0, 0, 1}} {
		if got := effectiveFPS(uint32(tc[0]), int32(tc[1])); int64(got) != tc[2] {
			t.Errorf("effectiveFPS(%d, %d) = %d", tc[0], tc[1], got)
		}
	}
}

// waitClosed waits for the backend's Closed on path.
func (r *rig) waitClosed(t *testing.T, path dbus.ObjectPath) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case s := <-r.signals:
			if s.Name == SessionIface+".Closed" && s.Path == path {
				return
			}
		case <-deadline:
			t.Fatalf("no Closed for %s", path)
		}
	}
}

// A stream whose source vanishes ends its session: Closed goes out, the
// other streams stop and the session's state is gone.
func TestScreenCastSourceLossClosesTheSession(t *testing.T) {
	r, f := castRig(t)
	if err := r.client.AddMatchSignal(dbus.WithMatchInterface(SessionIface)); err != nil {
		t.Fatal(err)
	}
	serveSharePicker(t, r, "/screen:DP-1;window:w1")
	r.castSession(t, Vardict{"multiple": dbus.MakeVariant(true), "types": dbus.MakeVariant(sourceMonitor | sourceWindow)})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	streams := f.streams.Load()
	streams[1].vanish()
	r.waitClosed(t, sessA)
	if !streams[0].closed.Load() || !streams[1].closed.Load() {
		t.Error("streams outlived their session")
	}
	if _, live := r.backend.sessions.get(sessA); live {
		t.Error("the session stayed live")
	}
	if code, _ := r.castStart(t); code != ResponseOther {
		t.Errorf("Start on the closed session = %d, want other", code)
	}
}

// A stream the session stops on purpose ends nothing else.
func TestScreenCastClosingIsNotSourceLoss(t *testing.T) {
	r, _ := castRig(t)
	if err := r.client.AddMatchSignal(dbus.WithMatchInterface(SessionIface)); err != nil {
		t.Fatal(err)
	}
	serveSharePicker(t, r, "/screen:DP-1")
	r.castSession(t, Vardict{})
	if code, _ := r.castStart(t); code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-r.signals:
		if s.Name == SessionIface+".Closed" {
			t.Errorf("the app's own close was announced as a backend close: %v", s)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

// Sessions answer only to their own interface: ScreenCast.Start on a
// RemoteDesktop session, RemoteDesktop on a ScreenCast session, and
// either on an unknown or another app's session fail.
func TestSessionsRefuseOtherInterfaces(t *testing.T) {
	r, _ := castRig(t)
	if code, _ := r.interactive(t, RemoteDesktopIface+".CreateSession", handle, sessA, "org.app", Vardict{}); code != ResponseSuccess {
		t.Fatal("RemoteDesktop CreateSession failed")
	}
	if code, _ := r.interactive(t, ScreenCastIface+".CreateSession", handle, sessB, "org.app", Vardict{}); code != ResponseSuccess {
		t.Fatal("ScreenCast CreateSession failed")
	}
	for _, tc := range []struct {
		name string
		call func() uint32
	}{
		{"ScreenCast.Start on a RemoteDesktop session", func() uint32 {
			code, _ := r.interactive(t, ScreenCastIface+".Start", handle, sessA, "org.app", "", Vardict{})
			return code
		}},
		{"RemoteDesktop.SelectDevices on a ScreenCast session", func() uint32 {
			code, _ := r.interactive(t, RemoteDesktopIface+".SelectDevices", handle, sessB, "org.app", Vardict{})
			return code
		}},
		{"ScreenCast.SelectSources on an unknown session", func() uint32 {
			code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessC, "org.app", Vardict{})
			return code
		}},
		{"ScreenCast.SelectSources for another app", func() uint32 {
			code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessB, "org.other", Vardict{})
			return code
		}},
	} {
		if code := tc.call(); code != ResponseOther {
			t.Errorf("%s = %d, want other", tc.name, code)
		}
	}
	// A RemoteDesktop session may pick screens to share.
	if code, _ := r.interactive(t, ScreenCastIface+".SelectSources", handle, sessA, "org.app", Vardict{}); code != ResponseSuccess {
		t.Errorf("SelectSources on a RemoteDesktop session = %d", code)
	}
	if err := r.obj.Call(RemoteDesktopIface+".NotifyPointerMotion", 0, sessB, Vardict{}, 1.0, 1.0).Err; err == nil {
		t.Error("RemoteDesktop input on a ScreenCast session was accepted")
	}
}

// A frame of a new layout is held back and its size reported, so the
// producer renegotiates; frames of the new layout then fill. A failed
// capture fills nothing.
func TestSnapshotSourceFollowsAResize(t *testing.T) {
	frame := func(w, h, fill int) *capture.Frame {
		data := make([]byte, w*4*h)
		for i := range data {
			data[i] = byte(fill)
		}
		return &capture.Frame{Width: w, Height: h, Stride: w * 4, Data: data}
	}
	next, fail := frame(4, 2, 1), false
	src := newSnapshotSource(func([]byte) (*capture.Frame, error) {
		if fail {
			return nil, errors.New("copy failed")
		}
		return next, nil
	}, next, nil)
	if _, ok := src.Fill(make([]byte, 16*2)); !ok {
		t.Fatal("a frame of the stream's layout did not fill")
	}
	next = frame(8, 4, 2)
	if _, ok := src.Fill(make([]byte, 16*2)); ok {
		t.Error("a frame of a new layout filled the old buffer")
	}
	if w, h, stride := src.Size(); w != 8 || h != 4 || stride != 32 {
		t.Errorf("Size = %d x %d stride %d, want the new layout", w, h, stride)
	}
	dst := make([]byte, 32*4)
	if _, ok := src.Fill(dst); !ok || dst[0] != 2 {
		t.Errorf("after renegotiating, filled %v with %d", ok, dst[0])
	}
	fail = true
	if _, ok := src.Fill(dst); ok {
		t.Error("a failed capture filled")
	}
}
