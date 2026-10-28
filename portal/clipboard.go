package portal

import (
	"fmt"
	"os"
	"sync"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// ClipboardIface bridges the Wayland selection to clipboard-enabled
// RemoteDesktop sessions: they read the current
// selection, become its owner, and serve its data on demand.
const ClipboardIface = "org.freedesktop.impl.portal.Clipboard"

// clipDevice is the Wayland side: the seat's data-control device
// (waylandClipboard), a fake in tests.
type clipDevice interface {
	// own claims the clipboard offering mimes; each receiver's request
	// is passed its pipe, which the callee closes.
	own(mimes []string, pass func(mime string, pipe *os.File)) error
	// clear gives up the clipboard if it is still ours.
	clear() error
	// read asks the current selection's owner for mime and returns the
	// read end of the pipe it streams into.
	read(mime string) (*os.File, error)
}

// selectionChange is a new clipboard selection: the mime types it
// offers, and whether it is our own claim (a session's SetSelection).
type selectionChange struct {
	mimes []string
	own   bool
}

// clipTransfer is a receiver's pipe waiting for the owner session to
// fill it.
type clipTransfer struct {
	session dbus.ObjectPath
	pipe    *os.File
}

// clipboardBridge is the interface's state; which sessions enabled the
// clipboard lives on their records.
type clipboardBridge struct {
	conn     *dbus.Conn
	sessions *sessions
	// start brings the device up, calling changed on every selection
	// change; it runs on first RequestClipboard.
	start func(changed func(selectionChange)) (clipDevice, error)

	mu  sync.Mutex
	dev clipDevice
	// claimer is the session behind our claim on the clipboard: the
	// last SetSelection's, "" when no session's claim is ours. Only our
	// claim is asked for data, so it is whose transfers they are.
	claimer   dbus.ObjectPath
	serial    uint32
	transfers map[uint32]clipTransfer
}

func newClipboardBridge(conn *dbus.Conn, s *sessions, start func(func(selectionChange)) (clipDevice, error)) *clipboardBridge {
	return &clipboardBridge{conn: conn, sessions: s, start: start, transfers: map[uint32]clipTransfer{}}
}

func (c *clipboardBridge) iface() dbusx.Interface {
	return dbusx.Interface{
		Name:       ClipboardIface,
		Methods:    clipboardObject{c},
		Properties: dbusx.Getters{"version": func() any { return uint32(1) }},
	}
}

// device is the started device, nil before (or without) one.
func (c *clipboardBridge) device() clipDevice {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dev
}

// ensure starts the device once.
func (c *clipboardBridge) ensure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dev != nil {
		return nil
	}
	dev, err := c.start(c.selectionChanged)
	if err != nil {
		return fmt.Errorf("no clipboard on this compositor: %w", err)
	}
	c.dev = dev
	return nil
}

// loopEnded drops the device, which died with the compositor
// connection, and the pending transfers; the sessions keep their
// remote control but must request the clipboard again.
func (c *clipboardBridge) loopEnded() {
	c.mu.Lock()
	c.dev, c.claimer = nil, ""
	c.mu.Unlock()
	c.dropTransfers()
	for _, s := range c.sessions.each(func(*session) bool { return true }) {
		s.mu.Lock()
		s.clipboard = false
		s.mu.Unlock()
	}
}

// enabledSessions is every live, started session that enabled the
// clipboard.
func (c *clipboardBridge) enabledSessions() []dbus.ObjectPath {
	var out []dbus.ObjectPath
	for _, s := range c.sessions.each(func(s *session) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.clipboard && s.remote.started()
	}) {
		out = append(out, s.path)
	}
	return out
}

// usable fails unless the live session at path enabled the clipboard
// and started.
func (c *clipboardBridge) usable(path dbus.ObjectPath) error {
	s, ok := c.sessions.get(path)
	if !ok || s.kind != kindRemoteDesktop {
		return fmt.Errorf("no remote-desktop session at %s", path)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clipboard {
		return fmt.Errorf("session %s did not enable the clipboard", path)
	}
	if !s.remote.started() {
		return fmt.Errorf("session %s is not started", path)
	}
	return nil
}

// selectionChanged tells every session about a new selection, and
// whether it is theirs: our own claim is the claimer's, any other
// client's selection is no session's (and ends the transfers our
// replaced claim had pending).
func (c *clipboardBridge) selectionChanged(change selectionChange) {
	c.mu.Lock()
	var owner dbus.ObjectPath
	if change.own {
		owner = c.claimer
	}
	c.mu.Unlock()
	if !change.own {
		c.dropTransfers()
	}
	mimes := change.mimes
	if mimes == nil {
		mimes = []string{}
	}
	for _, s := range c.enabledSessions() {
		options := Vardict{"mime_types": dbus.MakeVariant(mimes), "session_is_owner": dbus.MakeVariant(s == owner)}
		if err := c.conn.Emit(ObjectPath, ClipboardIface+".SelectionOwnerChanged", s, options); err != nil {
			warnf("clipboard: SelectionOwnerChanged for %s: %v", s, err)
		}
	}
}

// transfer files a receiver's pipe under a fresh serial (from 1) and
// asks the claimer for the data; with no claimer left the pipe is
// closed, ending the receiver's read.
func (c *clipboardBridge) transfer(mime string, pipe *os.File) {
	c.mu.Lock()
	owner := c.claimer
	if owner == "" {
		c.mu.Unlock()
		_ = pipe.Close()
		return
	}
	c.serial++
	serial := c.serial
	c.transfers[serial] = clipTransfer{session: owner, pipe: pipe}
	c.mu.Unlock()
	if err := c.conn.Emit(ObjectPath, ClipboardIface+".SelectionTransfer", owner, mime, serial); err != nil {
		warnf("clipboard: SelectionTransfer for %s: %v", owner, err)
	}
}

// dropTransfers closes the pipes no session took: a new selection
// replaces what they were asking for, and an open pipe would keep its
// receiver waiting.
func (c *clipboardBridge) dropTransfers() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for serial, t := range c.transfers {
		_ = t.pipe.Close()
		delete(c.transfers, serial)
	}
}

