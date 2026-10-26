package pipewire

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// The objects a connection talks to: the core and the client are the
// server's, always 0 and 1; the client allocates every id after them.
const (
	coreID   = 0
	clientID = 1
)

// Core methods and events (src/pipewire/core.h), and the interface
// version this client speaks.
const (
	coreVersion = 4

	coreMethodHello        = 1
	coreMethodSync         = 2
	coreMethodPong         = 3
	coreMethodError        = 4
	coreMethodCreateObject = 6

	coreEventInfo       = 0
	coreEventDone       = 1
	coreEventPing       = 2
	coreEventError      = 3
	coreEventRemoveID   = 4
	coreEventBoundID    = 5
	coreEventAddMem     = 6
	coreEventRemoveMem  = 7
	coreEventBoundProps = 8

	clientMethodUpdateProperties = 2
	clientEventInfo              = 0
	clientEventPermissions       = 1
)

// minServer is the oldest server this client was run against and
// speaks the node activation of.
var minServer = serverVersion{1, 0, 0}

// activationV1Server is the first server with the version 1 activation
// and client-node v6.
var activationV1Server = serverVersion{1, 2, 0}

// serverVersion is a server's major.minor.micro, from core info.
type serverVersion [3]int

func (v serverVersion) String() string { return fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]) }

func (v serverVersion) less(o serverVersion) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] < o[i]
		}
	}
	return false
}

// parseServerVersion reads the leading major.minor.micro of a version
// string ("1.6.9", "1.4.0-rc1").
func parseServerVersion(s string) (serverVersion, error) {
	var v serverVersion
	parts := strings.SplitN(s, ".", 3)
	if len(parts) != 3 {
		return v, fmt.Errorf("pipewire: server version %q is not major.minor.micro", s)
	}
	for i, p := range parts {
		end := strings.IndexFunc(p, func(r rune) bool { return r < '0' || r > '9' })
		if end == 0 || (end > 0 && i < 2) {
			return v, fmt.Errorf("pipewire: server version %q is not major.minor.micro", s)
		}
		if end > 0 {
			p = p[:end]
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("pipewire: server version %q: %w", s, err)
		}
		v[i] = n
	}
	return v, nil
}

// abiFor is the activation a server of version speaks; a server older
// than minServer is refused.
func abiFor(version string) (activationABI, error) {
	v, err := parseServerVersion(version)
	switch {
	case err != nil:
		return activationABI{}, err
	case v.less(minServer):
		return activationABI{}, fmt.Errorf("pipewire: server %s is older than %s, the oldest whose node activation this client speaks", v, minServer)
	case v.less(activationV1Server):
		return activationABI{activation: 0, clientNode: 5}, nil
	}
	return activationABI{activation: activationVersion, clientNode: 6}, nil
}

// ServerError is an error the server reported on one of the
// connection's objects (core error event).
type ServerError struct {
	// Object is the id of the object the error is about.
	Object uint32
	// Errno is the server's negative errno, as a positive Errno.
	Errno unix.Errno
	// Message is the server's description.
	Message string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("pipewire: server error on object %d: %s (%v)", e.Object, e.Message, e.Errno)
}

// objectHandler takes the events for one object the client created,
// and stops whatever it runs beside the event loop when the loop ends.
type objectHandler interface {
	event(m *message) error
	stop()
}

// firstObjectID is the first id the client allocates.
const firstObjectID = clientID + 1

// core is one connection: the handshake, the memory the server shares,
// the event loop, and the objects created on it. Events are handled on
// the reader goroutine, in order.
type core struct {
	c   *conn
	mem memPool

	mu       sync.Mutex
	objects  []objectHandler
	syncSeq  uint32
	syncs    map[uint32]chan struct{}
	err      error
	stopped  chan struct{}
	onStop   func(error)
	infoSeen bool
	abi      activationABI
}

