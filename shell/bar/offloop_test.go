package bar

import (
	"bytes"
	"context"
	"errors"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-ui/app"
	"github.com/stubbedev/lys-ui/widget"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/appicons"
	"github.com/stubbedev/lys-shell/internal/apptest"
	"github.com/stubbedev/lys-shell/service/brightness"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-shell/service/network"
	"github.com/stubbedev/lys-shell/service/powerprofiles"
	"github.com/stubbedev/lys-shell/service/sni"
	"github.com/stubbedev/lys-shell/service/upower"
	"github.com/stubbedev/lys-shell/shell/treetest"
)

type heldRead struct {
	held    atomic.Bool
	starts  atomic.Int32
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newHeldRead(t *testing.T) *heldRead {
	t.Helper()
	h := &heldRead{started: make(chan struct{}, 64), release: make(chan struct{})}
	t.Cleanup(h.free)
	return h
}

func (h *heldRead) free() { h.once.Do(func() { close(h.release) }) }

func (h *heldRead) wait() {
	if !h.held.Load() {
		return
	}
	h.starts.Add(1)
	h.started <- struct{}{}
	<-h.release
}

func TestOffLoopReadRunsOneAndCoalesces(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	h := newHeldRead(t)
	h.held.Store(true)
	var source atomic.Int32
	var applied []int32
	reads := newOffLoopRead(ctx, "probe", func(context.Context) (int32, error) {
		h.wait()
		return source.Load(), nil
	}, func(v int32) { applied = append(applied, v) })
	reads.request()
	apptest.Answer(t, "the first read started", h.started)
	onHeadlessLoop(func() bool { return true })
	for i := range int32(5) {
		source.Store(i + 1)
		reads.request()
	}
	if n := h.starts.Load(); n != 1 {
		t.Fatalf("%d reads run during a blocked one, want it alone", n)
	}
	h.free()
	apptest.Answer(t, "the read the requests asked for", h.started)
	waitHeadless(t, "both reads applied", func() bool { return len(applied) == 2 })
	apptest.Never(t, "a read nothing asked for", h.started)
	if got := onHeadlessLoop(func() []int32 { return slices.Clone(applied) }); got[1] != 5 {
		t.Fatalf("applied %v, want the last value last", got)
	}
}

func TestOffLoopReadWaitsForTheLoop(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	var reads atomic.Int32
	finished := make(chan struct{}, 8)
	var applied atomic.Int32
	r := newOffLoopRead(ctx, "probe", func(context.Context) (int32, error) {
		defer func() { finished <- struct{}{} }()
		return reads.Add(1), nil
	}, func(int32) { applied.Add(1) })
	headlessLoop.Lock()
	unlock := sync.OnceFunc(headlessLoop.Unlock)
	t.Cleanup(unlock)
	r.request()
	apptest.Answer(t, "the read finished", finished)
	r.request()
	r.request()
	apptest.Never(t, "a second read with the first not applied", finished)
	unlock()
	apptest.Answer(t, "the read after the apply", finished)
	waitHeadless(t, "two applies", func() bool { return applied.Load() == 2 })
}

func TestOffLoopReadLogsAFailureAndGoesOn(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	ctx := newTestContext(t, config.Defaults())
	var fail atomic.Bool
	fail.Store(true)
	logged := make(chan struct{}, 2)
	applied := 0
	r := newOffLoopRead(ctx, "probe", func(context.Context) (int, error) {
		defer func() { logged <- struct{}{} }()
		if fail.Load() {
			return 0, errors.New("daemon gone")
		}
		return 1, nil
	}, func(int) { applied++ })
	r.request()
	apptest.Answer(t, "the failing read", logged)
	waitHeadless(t, "the failure logged", func() bool { return strings.Contains(buf.String(), "probe: refresh: daemon gone") })
	if onHeadlessLoop(func() int { return applied }) != 0 {
		t.Fatal("a failed read applied")
	}
	fail.Store(false)
	r.request()
	apptest.Answer(t, "the next read", logged)
	waitHeadless(t, "the next read applied", func() bool { return applied == 1 })
}

func TestOffLoopReadEndsWithItsGeneration(t *testing.T) {
	ctx := newTestContext(t, config.Defaults())
	h := newHeldRead(t)
	h.held.Store(true)
	applied := make(chan int, 1)
	r := newOffLoopRead(ctx, "probe", func(context.Context) (int, error) {
		h.wait()
		return 1, nil
	}, func(v int) { applied <- v })
	r.request()
	apptest.Answer(t, "the read started", h.started)
	onHeadlessLoop(func() bool { ctx.gen.retire(); return true })
	h.free()
	apptest.Never(t, "an apply after the generation retired", applied)
}

func loopContext(t *testing.T) (ModuleContext, *app.Application) {
	t.Helper()
	a := apptest.Run(t)
	ctx := newTestContext(t, config.Defaults())
	ctx.App = a
	return ctx, a
}

func TestHeadlessFollowCoalescesOverAStalledLoop(t *testing.T) {
	ctx, a := loopContext(t)
	stalled, release := make(chan struct{}), make(chan struct{})
	free := sync.OnceFunc(func() { close(release) })
	t.Cleanup(free)
	a.Invoke(func() {
		close(stalled)
		<-release
	})
	apptest.Answer(t, "the loop stalled", stalled)
	values := make(chan int)
	calls := make(chan int, 8)
	drained := make(chan struct{})
	follow(ctx, values, func() { close(drained) }, func(v int) { calls <- v })
	for i := 1; i <= 5; i++ {
		values <- i
	}
	close(values)
	apptest.Answer(t, "the feed drained", drained)
	free()
	if v := apptest.Answer(t, "the call after the stall", calls); v != 5 {
		t.Fatalf("the call got %d, want the latest value", v)
	}
	apptest.Never(t, "a second call for the coalesced ticks", calls)
}

type heldNetwork struct {
	*fakeNetworkSource
	h *heldRead
}

func (s heldNetwork) Read(ctx context.Context) (network.Snapshot, error) {
	s.h.wait()
	return s.fakeNetworkSource.Read(ctx)
}

type heldPower struct {
	*fakePPSource
	h *heldRead
}

func (s heldPower) Read(ctx context.Context) (powerprofiles.Snapshot, error) {
	s.h.wait()
	return s.fakePPSource.Read(ctx)
}

func (s heldPower) SetActive(ctx context.Context, profile string) error {
	s.h.wait()
	return s.fakePPSource.SetActive(ctx, profile)
}

type heldBrightness struct {
	*fakeBrightnessSource
	h *heldRead
}

func (s heldBrightness) Devices(ctx context.Context) ([]brightness.Device, error) {
	s.h.wait()
	return s.fakeBrightnessSource.Devices(ctx)
}

type heldBattery struct {
	*fakeBattery
	h *heldRead
}

func (s heldBattery) Read(ctx context.Context) (upower.Device, error) {
	s.h.wait()
	return s.fakeBattery.Read(ctx)
}

type heldCws struct {
	*fakeCws
	h     *heldRead
	ticks chan struct{}
}

func (s heldCws) snapshot() ([]cwsWorkspace, []cwsWindow, error) {
	s.h.wait()
	return s.fakeCws.snapshot()
}

func (s heldCws) subscribe() (<-chan struct{}, func(), error) { return s.ticks, func() {}, nil }

func TestHeadlessLoopRunsWhileAReadIsBlocked(t *testing.T) {
	ctx, a := loopContext(t)
	for _, tc := range []struct {
		name   string
		create func(ctx ModuleContext, h *heldRead, ticks chan struct{}) (Module, error)
	}{
		{name: "network", create: func(ctx ModuleContext, h *heldRead, ticks chan struct{}) (Module, error) {
			ctx.Network = heldNetwork{&fakeNetworkSource{ticks: ticks}, h}
			return Create("network", ctx)
		}},
		{name: "power-profiles", create: func(ctx ModuleContext, h *heldRead, ticks chan struct{}) (Module, error) {
			ctx.PowerProfiles = heldPower{&fakePPSource{ticks: ticks}, h}
			return Create("power-profiles", ctx)
		}},
		{name: "brightness", create: func(ctx ModuleContext, h *heldRead, ticks chan struct{}) (Module, error) {
			ctx.Brightness = heldBrightness{&fakeBrightnessSource{ticks: ticks}, h}
			return Create("brightness", ctx)
		}},
		{name: "battery", create: func(ctx ModuleContext, h *heldRead, ticks chan struct{}) (Module, error) {
			f := newFakeBattery(upower.Device{})
			f.ticks = ticks
			ctx.Battery = heldBattery{f, h}
			return Create("battery", ctx)
		}},
		{name: "workspaces", create: func(ctx ModuleContext, h *heldRead, ticks chan struct{}) (Module, error) {
			return newCwsModule(ctx, "niri", cwsWorkspaces, config.DefaultsNiriWorkspaces().View(), heldCws{newFakeCws(nil, nil), h, ticks})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHeldRead(t)
			ticks := make(chan struct{})
			built := apptest.On(a, func() moduleResult {
				m, err := tc.create(ctx, h, ticks)
				return moduleResult{m, err}
			})
			if built.err != nil {
				t.Fatal(built.err)
			}
			h.held.Store(true)
			ticks <- struct{}{}
			apptest.Answer(t, "the read started", h.started)
			ran := make(chan struct{})
			a.Invoke(func() { close(ran) })
			apptest.Answer(t, "the loop ran during the read", ran)
			for range 5 {
				ticks <- struct{}{}
			}
			if n := h.starts.Load(); n != 1 {
				t.Fatalf("%d reads started during a blocked one", n)
			}
			h.free()
			apptest.Answer(t, "the read the ticks asked for", h.started)
		})
	}
}

func TestHeadlessLoopRunsWhileLysWMIsBlocked(t *testing.T) {
	ctx, a := loopContext(t)
	for _, tc := range []struct{ module, request, event string }{
		{"lys-wm-workspaces", "workspaces", `{"event":"workspace","output":"DP-1","workspace":3}`},
		{"window-title", "activewindow", `{"event":"focus","window":5}`},
	} {
		t.Run(tc.module, func(t *testing.T) {
			f := newFakeLysWM(t)
			if r := apptest.On(a, func() moduleResult { return createResult(tc.module, ctx) }); r.err != nil {
				t.Fatal(r.err)
			}
			f.AwaitWatch(t)
			free := f.Hold(tc.request)
			t.Cleanup(free)
			before := countRequests(f, tc.request)
			f.Events <- tc.event
			apptest.Until(t, a, "the read reached lys-wm", func() bool { return countRequests(f, tc.request) > before })
			ran := make(chan struct{})
			a.Invoke(func() { close(ran) })
			apptest.Answer(t, "the loop ran during the read", ran)
			free()
		})
	}
}

type moduleResult struct {
	module Module
	err    error
}

func createResult(name string, ctx ModuleContext) moduleResult {
	m, err := Create(name, ctx)
	return moduleResult{m, err}
}

func countRequests(f *lyswmtest.WM, request string) int {
	n := 0
	for _, line := range f.Requests() {
		if strings.Contains(line, `"request":"`+request+`"`) {
			n++
		}
	}
	return n
}

func lysWMTitleBar(t *testing.T, edit func(*config.Config)) *lyswmtest.WM {
	t.Helper()
	ctx, a := loopContext(t)
	ctx.Config = config.Defaults()
	edit(ctx.Config)
	ctx.Connector = "DP-1"
	f := newFakeLysWM(t)
	f.Result("workspaces", lysWMTwoOutputs)
	f.Result("windows", lysWMWindows)
	if r := apptest.On(a, func() moduleResult { return createResult("lys-wm-workspaces", ctx) }); r.err != nil {
		t.Fatal(r.err)
	}
	f.AwaitWatch(t)
	return f
}

func awaitRequest(t *testing.T, f *lyswmtest.WM, request string, before int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); countRequests(f, request) == before; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("lys-wm never heard another %s request", request)
		}
	}
}

