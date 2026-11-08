package portal

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/stubbedev/lys-desktop/service/lyswm"
	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/dbusx"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
	"github.com/stubbedev/lys-lib/wmipc"

	"github.com/stubbedev/lys-shell/internal/drmconn"
	"github.com/stubbedev/lys-shell/shell/regionoverlay"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// ScreenCastIface is org.freedesktop.impl.portal.ScreenCast:
// SelectSources records the options, Start shows
// the shell's share picker (unless a restore token already names the
// source), starts a PipeWire producer per picked source and replies
// their node ids. The selection never leaves lys-shell, so it works on any
// compositor.
const ScreenCastIface = "org.freedesktop.impl.portal.ScreenCast"

// castState is a session's screen sharing: the options gathered across
// the calls and the streams Start runs. It lives on the session,
// guarded by its mu.
type castState struct {
	types, persistMode uint32
	cursorMode         cursorMode
	multiple           bool
	restore            []restoreTarget
	restoreVirtual     *virtualSize
	// restoreIssued is when the replayed grant was given, zero when
	// its token does not say.
	restoreIssued time.Time
	streams       []startedStream
}

// screenCast is the interface: its sessions' state lives on the
// session records.
type screenCast struct {
	conn     *dbus.Conn
	sessions *sessions
	// start runs one stream (screenStreams.start; a fake in tests).
	start func(target sharepicker.Target, cursor cursorMode, fps uint32) (screenStream, error)
	// outputs is the outputs' logical geometry (the Wayland loop's; a
	// fake in tests).
	outputs func() ([]outputGeometry, error)
	// focused is the focused output's connector (lys-wm's; a fake in
	// tests).
	focused func() (string, error)
	// windows are lys-wm's windows.
	windows func() ([]wmipc.Window, error)
	// toplevels are the compositor's windows with their identifiers
	// (listToplevels; a fake in tests).
	toplevels func() ([]capture.Toplevel, error)
	// displays are the monitors behind the connectors (readDisplays; a
	// fake in tests).
	displays func() (map[string]displayID, error)
	// virtualOffered, createVirtual and removeVirtual ask lys-wm whether
	// it makes virtual outputs, make one and remove it (a fake in tests).
	virtualOffered func() (bool, error)
	createVirtual  func(virtualSize) (string, error)
	removeVirtual  func(name string) error
	// dynamicOffered asks the compositor whether it offers its cast
	// target as a capture source (a fake in tests).
	dynamicOffered func() (bool, error)
	// reminder asks again about an old persistent grant.
	reminder castReminder
}

// startedStream is a running stream and what Start reported of it;
// stopped closes when the session stops it on purpose, so its end is
// not taken for the source vanishing.
type startedStream struct {
	stream  screenStream
	target  sharepicker.Target
	node    uint32
	serial  uint64
	stopped chan struct{}
}

func newScreenCast(conn *dbus.Conn, s *sessions, reminder castReminder, start func(sharepicker.Target, cursorMode, uint32) (screenStream, error), outputs func() ([]outputGeometry, error)) *screenCast {
	return &screenCast{
		conn: conn, sessions: s, reminder: reminder, start: start, outputs: outputs, focused: lysWMFocusedOutput, windows: lyswm.Windows,
		toplevels: listToplevels, displays: func() (map[string]displayID, error) { return readDisplays(drmconn.Dir) },
		virtualOffered: lyswm.OffersVirtualOutputs, createVirtual: createLysWMOutput, removeVirtual: lyswm.RemoveOutput,
		dynamicOffered: dynamicCaptureOffered,
	}
}

// sources lists what targets of these kinds resolve against.
func (c *screenCast) sources(targets []sharepicker.Target) (sources, error) {
	var now sources
	var errs []error
	if slices.ContainsFunc(targets, func(t sharepicker.Target) bool { return t.Kind == sharepicker.TargetWindow }) {
		windows, err := c.toplevels()
		now.windows = windows
		errs = append(errs, err)
	}
	if slices.ContainsFunc(targets, onOutput) {
		outputs, err := c.outputs()
		for _, o := range outputs {
			now.outputs = append(now.outputs, o.name)
		}
		displays, derr := c.displays()
		now.displays = displays
		errs = append(errs, err, derr)
	}
	return now, errors.Join(errs...)
}

