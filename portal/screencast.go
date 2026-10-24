package portal

import (
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// ScreenCastIface is org.freedesktop.impl.portal.ScreenCast
// (screencast/mod.rs): SelectSources records the options, Start shows
// the shell's share picker (unless a restore token already names the
// source), starts a PipeWire producer per picked source and replies
// their node ids. The selection never leaves lys-shell, so it works on any
// compositor.
const ScreenCastIface = "org.freedesktop.impl.portal.ScreenCast"

// Restore tokens are (suv): vendor, version, and the target payload.
const (
	restoreVendor  = "lys-shell"
	restoreVersion = uint32(1)
)

// castState is a session's screen sharing: the options gathered across
// the calls and the streams Start runs. It lives on the session,
// guarded by its mu.
type castState struct {
	types, cursorMode, persistMode uint32
	multiple                       bool
	restore                        []captureTarget
	streams                        []startedStream
}

// streamSizes maps a running stream's node id to its size, shared with
// RemoteDesktop, which maps absolute pointer motion onto a stream.
type streamSizes struct {
	mu    sync.Mutex
	sizes map[uint32][2]int32
}

func (s *streamSizes) set(node uint32, w, h int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes[node] = [2]int32{w, h}
}

func (s *streamSizes) drop(node uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sizes, node)
}

func (s *streamSizes) get(node uint32) ([2]int32, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	size, ok := s.sizes[node]
	return size, ok
}

// screenCast is the interface: its sessions' state lives on the
// session records.
type screenCast struct {
	conn     *dbus.Conn
	sessions *sessions
	sizes    *streamSizes
	// start runs one stream (startScreenStream; a fake in tests).
	start func(target captureTarget, cursor bool, fps uint32) (screenStream, error)
	// outputs is the outputs' logical geometry (the Wayland loop's; a
	// fake in tests).
	outputs func() ([]outputGeometry, error)
}

// startedStream is a running stream and what Start reported of it;
// stopped closes when the session stops it on purpose, so its end is
// not taken for the source vanishing.
type startedStream struct {
	stream  screenStream
	target  captureTarget
	node    uint32
	width   int32
	height  int32
	stopped chan struct{}
}

func newScreenCast(conn *dbus.Conn, s *sessions, sizes *streamSizes, start func(captureTarget, bool, uint32) (screenStream, error), outputs func() ([]outputGeometry, error)) *screenCast {
	return &screenCast{conn: conn, sessions: s, sizes: sizes, start: start, outputs: outputs}
}

func (c *screenCast) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    ScreenCastIface,
		Methods: screenCastObject{c},
		Properties: dbusx.Getters{
			"AvailableSourceTypes": func() any { return availableSources },
			"AvailableCursorModes": func() any { return availableCursorModes },
			// 4: cursor_mode (2), source_type (3) and restore_data /
			// persist_mode (4) are honoured; mapping_id (5) and
			// pipewire-serial (6) are not, so claiming them would
			// promise a contract the streams do not keep.
			"version": func() any { return uint32(4) },
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
		})
	}
}