func neverRequests(t *testing.T, f *lyswmtest.WM, what string) {
	t.Helper()
	before := f.Requests()
	time.Sleep(300 * time.Millisecond)
	if got := f.Requests(); !slices.Equal(got, before) {
		t.Fatalf("%s: lys-wm heard %q", what, got[len(before):])
	}
}

func TestHeadlessLysWMTitleWithoutAppIconsMakesNoRequest(t *testing.T) {
	f := lysWMTitleBar(t, func(*config.Config) {})
	f.Events <- `{"event":"title","window":4,"title":"elsewhere"}`
	neverRequests(t, f, "a request for a title change")
	before := countRequests(f, "workspaces")
	f.Events <- `{"event":"workspace","output":"DP-1","workspace":2}`
	awaitRequest(t, f, "workspaces", before)
}

func TestHeadlessLysWMTitleWithPlainAppIconsMakesNoRequest(t *testing.T) {
	f := lysWMTitleBar(t, func(c *config.Config) { c.LysWMWorkspaces.AppIconsShow = true })
	f.Events <- `{"event":"title","window":4,"title":"elsewhere"}`
	neverRequests(t, f, "a request for a title change")
}

func TestHeadlessLysWMTitleOnlyOfAShownWindowReads(t *testing.T) {
	f := lysWMTitleBar(t, func(c *config.Config) {
		c.LysWMWorkspaces.AppIconsShow = true
		c.LysWMWorkspaces.AppIconMap = map[string]string{"title:*secret*": "ld-lock-symbolic"}
	})
	f.Events <- `{"event":"title","window":9,"title":"on the other output"}`
	neverRequests(t, f, "a request for the title of a window the bar does not show")
	before := countRequests(f, "windows")
	f.Events <- `{"event":"title","window":4,"title":"secret"}`
	awaitRequest(t, f, "windows", before)
}