// describe is what a restore token keeps of the targets; a source that
// cannot be listed is kept by its name alone.
func (c *screenCast) describe(targets []sharepicker.Target) []restoreTarget {
	now, err := c.sources(targets)
	if err != nil {
		warnf("screencast: the restore token keeps sources by name only: %v", err)
	}
	return describe(targets, now)
}

// resolveRestore is the current targets a token's kept ones name.
func (c *screenCast) resolveRestore(kept []restoreTarget) ([]sharepicker.Target, error) {
	picked := pickedOf(kept)
	now, err := c.sources(picked)
	if err != nil {
		return nil, err
	}
	return resolve(kept, now)
}

// lysWMFocusedOutput is the output lys-wm says is focused.
func lysWMFocusedOutput() (string, error) {
	outs, err := lyswm.Outputs()
	if err != nil {
		return "", err
	}
	for _, o := range outs {
		if o.Focused {
			return o.Name, nil
		}
	}
	return "", errors.New("lys-wm focuses no output")
}

// choose is the session's sources: the one output a request for a
// single monitor can only mean, the window a click picks, or else what
// the share picker replies.
func (c *screenCast) choose(call *call, cfg castState) (token bool, targets []sharepicker.Target, code uint32) {
	if t, ok := c.onlyMonitor(cfg); ok {
		return false, []sharepicker.Target{t}, ResponseSuccess
	}
	if targets, code, ok := c.clickWindow(call, cfg); ok {
		return false, targets, code
	}
	return c.pick(call, cfg)
}

// clickWindow lets a click pick the window a request for one window
// shares, over the windows lys-wm shows, front to back by its stacking
// order; where the request takes a monitor too, Enter shares the
// focused output. It is not ok, so the picker asks, without lys-wm, a
// shown window, or the shell's overlay or one that cannot open; only
// Escape cancels the request, as a cancelled picker does.
func (c *screenCast) clickWindow(call *call, cfg castState) ([]sharepicker.Target, uint32, bool) {
	if cfg.types&sourceWindow == 0 || cfg.multiple {
		return nil, 0, false
	}
	all, err := c.windows()
	if err != nil {
		if !errors.Is(err, lyswm.ErrNotRunning) {
			warnf("screencast: no lys-wm windows, so the picker asks: %v", err)
		}
		return nil, 0, false
	}
	var req sharepicker.WindowRequest
	for _, w := range lyswm.Stacked(all) {
		req.Windows = append(req.Windows, regionoverlay.Window{
			ID: lyswm.ToplevelIdentifier(w.ID), Output: w.Output,
			X: intconv.Clamp[int32](w.Rect.X), Y: intconv.Clamp[int32](w.Rect.Y),
			Width: intconv.Clamp[int32](w.Rect.Width), Height: intconv.Clamp[int32](w.Rect.Height),
		})
	}
	if len(req.Windows) == 0 {
		return nil, 0, false
	}
	if cfg.types&sourceMonitor != 0 {
		if name, err := c.focused(); err == nil {
			req.Screen = name
		}
	}
	reply, err := sharepicker.NewClient(c.conn).PickWindow(call.on(sharepicker.ServiceName), call.caller, req)
	switch {
	case call.closed():
		return nil, ResponseCancelled, true
	case call.missing != nil:
		return nil, ResponseOther, true
	case err != nil:
		warnf("screencast: no click-to-pick, so the picker asks: %v", err)
		return nil, 0, false
	case reply == "":
		return nil, ResponseCancelled, true
	}
	_, targets, ok := sharepicker.ParseReply(reply)
	if !ok || len(targets) != 1 || !offered(targets[0], req) {
		warnf("screencast: the click-to-pick replied %q for what it was offered", reply)
		return nil, ResponseOther, true
	}
	return targets, ResponseSuccess, true
}