// connectCore dials the server, says hello and checks its version.
func connectCore(timeout time.Duration, onStop func(error)) (*core, error) {
	c, err := dial()
	if err != nil {
		return nil, err
	}
	k := &core{
		c:       c,
		syncs:   map[uint32]chan struct{}{},
		stopped: make(chan struct{}),
		onStop:  onStop,
	}
	go k.read()
	if err := k.hello(timeout); err != nil {
		k.close()
		return nil, err
	}
	return k, nil
}

// hello introduces the client and waits until the server's info has
// been checked.
func (k *core) hello(timeout time.Duration) error {
	if _, err := k.c.send(coreID, coreMethodHello, podStruct(podInt(coreVersion)).bytes(), nil); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("pipewire: name the client: %w", err)
	}
	props := podStruct(podDict([]dictItem{
		{"application.name", "lys-shell"},
		{"application.process.binary", filepath.Base(exe)},
		{"application.process.id", strconv.Itoa(os.Getpid())},
	}))
	if _, err := k.c.send(clientID, clientMethodUpdateProperties, props.bytes(), nil); err != nil {
		return err
	}
	if err := k.roundtrip(timeout); err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.infoSeen {
		return errors.New("pipewire: the server answered the hello without its info")
	}
	return nil
}

// newObject allocates the next object id and registers the handler
// create returns for it.
func (k *core) newObject(create func(id uint32) objectHandler) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.objects = append(k.objects, create(firstObjectID+uint32(len(k.objects))))
}

// object is the handler of id, nil for an id the client never created.
func (k *core) object(id uint32) objectHandler {
	k.mu.Lock()
	defer k.mu.Unlock()
	if id < firstObjectID || id-firstObjectID >= uint32(len(k.objects)) {
		return nil
	}
	return k.objects[id-firstObjectID]
}

// createObject asks factory for a new object of iface at id.
func (k *core) createObject(factory, iface string, version uint32, props []dictItem, id uint32) error {
	body := podStruct(podString(factory), podString(iface), podInt(int32(version)), podDict(props), podInt(int32(id)))
	_, err := k.c.send(coreID, coreMethodCreateObject, body.bytes(), nil)
	return err
}

// roundtrip waits until the server has handled everything sent before
// it, failing with the connection's error if it stopped first.
func (k *core) roundtrip(timeout time.Duration) error {
	select {
	case <-k.stopped:
		return k.stopErr()
	default:
	}
	k.mu.Lock()
	seq := k.syncSeq
	k.syncSeq++
	done := make(chan struct{})
	k.syncs[seq] = done
	k.mu.Unlock()
	if _, err := k.c.send(coreID, coreMethodSync, podStruct(podInt(coreID), podInt(int32(seq))).bytes(), nil); err != nil {
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-k.stopped:
		return k.stopErr()
	case <-timer.C:
		return fmt.Errorf("pipewire: the server did not answer within %v", timeout)
	}
}

// reportError tells the server an event on object id failed (the
// message with seq), as libpipewire's pw_proxy_error does.
func (k *core) reportError(id, seq uint32, errno unix.Errno, msg string) error {
	body := podStruct(podInt(int32(id)), podInt(int32(seq)), podInt(-int32(errno)), podString(msg))
	_, err := k.c.send(coreID, coreMethodError, body.bytes(), nil)
	return err
}

// read is the event loop: it handles every message in order until the
// connection fails or closes.
func (k *core) read() {
	var err error
	for err == nil {
		var m message
		m, err = k.c.recv()
		if err != nil {
			break
		}
		err = k.dispatch(&m)
		m.closeFds()
	}
	k.mu.Lock()
	if k.err == nil {
		k.err = err
	}
	err = k.err
	objects := slices.Clone(k.objects)
	k.mu.Unlock()
	for _, h := range objects {
		h.stop()
	}
	close(k.stopped)
	k.onStop(err)
}