func TestTrayLookComparesPixmapBytes(t *testing.T) {
	look := func(b byte) trayLook {
		return trayLook{icon: trayIconSource{kind: trayIconPixmap, pixmap: sni.Pixmap{Width: 1, Height: 1, Data: []byte{b, 0, 0, 0}}}}
	}
	if !look(1).same(look(1)) {
		t.Error("equal pixmaps read as different looks")
	}
	if look(1).same(look(2)) {
		t.Error("different pixmaps of one size read as the same look")
	}
}

func cwsCostWorkspaces() []cwsWorkspace {
	urgent := occupiedWS(1, 1, "DP-1")
	urgent.urgent = true
	all := []cwsWorkspace{urgent}
	for i := 2; i <= 8; i++ {
		all = append(all, occupiedWS(uint64(i), i, "DP-1"))
	}
	return all
}

func TestCwsEqualModelsAllocateLittle(t *testing.T) {
	cfg := config.DefaultsSwayWorkspaces().View()
	cfg.AppIconsShow = true
	windows := []cwsWindow{{id: 1, workspace: 1, hasWorkspace: true}}
	f := newFakeCws(cwsCostWorkspaces(), windows)
	m := newTestCws(t, cfg, f)
	icon := func() widget.Widget { return treetest.WithClass(m.segs, "workspace-icon")[0] }
	shown, models := icon(), &m.models[0]
	f.windows = []cwsWindow{{id: 1, workspace: 1, hasWorkspace: true, app: appicons.Window{Title: "another", HasTitle: true}}}
	got := testing.AllocsPerRun(100, func() {
		if err := renderOnce(m.read, m.apply); err != nil {
			t.Fatal(err)
		}
	})
	if icon() != shown || &m.models[0] != models {
		t.Error("a title no pattern reads changed what the segments show")
	}
	if got > 70 {
		t.Fatalf("an unchanged snapshot allocates %.0f times, want at most 70", got)
	}
}

