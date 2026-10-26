package pipewire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// The native protocol's framing (module-protocol-native/connection.c):
// each message is a 16-byte header (object id; opcode in the top byte
// over the body size; sequence number; fd count) and a body holding one
// Struct pod, optionally followed by a footer pod. Fds travel as
// SCM_RIGHTS beside the bytes, a message's fds with its first byte.
const (
	headerSize = 16
	// maxBodySize is what the header's 24 size bits hold.
	maxBodySize = 1<<24 - 1
	// maxFdsPerMessage is what the server receives per recvmsg
	// (MAX_FDS_MSG): a message with more would arrive truncated.
	maxFdsPerMessage = 28
	// defaultRemote is the socket name libpipewire connects to.
	defaultRemote = "pipewire-0"
	// systemRuntimeDir is where a system-wide daemon puts its socket.
	systemRuntimeDir = "/run/pipewire"
)

// message is one received message. It owns its fds until a handler
// takes them; the reader closes the rest.
type message struct {
	id     uint32
	opcode uint8
	seq    uint32
	body   []byte
	fds    []int
}

// takeFd hands over the fd at index (an Fd field's value), which the
// caller then owns; a missing or already taken index is an error.
func (m *message) takeFd(index int64) (int, error) {
	if index < 0 || index >= int64(len(m.fds)) || m.fds[index] < 0 {
		return -1, fmt.Errorf("fd index %d of a message carrying %d", index, len(m.fds))
	}
	fd := m.fds[index]
	m.fds[index] = -1
	return fd, nil
}

// closeFds closes the fds no handler took.
func (m *message) closeFds() {
	for i, fd := range m.fds {
		if fd >= 0 {
			_ = unix.Close(fd)
			m.fds[i] = -1
		}
	}
}

// conn is one connection to the server. Any goroutine sends; one reader
// receives.
type conn struct {
	fd int

	wmu sync.Mutex
	seq uint32

	in    []byte
	inFds []int
	oob   []byte
}

// remotePaths are the sockets to try, in libpipewire's order:
// $PIPEWIRE_REMOTE (a name, an absolute path, or @ for an abstract
// socket) or pipewire-0, under $PIPEWIRE_RUNTIME_DIR or
// $XDG_RUNTIME_DIR and then the system daemon's directory.
func remotePaths() []string {
	name := os.Getenv("PIPEWIRE_REMOTE")
	if name == "" {
		name = defaultRemote
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "@") {
		return []string{name}
	}
	var paths []string
	if dir := os.Getenv("PIPEWIRE_RUNTIME_DIR"); dir != "" {
		paths = append(paths, filepath.Join(dir, name))
	} else if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		paths = append(paths, filepath.Join(dir, name))
	}
	return append(paths, filepath.Join(systemRuntimeDir, name))
}

// dial connects to the first remote socket that accepts, joining every
// failure when none does.
func dial() (*conn, error) {
	var errs []error
	for _, path := range remotePaths() {
		c, err := dialPath(path)
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, fmt.Errorf("pipewire: connect to the daemon: %w", errors.Join(errs...))
}

func dialPath(path string) (*conn, error) {
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("socket for %s: %w", path, err)
	}
	addr := path
	if strings.HasPrefix(path, "@") {
		addr = "\x00" + path[1:]
	}
	if err := unix.Connect(fd, &unix.SockaddrUnix{Name: addr}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("connect %s: %w", path, err)
	}
	return newConn(fd), nil
}

func newConn(fd int) *conn {
	return &conn{fd: fd, oob: make([]byte, unix.CmsgSpace(maxFdsPerMessage*4))}
}