// ownerClosed gives up our claim when its session closes: nothing is
// left to serve its data.
func (c *clipboardBridge) ownerClosed(path dbus.ObjectPath) {
	c.mu.Lock()
	dev := c.dev
	owned := c.claimer == path
	if owned {
		c.claimer = ""
	}
	c.mu.Unlock()
	if !owned {
		return
	}
	c.dropTransfers()
	if dev != nil {
		if err := dev.clear(); err != nil {
			warnf("clipboard: clearing %s's selection: %v", path, err)
		}
	}
}

// clipboardObject carries the interface's D-Bus methods.
type clipboardObject struct{ c *clipboardBridge }

// RequestClipboard enables clipboard access for a RemoteDesktop session
// not started yet; a compositor without data-control fails it.
func (o clipboardObject) RequestClipboard(session dbus.ObjectPath, _ Vardict) *dbus.Error {
	s, ok := o.c.sessions.get(session)
	if !ok || s.kind != kindRemoteDesktop {
		return dbusx.Failed(fmt.Sprintf("no remote-desktop session at %s", session))
	}
	s.mu.Lock()
	started, enabled := s.remote.started(), s.clipboard
	s.mu.Unlock()
	if started {
		return dbusx.Failed(fmt.Sprintf("session %s already started", session))
	}
	if err := o.c.ensure(); err != nil {
		return dbusx.Failed(err.Error())
	}
	if enabled {
		return nil
	}
	s.mu.Lock()
	s.clipboard = true
	s.mu.Unlock()
	s.onClose(func() { o.c.ownerClosed(session) })
	return nil
}

// SetSelection makes the session the owner of the given mime types.
func (o clipboardObject) SetSelection(session dbus.ObjectPath, options Vardict) *dbus.Error {
	if err := o.c.usable(session); err != nil {
		return dbusx.Failed(err.Error())
	}
	dev := o.c.device()
	if dev == nil {
		return dbusx.Failed("the clipboard went away with the compositor")
	}
	mimes, ok := options["mime_types"].Value().([]string)
	if !ok || len(mimes) == 0 {
		return dbusx.InvalidArgs("SetSelection needs mime_types")
	}
	o.c.dropTransfers()
	// The claimer is set before the claim, whose echo can arrive before
	// own returns; a failed claim leaves the previous one standing.
	o.c.mu.Lock()
	previous := o.c.claimer
	o.c.claimer = session
	o.c.mu.Unlock()
	if err := dev.own(mimes, o.c.transfer); err != nil {
		o.c.mu.Lock()
		if o.c.claimer == session {
			o.c.claimer = previous
		}
		o.c.mu.Unlock()
		return dbusx.Failed(fmt.Sprintf("set the selection: %v", err))
	}
	return nil
}

// SelectionWrite hands over the pipe of a transfer pending for the
// session, for the app to fill; godbus closes our copy once the reply
// is out.
func (o clipboardObject) SelectionWrite(session dbus.ObjectPath, serial uint32) (*os.File, *dbus.Error) {
	if err := o.c.usable(session); err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	o.c.mu.Lock()
	t, ok := o.c.transfers[serial]
	if ok && t.session == session {
		delete(o.c.transfers, serial)
	}
	o.c.mu.Unlock()
	if !ok || t.session != session {
		return nil, dbusx.Failed(fmt.Sprintf("no clipboard transfer %d pending for %s", serial, session))
	}
	return t.pipe, nil
}

// SelectionWriteDone acknowledges a finished transfer: the pipe was
// already the app's.
func (o clipboardObject) SelectionWriteDone(session dbus.ObjectPath, _ uint32, _ bool) *dbus.Error {
	if err := o.c.usable(session); err != nil {
		return dbusx.Failed(err.Error())
	}
	return nil
}

// SelectionRead streams the current selection under mime.
func (o clipboardObject) SelectionRead(session dbus.ObjectPath, mime string) (*os.File, *dbus.Error) {
	if err := o.c.usable(session); err != nil {
		return nil, dbusx.Failed(err.Error())
	}
	dev := o.c.device()
	if dev == nil {
		return nil, dbusx.Failed("the clipboard went away with the compositor")
	}
	f, err := dev.read(mime)
	if err != nil {
		return nil, dbusx.Failed(fmt.Sprintf("read the selection as %s: %v", mime, err))
	}
	return f, nil
}