// offered reports whether a click-to-pick request offered t: one of its
// windows, or its screen.
func offered(t sharepicker.Target, req sharepicker.WindowRequest) bool {
	if t.Kind == sharepicker.TargetOutput {
		return req.Screen != "" && t.Name == req.Screen
	}
	return t.Kind == sharepicker.TargetWindow && slices.ContainsFunc(req.Windows, func(w regionoverlay.Window) bool { return w.ID == t.Name })
}

// onlyMonitor is the output a request for one monitor shares without
// asking again: the app's own dialog already chose the entire screen.
// It is the one output there is, or the focused one where lys-wm says
// which; on another compositor with several, the picker asks.
func (c *screenCast) onlyMonitor(cfg castState) (sharepicker.Target, bool) {
	if cfg.types != sourceMonitor || cfg.multiple {
		return sharepicker.Target{}, false
	}
	outputs, err := c.outputs()
	if err != nil || len(outputs) == 0 {
		return sharepicker.Target{}, false
	}
	name := outputs[0].name
	if len(outputs) > 1 {
		if name, err = c.focused(); err != nil {
			if !errors.Is(err, lyswm.ErrNotRunning) {
				warnf("screencast: no focused output, so the picker asks: %v", err)
			}
			return sharepicker.Target{}, false
		}
		if !slices.ContainsFunc(outputs, func(o outputGeometry) bool { return o.name == name }) {
			return sharepicker.Target{}, false
		}
	}
	return sharepicker.Target{Kind: sharepicker.TargetOutput, Name: name}, true
}

func (c *screenCast) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    ScreenCastIface,
		Methods: screenCastObject{c},
		Properties: dbusx.Getters{
			"AvailableSourceTypes": func() any { return c.availableSources() },
			"AvailableCursorModes": func() any { return uint32(availableCursorModes) },
			"version":              func() any { return uint32(6) },
		},
	}
}

// sharing gives a session its castState, once, with the cleanup that
// stops its streams when the session closes.
func (c *screenCast) sharing(s *session) {
	s.mu.Lock()
	fresh := s.cast == nil
	if fresh {
		s.cast = &castState{}
	}
	s.mu.Unlock()
	if fresh {
		s.onClose(func() {
			s.mu.Lock()
			streams := s.cast.streams
			s.cast.streams = nil
			s.mu.Unlock()
			c.stopStreams(streams)
			if len(streams) > 0 {
				c.sessions.notifyShares()
			}
		})
	}
}

func (c *screenCast) stopStreams(streams []startedStream) {
	for _, s := range streams {
		close(s.stopped)
		s.stream.Close()
	}
}

// config is a copy of the session's options.
func (c *screenCast) config(s *session) castState {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := *s.cast
	cfg.streams = nil
	return cfg
}

// pick asks the share picker for sources of the session's types. The
// code is cancelled for the user's cancel, a closed request, or a failed
// picker call, and other for a shell that never appeared or a reply the
// picker should not have made: one malformed, or naming a type not asked
// for.
func (c *screenCast) pick(call *call, cfg castState) (token bool, targets []sharepicker.Target, code uint32) {
	// No window list: the picker enumerates toplevels itself and
	// replies identifiers the capture re-resolves.
	sources := pickerSources(cfg.types, c.offersDynamic())
	reply, err := sharepicker.NewClient(c.conn).Pick(call.on(sharepicker.ServiceName), call.caller, sharepicker.Request{
		Sources: sources, AllowToken: cfg.persistMode > 0, Multiple: cfg.multiple,
	})
	switch {
	case call.closed():
		return false, nil, ResponseCancelled
	case call.missing != nil:
		return false, nil, ResponseOther
	case err != nil:
		warnf("screencast: picker call failed: %v", err)
		return false, nil, ResponseCancelled
	case reply == "":
		return false, nil, ResponseCancelled
	}
	token, targets, ok := sharepicker.ParseReply(reply)
	if !ok || !allOfTypes(targets, cfg.types) || sources&sharepicker.SourceDynamic == 0 && slices.ContainsFunc(targets, isDynamic) {
		warnf("screencast: the picker replied %q for source types %#x", reply, cfg.types)
		return false, nil, ResponseOther
	}
	return token, targets, ResponseSuccess
}

