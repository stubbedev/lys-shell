package portal

import (
	"context"
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
	cursorMode, persistMode uint32
	multiple                bool
	restore                 *captureTarget
	streams                 []startedStream
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
}

// startedStream is a running stream and what Start reported of it;
// stopped closes when the session stops it on purpose, so its end is
// not taken for the source vanishing.
type startedStream struct {
	stream  screenStream
	node    uint32
	width   int32
	height  int32
	source  uint32
	stopped chan struct{}
}

func newScreenCast(conn *dbus.Conn, s *sessions, sizes *streamSizes, start func(captureTarget, bool, uint32) (screenStream, error)) *screenCast {
	return &screenCast{conn: conn, sessions: s, sizes: sizes, start: start}
}

func (c *screenCast) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:    ScreenCastIface,
		Methods: screenCastObject{c},
		Properties: dbusx.Getters{
			"AvailableSourceTypes": func() any { return sourceMonitor | sourceWindow | sourceVirtual },
			"AvailableCursorModes": func() any { return cursorHidden | cursorEmbedded },
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

// pick asks the share picker, giving up when the request is closed;
// ok is false for a cancel, a closed request, or no shell.
func (c *screenCast) pick(cancelled <-chan struct{}, allowToken, multiple bool) (token bool, targets []captureTarget, ok bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-cancelled:
			cancel()
		case <-ctx.Done():
		}
	}()
	// No window list: the picker enumerates toplevels itself and
	// replies identifiers the capture re-resolves.
	reply, err := sharepicker.NewClient(c.conn).Pick(ctx, "", allowToken, multiple)
	if err != nil {
		warnf("screencast: picker call failed: %v", err)
		return false, nil, false
	}
	return parsePickerReply(reply)
}

// begin starts a stream per target; one that fails stops the ones this
// call started, so a partial set never runs. A stream that ends by
// itself (its window closed, its output unplugged) ends the session.
func (c *screenCast) begin(session *session, targets []captureTarget, cursorMode uint32) ([]startedStream, error) {
	var started []startedStream
	for _, t := range targets {
		s, err := c.start(t, showCursor(cursorMode), defaultFPS)
		if err != nil {
			c.stopStreams(started)
			return nil, err
		}
		w, h := s.Size()
		started = append(started, startedStream{stream: s, node: s.NodeID(), width: w, height: h, source: t.sourceType(), stopped: make(chan struct{})})
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

func encodeRestore(t captureTarget) restoreToken {
	return restoreToken{restoreVendor, restoreVersion, dbus.MakeVariant(t.payload())}
}

// decodeRestore reads a token this portal issued; any other is none.
func decodeRestore(v dbus.Variant) (captureTarget, bool) {
	fields, ok := v.Value().([]any)
	if !ok || len(fields) != 3 || fields[0] != restoreVendor {
		return captureTarget{}, false
	}
	data, ok := fields[2].(dbus.Variant)
	if !ok {
		return captureTarget{}, false
	}
	payload, ok := data.Value().(string)
	if !ok {
		return captureTarget{}, false
	}
	return parseTarget(payload)
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
// a ScreenCast session or a RemoteDesktop one sharing its screens. An
// omitted cursor_mode is embedded, not hidden: many clients send none
// and expect the pointer; one that wants it hidden says so.
func (o screenCastObject) SelectSources(_, session dbus.ObjectPath, appID string, options Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.c.sessions.lookup(session, appID, kindScreenCast, kindRemoteDesktop)
	if err != nil {
		warnf("screencast: SelectSources: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	o.c.sharing(s)
	cursor, ok := optU32(options, "cursor_mode")
	if !ok {
		cursor = cursorEmbedded
	}
	multiple, _ := optBool(options, "multiple")
	persist, _ := optU32(options, "persist_mode")
	var restore *captureTarget
	if v, ok := options["restore_data"]; ok {
		if t, ok := decodeRestore(v); ok {
			restore = &t
		}
	}
	s.mu.Lock()
	streams := s.cast.streams
	*s.cast = castState{cursorMode: cursor, persistMode: persist, multiple: multiple, restore: restore, streams: streams}
	s.mu.Unlock()
	return ResponseSuccess, Vardict{}, nil
}

// Start picks the sources (or replays the token), starts their streams
// and replies them. A restored source that is gone re-prompts once
// (xdph's restore validation) rather than failing the request.
func (o screenCastObject) Start(handle, session dbus.ObjectPath, appID, _ string, _ Vardict) (uint32, Vardict, *dbus.Error) {
	s, err := o.c.sessions.lookup(session, appID, kindScreenCast)
	if err != nil {
		warnf("screencast: Start: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	cfg := o.c.config(s)
	var cancelled <-chan struct{}
	if req, err := mountRequest(o.c.conn, handle); err == nil {
		defer req.end()
		cancelled = req.Cancelled()
	}
	restoring := cfg.restore != nil
	allowToken, targets := cfg.persistMode > 0, []captureTarget(nil)
	if restoring {
		targets = []captureTarget{*cfg.restore}
	} else {
		var ok bool
		if allowToken, targets, ok = o.c.pick(cancelled, cfg.persistMode > 0, cfg.multiple); !ok {
			return ResponseCancelled, Vardict{}, nil
		}
	}
	streams, err := o.c.begin(s, targets, cfg.cursorMode)
	if err != nil && restoring {
		warnf("screencast: restored target unavailable, re-prompting: %v", err)
		var ok bool
		if allowToken, targets, ok = o.c.pick(cancelled, cfg.persistMode > 0, cfg.multiple); !ok {
			return ResponseCancelled, Vardict{}, nil
		}
		streams, err = o.c.begin(s, targets, cfg.cursorMode)
	}
	if err != nil {
		warnf("screencast: failed to start stream: %v", err)
		return ResponseOther, Vardict{}, nil
	}
	out := make([]castStream, 0, len(streams))
	for _, s := range streams {
		out = append(out, castStream{s.node, Vardict{
			"source_type": dbus.MakeVariant(s.source),
			"size":        dbus.MakeVariant(castSize{s.width, s.height}),
		}})
	}
	results := Vardict{"streams": dbus.MakeVariant(out)}
	// A token holds one target: a multi-source session restores its
	// first source only.
	if cfg.persistMode > 0 && allowToken {
		results["restore_data"] = dbus.MakeVariant(encodeRestore(targets[0]))
		results["persist_mode"] = dbus.MakeVariant(cfg.persistMode)
	}
	return ResponseSuccess, results, nil
}