func TestHeadlessHyprlandLoopRunsWhileAReadIsBlocked(t *testing.T) {
	ctx, a := loopContext(t)
	f, conn := startFakeHypr(t, hyprReplies())
	ctx.Hyprland = conn
	ctx.Connector = "DP-1"
	built := apptest.On(a, func() moduleResult { return createResult("hyprland-workspaces", ctx) })
	if built.err != nil {
		t.Fatal(built.err)
	}
	m := built.module.(*hyprlandWorkspaces)
	free := f.hold(t, "j/workspaces")
	f.set("j/workspaces", `[{"id":1,"name":"1","monitor":"DP-1","windows":2},{"id":7,"name":"7","monitor":"DP-1","windows":1}]`)
	f.emit(t, "createworkspacev2>>7,7")
	apptest.Answer(t, "the read reached Hyprland", f.arrived)
	ran := make(chan struct{})
	a.Invoke(func() { close(ran) })
	apptest.Answer(t, "the loop ran during the read", ran)
	free()
	apptest.Until(t, a, "the event's workspace shown", func() bool {
		return slices.ContainsFunc(m.models, func(b cwsButtonModel) bool { return b.ws.id == 7 })
	})
}

func TestWorkspacesEndOfStreamLeavesTheBlinkToTheLoop(t *testing.T) {
	stopped, closed := 0, 0
	m := &cwsModule{
		blinkStop:  func() { stopped++ },
		stopEvents: func() { closed++ },
		backend:    closedCounter{closed: &closed},
	}
	m.closeIPC()
	if stopped != 0 || m.blinkStop == nil {
		t.Errorf("the event stream's goroutine stopped the blink timer %d times", stopped)
	}
	if closed != 2 {
		t.Errorf("closing the IPC closed %d of the events and the backend, want 2", closed)
	}
	m.Stop()
	if stopped != 1 || m.blinkStop != nil {
		t.Errorf("Stop stopped the blink timer %d times, want 1", stopped)
	}
	if closed != 2 {
		t.Errorf("Stop closed the IPC again: %d", closed)
	}
}

type closedCounter struct {
	cwsBackend
	closed *int
}

func (c closedCounter) close() { *c.closed++ }

func TestHeadlessRetiringAGenerationStopsTheWorkspacesBlink(t *testing.T) {
	ctx, a := loopContext(t)
	ctx.Connector = "DP-1"
	ctx.gen = newMountGen()
	f := newFakeLysWM(t)
	f.Result("workspaces", lysWMTwoOutputs)
	f.Result("windows", lysWMWindows)
	r := apptest.On(a, func() moduleResult { return createResult("lys-wm-workspaces", ctx) })
	if r.err != nil {
		t.Fatal(r.err)
	}
	m := r.module.(*cwsModule)
	if !apptest.On(a, func() bool { return m.blinkStop != nil }) {
		t.Fatal("an urgent window started no blink")
	}
	apptest.On(a, func() struct{} { ctx.gen.retire(); return struct{}{} })
	if apptest.On(a, func() bool { return m.blinkStop != nil }) {
		t.Error("the blink timer outlived its generation")
	}
}