// allOfTypes reports whether every target is of one of the types.
func allOfTypes(targets []sharepicker.Target, types uint32) bool {
	for _, t := range targets {
		if sourceTypeOf(t)&types == 0 {
			return false
		}
	}
	return true
}

// errSessionClosed is begin's refusal of a session that ended while its
// streams started: nothing would ever stop them.
var errSessionClosed = errors.New("the session closed")

// begin starts a stream per target; one that fails stops the ones this
// call started, so a partial set never runs, and so does a session that
// ended meanwhile. A stream that ends by itself (its window closed, its
// output unplugged) ends the session.
func (c *screenCast) begin(session *session, targets []sharepicker.Target, cursor cursorMode) ([]startedStream, error) {
	return c.beginWith(session, targets, cursor, c.start)
}

// beginWith is begin starting each stream with start.
func (c *screenCast) beginWith(session *session, targets []sharepicker.Target, cursor cursorMode, start func(sharepicker.Target, cursorMode, uint32) (screenStream, error)) ([]startedStream, error) {
	var started []startedStream
	for _, t := range targets {
		s, err := start(t, cursor, defaultFPS)
		if err != nil {
			c.stopStreams(started)
			return nil, err
		}
		started = append(started, startedStream{stream: s, target: t, node: s.NodeID(), serial: s.Serial(), stopped: make(chan struct{})})
	}
	session.mu.Lock()
	if session.cleanups == nil {
		session.mu.Unlock()
		c.stopStreams(started)
		return nil, errSessionClosed
	}
	session.cast.streams = append(session.cast.streams, started...)
	session.mu.Unlock()
	for _, s := range started {
		go c.watch(session, s)
	}
	c.sessions.notifyShares()
	return started, nil
}

// watch closes the session when st ends without the session stopping
// it.
func (c *screenCast) watch(s *session, st startedStream) {
	select {
	case <-st.stopped:
	case <-st.stream.Ended():
		select {
		case <-st.stopped:
			return
		default:
		}
		warnf("screencast: stream %d of %s ended; closing the session", st.node, s.path)
		c.sessions.close(s)
	}
}

// castSize is a stream's (ii) size.
type castSize struct{ Width, Height int32 }

// castStream is one (ua{sv}) of Start's streams.
type castStream struct {
	Node  uint32
	Props Vardict
}

// streamProps are a started stream's properties: its source type, its
// node's PipeWire object.serial and, for an output or a region, its
// position and size in the compositor's logical space and the mapping
// id of its EIS region. A window's place is not known, so it carries
// none of the last three.
func streamProps(st startedStream, outputs []outputGeometry) Vardict {
	sourceType := sourceTypeOf(st.target)
	if _, ok := virtualOf(st); ok {
		sourceType = sourceVirtual
	}
	props := Vardict{
		"source_type":     dbus.MakeVariant(sourceType),
		"pipewire-serial": dbus.MakeVariant(st.serial),
	}
	if x, y, w, h, ok := targetGeometry(st.target, outputs); ok {
		props["position"] = dbus.MakeVariant(castSize{x, y})
		props["size"] = dbus.MakeVariant(castSize{w, h})
		props["mapping_id"] = dbus.MakeVariant(mappingID(st))
	}
	return props
}

// mappingID ties a monitor stream to the EIS region of the same area:
// its node's serial, which no other stream shares.
func mappingID(st startedStream) string { return strconv.FormatUint(st.serial, 10) }