// send writes one message: the body (a Struct pod) to object id with
// the fds, which stay the caller's. It returns the message's sequence
// number.
func (c *conn) send(id uint32, opcode uint8, body []byte, fds []int) (uint32, error) {
	if len(body) > maxBodySize {
		return 0, fmt.Errorf("pipewire: message %d to object %d: body of %d bytes exceeds %d", opcode, id, len(body), maxBodySize)
	}
	if len(fds) > maxFdsPerMessage {
		return 0, fmt.Errorf("pipewire: message %d to object %d carries %d fds, more than %d", opcode, id, len(fds), maxFdsPerMessage)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	seq := c.seq
	c.seq++
	buf := make([]byte, headerSize, headerSize+len(body))
	binary.LittleEndian.PutUint32(buf[0:], id)
	binary.LittleEndian.PutUint32(buf[4:], uint32(opcode)<<24|uint32(len(body)))
	binary.LittleEndian.PutUint32(buf[8:], seq)
	binary.LittleEndian.PutUint32(buf[12:], uint32(len(fds)))
	buf = append(buf, body...)
	var oob []byte
	if len(fds) > 0 {
		oob = unix.UnixRights(fds...)
	}
	for len(buf) > 0 {
		n, err := unix.SendmsgN(c.fd, buf, oob, nil, unix.MSG_NOSIGNAL)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("pipewire: send message %d to object %d: %w", opcode, id, err)
		}
		buf, oob = buf[n:], nil
	}
	return seq, nil
}

// recv reads the next message. Only the reader goroutine calls it.
func (c *conn) recv() (message, error) {
	for {
		m, ok, err := c.parse()
		if err != nil || ok {
			return m, err
		}
		if err := c.fill(); err != nil {
			return message{}, err
		}
	}
}

// parse takes one whole message off the input, if there is one.
func (c *conn) parse() (message, bool, error) {
	if len(c.in) < headerSize {
		return message{}, false, nil
	}
	size := int(binary.LittleEndian.Uint32(c.in[4:]) & maxBodySize)
	if len(c.in) < headerSize+size {
		return message{}, false, nil
	}
	nfds := int(binary.LittleEndian.Uint32(c.in[12:]))
	if nfds > len(c.inFds) {
		return message{}, false, fmt.Errorf("pipewire: message claims %d fds, %d arrived", nfds, len(c.inFds))
	}
	m := message{
		id:     binary.LittleEndian.Uint32(c.in),
		opcode: uint8(binary.LittleEndian.Uint32(c.in[4:]) >> 24),
		seq:    binary.LittleEndian.Uint32(c.in[8:]),
		body:   append([]byte(nil), c.in[headerSize:headerSize+size]...),
		fds:    append([]int(nil), c.inFds[:nfds]...),
	}
	c.in = c.in[:copy(c.in, c.in[headerSize+size:])]
	c.inFds = c.inFds[:copy(c.inFds, c.inFds[nfds:])]
	return m, true, nil
}

// fill reads what the socket has, bytes and fds.
func (c *conn) fill() error {
	if cap(c.in)-len(c.in) < 4096 {
		grown := make([]byte, len(c.in), 2*cap(c.in)+4096)
		copy(grown, c.in)
		c.in = grown
	}
	for {
		n, oobn, flags, _, err := unix.Recvmsg(c.fd, c.in[len(c.in):cap(c.in)], c.oob, unix.MSG_CMSG_CLOEXEC)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return fmt.Errorf("pipewire: receive: %w", err)
		}
		fds, ferr := parseRights(c.oob[:oobn])
		c.inFds = append(c.inFds, fds...)
		switch {
		case ferr != nil:
			return fmt.Errorf("pipewire: receive fds: %w", ferr)
		case flags&unix.MSG_CTRUNC != 0:
			return errors.New("pipewire: receive: fds truncated")
		case n == 0:
			return errServerClosed
		}
		c.in = c.in[:len(c.in)+n]
		return nil
	}
}

// errServerClosed is the server hanging up.
var errServerClosed = errors.New("pipewire: the server closed the connection")

func parseRights(oob []byte) ([]int, error) {
	if len(oob) == 0 {
		return nil, nil
	}
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	var fds []int
	for _, m := range msgs {
		if m.Header.Level != unix.SOL_SOCKET || m.Header.Type != unix.SCM_RIGHTS {
			continue
		}
		got, err := unix.ParseUnixRights(&m)
		if err != nil {
			return fds, err
		}
		fds = append(fds, got...)
	}
	return fds, nil
}

// shutdown ends both directions, waking a blocked reader.
func (c *conn) shutdown() { _ = unix.Shutdown(c.fd, unix.SHUT_RDWR) }

// close releases the socket and the fds of messages never read. The
// reader must have stopped.
func (c *conn) close() {
	for _, fd := range c.inFds {
		_ = unix.Close(fd)
	}
	c.inFds = nil
	_ = unix.Close(c.fd)
}