func (c *screenCast) stopStreams(streams []startedStream) {
	for _, s := range streams {
		close(s.stopped)
		c.sizes.drop(s.node)
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
// code is cancelled for the user's cancel, a closed request, or no
// shell, and other for a reply the picker should not have made: one
// malformed, or naming a type not asked for.
func (c *screenCast) pick(call *call, cfg castState) (token bool, targets []captureTarget, code uint32) {
	// No window list: the picker enumerates toplevels itself and
	// replies identifiers the capture re-resolves.
	reply, err := sharepicker.NewClient(c.conn).Pick(call.ctx, call.caller, sharepicker.Request{
		Sources: pickerSources(cfg.types), AllowToken: cfg.persistMode > 0, Multiple: cfg.multiple,
	})
	switch {
	case call.closed():
		return false, nil, ResponseCancelled
	case err != nil:
		warnf("screencast: picker call failed: %v", err)
		return false, nil, ResponseCancelled
	case reply == "":
		return false, nil, ResponseCancelled
	}
	token, targets, ok := parsePickerReply(reply)
	if !ok || !allOfTypes(targets, cfg.types) {
		warnf("screencast: the picker replied %q for source types %#x", reply, cfg.types)
		return false, nil, ResponseOther
	}
	return token, targets, ResponseSuccess
}

// allOfTypes reports whether every target is of one of the types.
func allOfTypes(targets []captureTarget, types uint32) bool {
	for _, t := range targets {
		if t.sourceType()&types == 0 {
			return false
		}
	}
	return true
}

// begin starts a stream per target; one that fails stops the ones this
// call started, so a partial set never runs. A stream that ends by
// itself (its window closed, its output unplugged) ends the session.
func (c *screenCast) begin(session *session, targets []captureTarget, cursorMode uint32) ([]startedStream, error) {
	var started []startedStream
	for _, t := range targets {
		s, err := c.start(t, cursorMode == cursorEmbedded, defaultFPS)
		if err != nil {
			c.stopStreams(started)
			return nil, err
		}
		w, h := s.Size()
		started = append(started, startedStream{stream: s, target: t, node: s.NodeID(), width: w, height: h, stopped: make(chan struct{})})
	}
	for _, s := range started {
		c.sizes.set(s.node, s.width, s.height)
		go c.watch(session, s)
	}
	session.mu.Lock()
	session.cast.streams = append(session.cast.streams, started...)
	session.mu.Unlock()
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

// restoreToken is the (suv) restore_data.
type restoreToken struct {
	Vendor  string
	Version uint32
	Data    dbus.Variant
}

// encodeRestore keeps every target of the session, as the picker
// replies them.
func encodeRestore(targets []captureTarget) restoreToken {
	return restoreToken{restoreVendor, restoreVersion, dbus.MakeVariant(joinTargets(targets))}
}

// decodeRestore reads a token this portal issued; any other is none.
func decodeRestore(v dbus.Variant) ([]captureTarget, bool) {
	fields, ok := v.Value().([]any)
	if !ok || len(fields) != 3 || fields[0] != restoreVendor {
		return nil, false
	}
	data, ok := fields[2].(dbus.Variant)
	if !ok {
		return nil, false
	}
	payload, ok := data.Value().(string)
	if !ok {
		return nil, false
	}
	return parseTargets(payload)
}

// streamProps are a started stream's properties: its source type and,
// for an output or a region, its position and size in the compositor's
// logical space. A window's are not known, so it carries neither.
func streamProps(st startedStream, outputs []outputGeometry) Vardict {
	props := Vardict{"source_type": dbus.MakeVariant(st.target.sourceType())}
	if x, y, w, h, ok := st.target.geometry(outputs); ok {
		props["position"] = dbus.MakeVariant(castSize{x, y})
		props["size"] = dbus.MakeVariant(castSize{w, h})
	}
	return props
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
	if types == 0 || types&^availableSources != 0 {
		warnf("screencast: SelectSources: source types %#x, of %#x offered", types, availableSources)
		return ResponseOther, Vardict{}, nil
	}
	cursor, ok := optU32(options, "cursor_mode")
	if !ok {
		cursor = cursorHidden
	}
	if cursor != cursorHidden && cursor != cursorEmbedded {
		warnf("screencast: SelectSources: cursor mode %#x, of %#x offered", cursor, availableCursorModes)
		return ResponseOther, Vardict{}, nil
	}
	o.c.sharing(s)
	multiple, _ := optBool(options, "multiple")
	persist, _ := optU32(options, "persist_mode")
	var restore []captureTarget
	if v, ok := options["restore_data"]; ok {
		if targets, ok := decodeRestore(v); ok && allOfTypes(targets, types) {
			restore = targets
		}
	}
	s.mu.Lock()
	streams := s.cast.streams
	*s.cast = castState{types: types, cursorMode: cursor, persistMode: persist, multiple: multiple, restore: restore, streams: streams}
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
	call, ok := beginOr(o.c.conn, handle, appID, parent, sharepicker.NewClient(o.c.conn).Dismiss)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer call.end()
	results, code := o.c.share(call, s)
	return code, results, nil
}

// share picks the session's sources (or replays its token), starts
// their streams, and makes the results Start replies: the streams and,
// when granted, a restore token for all of them.
func (c *screenCast) share(call *call, s *session) (Vardict, uint32) {
	cfg := c.config(s)
	restoring := len(cfg.restore) > 0
	allowToken, targets := cfg.persistMode > 0, cfg.restore
	if !restoring {
		var code uint32
		if allowToken, targets, code = c.pick(call, cfg); code != ResponseSuccess {
			return Vardict{}, code
		}
	}
	streams, err := c.begin(s, targets, cfg.cursorMode)
	if err != nil && restoring {
		warnf("screencast: restored target unavailable, re-prompting: %v", err)
		var code uint32
		if allowToken, targets, code = c.pick(call, cfg); code != ResponseSuccess {
			return Vardict{}, code
		}
		streams, err = c.begin(s, targets, cfg.cursorMode)
	}
	if err != nil {
		warnf("screencast: failed to start stream: %v", err)
		return Vardict{}, ResponseOther
	}
	outputs, err := c.outputs()
	if err != nil {
		warnf("screencast: no output geometry, so no stream positions: %v", err)
	}
	out := make([]castStream, 0, len(streams))
	for _, st := range streams {
		out = append(out, castStream{st.node, streamProps(st, outputs)})
	}
	results := Vardict{"streams": dbus.MakeVariant(out)}
	if cfg.persistMode > 0 && allowToken {
		results["restore_data"] = dbus.MakeVariant(encodeRestore(targets))
		results["persist_mode"] = dbus.MakeVariant(cfg.persistMode)
	}
	return results, ResponseSuccess
}