// streams is the session's running streams.
func (c *screenCast) streams(s *session) []startedStream {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cast == nil {
		return nil
	}
	return slices.Clone(s.cast.streams)
}

// screenCastObject carries the interface's D-Bus methods.
type screenCastObject struct{ c *screenCast }

// CreateSession opens a session.
func (o screenCastObject) CreateSession(_, session dbus.ObjectPath, appID string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.c.sessions.create(session, kindScreenCast, appID)
	if err != nil {
		warnf("screencast: cannot mount session object: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	o.c.sharing(s)
	return ResponseSuccess, Vardict{}, nil
}

// SelectSources records the options and decodes a replayed token, for
// a ScreenCast session or a RemoteDesktop one sharing its screens.
// types defaults to monitor and cursor_mode to hidden, as the spec
// has them; a type or cursor mode not offered fails the call.
func (o screenCastObject) SelectSources(_, session dbus.ObjectPath, appID string, options Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.c.sessions.lookup(session, appID, kindScreenCast, kindRemoteDesktop)
	if err != nil {
		warnf("screencast: SelectSources: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	types, ok := optU32(options, "types")
	if !ok {
		types = sourceMonitor
	}
	if available := o.c.availableSources(); types == 0 || types&^available != 0 {
		warnf("screencast: SelectSources: source types %#x, of %#x offered", types, available)
		return ResponseOther, Vardict{}, nil
	}
	cursor := cursorHidden
	if v, ok := optU32(options, "cursor_mode"); ok {
		if cursor, ok = parseCursorMode(v); !ok {
			warnf("screencast: SelectSources: cursor mode %#x, of %#x offered", v, availableCursorModes)
			return ResponseOther, Vardict{}, nil
		}
	}
	o.c.sharing(s)
	multiple, _ := optBool(options, "multiple")
	persist, _ := optU32(options, "persist_mode")
	var restore []restoreTarget
	var restoreVirtual *virtualSize
	var issued time.Time
	if v, ok := options["restore_data"]; ok {
		if g, ok := decodeRestore(v); ok && g.devices == 0 && g.fits(types) {
			restore, restoreVirtual, issued = g.targets, g.virtual, g.issued
		}
	}
	s.mu.Lock()
	streams := s.cast.streams
	*s.cast = castState{types: types, cursorMode: cursor, persistMode: persist, multiple: multiple, restore: restore, restoreVirtual: restoreVirtual, restoreIssued: issued, streams: streams}
	s.mu.Unlock()
	return ResponseSuccess, Vardict{}, nil
}

// Start picks the sources (or replays the token), starts their streams
// and replies them. A restored source that is gone re-prompts once
// (xdph's restore validation) rather than failing the request.
func (o screenCastObject) Start(handle, session dbus.ObjectPath, appID, parent string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.c.sessions.lookup(session, appID, kindScreenCast)
	if err != nil {
		warnf("screencast: Start: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	call, ok := beginOnSession(o.c.conn, handle, s, parent, func(ctx context.Context, h string) error {
		return errors.Join(sharepicker.NewClient(o.c.conn).Dismiss(ctx, h), portaldialogs.NewClient(o.c.conn).Dismiss(ctx, h))
	})
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer call.end()
	shared, code := o.c.share(call, s)
	if code != ResponseSuccess {
		return code, Vardict{}, nil
	}
	if s.closed() {
		return ResponseCancelled, Vardict{}, nil
	}
	results := Vardict{"streams": dbus.MakeVariant(shared.streams)}
	if cfg := o.c.config(s); cfg.persistMode > 0 && shared.allowToken {
		results["restore_data"] = dbus.MakeVariant(encodeRestore(o.c.grant(0, shared)))
		results["persist_mode"] = dbus.MakeVariant(cfg.persistMode)
	}
	return ResponseSuccess, results, nil
}

// sharedScreens is what share started: the streams Start replies, the
// sources behind them, the virtual outputs it made, whether the user
// granted a restore token, and when that grant was given, as its token
// keeps it.
type sharedScreens struct {
	streams    []castStream
	targets    []sharepicker.Target
	virtual    []virtualSize
	allowToken bool
	issued     time.Time
}

// grant is the restore grant of what share started, with the
// RemoteDesktop device types granted beside it.
func (c *screenCast) grant(devices uint32, shared sharedScreens) restoreGrant {
	g := restoreGrant{devices: devices, targets: c.describe(shared.targets), issued: shared.issued}
	if len(shared.virtual) > 0 {
		g.virtual = &shared.virtual[0]
	}
	return g
}

// share picks the session's sources (or replays its restore targets)
// and starts their streams, for ScreenCast.Start or a RemoteDesktop
// session's.
func (c *screenCast) share(call *call, s *session) (sharedScreens, uint32) {
	cfg := c.config(s)
	if cfg.types == sourceVirtual || cfg.restoreVirtual != nil {
		issued, code := c.replayed(call, s, cfg, cfg.restoreVirtual != nil)
		if code != ResponseSuccess {
			return sharedScreens{}, code
		}
		shared, code := c.shareVirtual(call, s, cfg)
		shared.issued = issued
		return shared, code
	}
	// Replaying a token is the grant it was issued for.
	var restoring, allowToken bool
	var targets []sharepicker.Target
	if len(cfg.restore) > 0 {
		resolved, err := c.resolveRestore(cfg.restore)
		if err != nil {
			warnf("screencast: the restore token names no current source, re-prompting: %v", err)
		}
		restoring, allowToken, targets = err == nil, err == nil, resolved
	}
	issued, code := c.replayed(call, s, cfg, restoring)
	if code != ResponseSuccess {
		return sharedScreens{}, code
	}
	if !restoring {
		var code uint32
		if allowToken, targets, code = c.choose(call, cfg); code != ResponseSuccess {
			return sharedScreens{}, code
		}
	}
	streams, err := c.begin(s, targets, cfg.cursorMode)
	if err != nil && restoring && !errors.Is(err, errSessionClosed) {
		warnf("screencast: restored target unavailable, re-prompting: %v", err)
		issued = c.reminder.now().UTC()
		var code uint32
		if allowToken, targets, code = c.choose(call, cfg); code != ResponseSuccess {
			return sharedScreens{}, code
		}
		streams, err = c.begin(s, targets, cfg.cursorMode)
	}
	switch {
	case errors.Is(err, errSessionClosed):
		return sharedScreens{}, ResponseCancelled
	case err != nil:
		warnf("screencast: failed to start stream: %v", err)
		return sharedScreens{}, ResponseOther
	}
	shared := c.reply(streams, targets, allowToken)
	shared.issued = issued
	return shared, ResponseSuccess
}

// reply is what share started, with each stream's properties.
func (c *screenCast) reply(streams []startedStream, targets []sharepicker.Target, allowToken bool) sharedScreens {
	outputs, err := c.outputs()
	if err != nil {
		warnf("screencast: no output geometry, so no stream positions: %v", err)
	}
	shared := sharedScreens{streams: make([]castStream, 0, len(streams)), targets: targets, allowToken: allowToken}
	for _, st := range streams {
		shared.streams = append(shared.streams, castStream{st.node, streamProps(st, outputs)})
		if v, ok := virtualOf(st); ok {
			shared.virtual = append(shared.virtual, v.size)
		}
	}
	return shared
}

// stream is the session's running stream on node.
func (c *screenCast) stream(s *session, node uint32) (startedStream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cast == nil {
		return startedStream{}, false
	}
	i := slices.IndexFunc(s.cast.streams, func(st startedStream) bool { return st.node == node })
	if i < 0 {
		return startedStream{}, false
	}
	return s.cast.streams[i], true
}

// sharesScreens is whether SelectSources was called on the session.
func sharesScreens(s *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cast != nil
}