// abort ends the connection from outside the event loop, which stops
// with err.
func (k *core) abort(err error) {
	k.mu.Lock()
	if k.err == nil {
		k.err = err
	}
	k.mu.Unlock()
	k.c.shutdown()
}

// stopErr is why the loop stopped.
func (k *core) stopErr() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.err
}

func (k *core) dispatch(m *message) error {
	switch m.id {
	case coreID:
		return k.coreEvent(m)
	case clientID:
		if m.opcode != clientEventInfo && m.opcode != clientEventPermissions {
			return fmt.Errorf("pipewire: client event %d", m.opcode)
		}
		return nil
	}
	h := k.object(m.id)
	if h == nil {
		return fmt.Errorf("pipewire: event %d for unknown object %d", m.opcode, m.id)
	}
	return h.event(m)
}

func (k *core) coreEvent(m *message) error {
	f := structFields(m.body)
	var err error
	switch m.opcode {
	case coreEventInfo:
		err = k.info(&f)
	case coreEventDone:
		id, seq := f.uint(), f.uint()
		if f.err == nil && id == coreID {
			k.mu.Lock()
			done, ok := k.syncs[seq]
			delete(k.syncs, seq)
			k.mu.Unlock()
			if !ok {
				err = fmt.Errorf("pipewire: done for sync %d never sent", seq)
			} else {
				close(done)
			}
		}
	case coreEventPing:
		id, seq := f.int(), f.int()
		if f.err == nil {
			_, err = k.c.send(coreID, coreMethodPong, podStruct(podInt(id), podInt(seq)).bytes(), nil)
		}
	case coreEventError:
		id, _, res, msg := f.uint(), f.int(), f.int(), f.string()
		if f.err == nil {
			err = &ServerError{Object: id, Errno: unix.Errno(-res), Message: msg}
		}
	case coreEventRemoveID:
		id := f.uint()
		if f.err == nil {
			err = fmt.Errorf("pipewire: the server removed object %d", id)
		}
	case coreEventBoundID:
		id, global := f.uint(), f.uint()
		if f.err == nil {
			err = k.bound(id, global)
		}
	case coreEventBoundProps:
		id, global := f.uint(), f.uint()
		f.dict()
		if f.err == nil {
			err = k.bound(id, global)
		}
	case coreEventAddMem:
		id, _, index, flags := f.uint(), f.id(), f.fd(), f.uint()
		if f.err == nil {
			var fd int
			if fd, err = m.takeFd(index); err == nil {
				err = k.mem.add(id, fd, flags)
			}
		}
	case coreEventRemoveMem:
		id := f.uint()
		if f.err == nil {
			err = k.mem.remove(id)
		}
	default:
		return fmt.Errorf("pipewire: core event %d", m.opcode)
	}
	if f.err != nil {
		return fmt.Errorf("pipewire: core event %d: %w", m.opcode, f.err)
	}
	return err
}

// info checks the server's version; an unsupported one ends the
// connection.
func (k *core) info(f *podIter) error {
	f.int()
	f.int()
	f.string()
	f.string()
	version := f.string()
	if f.err != nil {
		return nil
	}
	abi, err := abiFor(version)
	if err != nil {
		return err
	}
	k.mu.Lock()
	k.infoSeen, k.abi = true, abi
	k.mu.Unlock()
	return nil
}

// boundHandler is an object that learns its global id.
type boundHandler interface {
	bound(global uint32)
}

func (k *core) bound(id, global uint32) error {
	if id == clientID {
		return nil
	}
	b, ok := k.object(id).(boundHandler)
	if !ok {
		return fmt.Errorf("pipewire: global %d bound to object %d, which takes none", global, id)
	}
	b.bound(global)
	return nil
}

// close stops the loop and releases the connection and the shared
// memory.
func (k *core) close() {
	k.c.shutdown()
	<-k.stopped
	k.c.close()
	k.mem.close()
}
