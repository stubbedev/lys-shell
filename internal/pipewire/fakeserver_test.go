package pipewire

import (
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/sockdir"
)

// fakeServer is the server end of one connection, scripted by a test:
// it reads what the client sends with the production framing and
// answers with hand-written events.
type fakeServer struct {
	t   *testing.T
	c   *conn
	act []byte
	abi activationABI
}

// serveFake listens where the client connects and returns the server
// end of the first connection, accepted while start runs.
func serveFake(t *testing.T, start func()) *fakeServer {
	t.Helper()
	dir := sockdir.Dir(t)
	t.Setenv("PIPEWIRE_RUNTIME_DIR", dir)
	t.Setenv("PIPEWIRE_REMOTE", "pipewire-0")
	ln, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(ln)
	if err := unix.Bind(ln, &unix.SockaddrUnix{Name: filepath.Join(dir, "pipewire-0")}); err != nil {
		t.Fatal(err)
	}
	if err := unix.Listen(ln, 1); err != nil {
		t.Fatal(err)
	}
	timeout := unix.NsecToTimeval((5 * time.Second).Nanoseconds())
	if err := unix.SetsockoptTimeval(ln, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		t.Fatal(err)
	}
	go start()
	fd, _, err := unix.Accept4(ln, unix.SOCK_CLOEXEC)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &timeout); err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{t: t, c: newConn(fd)}
	t.Cleanup(s.c.close)
	return s
}

// expect reads the next message, which must go to object id with
// opcode op.
func (s *fakeServer) expect(id uint32, op uint8) message {
	s.t.Helper()
	m, err := s.c.recv()
	if err != nil {
		s.t.Fatalf("waiting for message %d to object %d: %v", op, id, err)
	}
	if m.id != id || m.opcode != op {
		s.t.Fatalf("message %d to object %d, want %d to %d", m.opcode, m.id, op, id)
	}
	s.t.Cleanup(m.closeFds)
	return m
}

func (s *fakeServer) event(id uint32, op uint8, body pod, fds ...int) uint32 {
	s.t.Helper()
	seq, err := s.c.send(id, op, body.bytes(), fds)
	if err != nil {
		s.t.Fatal(err)
	}
	return seq
}

// handshake answers the hello: the server's info with version, then
// the client's sync.
func (s *fakeServer) handshake(version string) {
	s.t.Helper()
	s.expect(coreID, coreMethodHello)
	s.expect(clientID, clientMethodUpdateProperties)
	seq := s.syncSeq()
	s.info(version)
	s.done(seq)
	var err error
	if s.abi, err = abiFor(version); err != nil {
		s.t.Fatal(err)
	}
}

// info is the server's core info, with its version.
func (s *fakeServer) info(version string) {
	s.event(coreID, coreEventInfo, podStruct(podInt(0), podInt(1), podString("user"), podString("host"),
		podString(version), podString("pipewire-0"), podLong(0), podDict(nil)))
}

// syncSeq waits for a sync and returns its seq.
func (s *fakeServer) syncSeq() int32 {
	s.t.Helper()
	f := structFields(s.expect(coreID, coreMethodSync).body)
	f.int()
	return f.int()
}

func (s *fakeServer) done(seq int32) {
	s.event(coreID, coreEventDone, podStruct(podInt(coreID), podInt(seq)))
}

// fakeNodeID is the global id the fake server gives the node.
const fakeNodeID = 42

// createNode takes the node's creation and its first updates.
func (s *fakeServer) createNode() {
	s.t.Helper()
	m := s.expect(coreID, coreMethodCreateObject)
	f := structFields(m.body)
	if factory, iface, version := f.string(), f.string(), f.int(); f.err != nil || factory != clientNodeFactory || iface != clientNodeIface || uint32(version) != s.abi.clientNode {
		s.t.Fatalf("create_object %q %q %d: %v", factory, iface, version, f.err)
	}
	s.expect(firstObjectID, nodeMethodUpdate)
	s.expect(firstObjectID, nodeMethodPortUpdate)
}

// exportNode registers the node and hands over its transport: an
// activation in a memfd, marked with the server's activation version,
// and two eventfds.
func (s *fakeServer) exportNode() {
	s.t.Helper()
	s.transport(s.abi.activation)
	s.expect(firstObjectID, nodeMethodSetActive)
	s.done(s.syncSeq())
}

// transport sends the node's transport with an activation claiming
// version.
func (s *fakeServer) transport(version uint32) {
	s.t.Helper()
	fd, mem, err := memfd(int(pageSize))
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = unix.Munmap(mem) })
	s.act = mem
	binary.LittleEndian.PutUint32(mem[0:], activationInactive)
	binary.LittleEndian.PutUint32(mem[544:], version)
	binary.LittleEndian.PutUint32(mem[556:], activationFlagAsync)
	rd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		s.t.Fatal(err)
	}
	wr, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		s.t.Fatal(err)
	}
	s.event(coreID, coreEventAddMem, podStruct(podInt(0), podID(dataMemFd), podFd(0), podInt(memFlagReadable|memFlagWritable)), fd)
	s.event(coreID, coreEventBoundProps, podStruct(podInt(firstObjectID), podInt(fakeNodeID), podDict(nil)))
	s.event(firstObjectID, nodeEventTransport, podStruct(podFd(0), podFd(1), podInt(0), podInt(0), podInt(2312)), rd, wr)
	for _, f := range []int{fd, rd, wr} {
		_ = unix.Close(f)
	}
}

// started is a Start running against the fake server.
type started struct {
	p   *Producer
	err error
}

func startAgainst(t *testing.T, script func(*fakeServer)) (*fakeServer, started) {
	t.Helper()
	result := make(chan started, 1)
	s := serveFake(t, func() {
		p, err := Start(Config{Width: 64, Height: 32, Stride: 256, Format: VideoBGRx, FPS: 30, Source: solid{1}})
		result <- started{p, err}
	})
	script(s)
	select {
	case r := <-result:
		if r.p != nil {
			t.Cleanup(r.p.Close)
		}
		return s, r
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return")
		return nil, started{}
	}
}

// A server that registers the node starts the stream on its id; one
// that refuses the node's params fails Start with its own error.
func TestStartReportsTheServerRefusingTheNode(t *testing.T) {
	_, ok := startAgainst(t, func(s *fakeServer) {
		s.handshake("1.6.9")
		s.createNode()
		s.exportNode()
	})
	if ok.err != nil || ok.p.NodeID() != fakeNodeID {
		t.Fatalf("an accepting server: %v", ok.err)
	}

	_, refused := startAgainst(t, func(s *fakeServer) {
		s.handshake("1.6.9")
		s.createNode()
		s.event(coreID, coreEventError, podStruct(podInt(firstObjectID), podInt(0), podInt(-int32(unix.EINVAL)), podString("no format matched")))
	})
	var serverErr *ServerError
	if refused.p != nil || !errors.As(refused.err, &serverErr) || serverErr.Object != firstObjectID || serverErr.Errno != unix.EINVAL {
		t.Fatalf("a refusing server: %v", refused.err)
	}
	if want := "pipewire: export the stream node: pipewire: server error on object 2: no format matched (invalid argument)"; refused.err.Error() != want {
		t.Errorf("error %q, want %q", refused.err, want)
	}
}

// A server older than 1.0.0 is refused before the client creates
// anything on it, with both versions named; Start fails with that.
func TestStartRefusesAServerOlderThanTheActivationItSpeaks(t *testing.T) {
	s, r := startAgainst(t, func(s *fakeServer) {
		s.expect(coreID, coreMethodHello)
		s.expect(clientID, clientMethodUpdateProperties)
		s.syncSeq()
		s.info("0.3.85")
	})
	if want := "pipewire: server 0.3.85 is older than 1.0.0, the oldest whose node activation this client speaks"; r.p != nil || r.err == nil || r.err.Error() != want {
		t.Fatalf("Start = %v, want %q", r.err, want)
	}
	if m, err := s.c.recv(); !errors.Is(err, errServerClosed) {
		t.Errorf("after the refusal the client sent %d to %d (%v)", m.opcode, m.id, err)
	}
}

// A format the stream did not offer is refused back to the server on
// the event it came in, and the stream runs on; an offered one is taken
// and the port then carries it, writable and readable.
func TestAFormatNotOfferedIsRefusedOnTheWire(t *testing.T) {
	s, r := startAgainst(t, func(s *fakeServer) {
		s.handshake("1.6.9")
		s.createNode()
		s.exportNode()
	})
	if r.err != nil {
		t.Fatal(r.err)
	}
	setFormat := func(format []byte) uint32 {
		return s.event(firstObjectID, nodeEventPortSetParam, podStruct(podInt(directionOutput), podInt(0), podID(paramFormat), podInt(0), podRaw(format)))
	}

	seq := setFormat(formatObject(paramFormat, 64, 32, 30, VideoRGBA, nil))
	f := structFields(s.expect(coreID, coreMethodError).body)
	id, errSeq, res, msg := f.uint(), f.uint(), f.int(), f.string()
	if f.err != nil || id != firstObjectID || errSeq != seq || res != -int32(unix.EINVAL) || msg != "port_set_param(Format): video format 11 is not the offered 8" {
		t.Errorf("refusal = %d %d %d %q (%v)", id, errSeq, res, msg, f.err)
	}
	select {
	case <-r.p.Done():
		t.Fatalf("a refused format ended the stream: %v", r.p.Err())
	default:
	}

	offered := formatObject(paramFormat, 64, 32, 30, VideoBGRx, nil)
	setFormat(offered)
	f = structFields(s.expect(firstObjectID, nodeMethodPortUpdate).body)
	f.int()
	f.int()
	f.int()
	var params [][]byte
	for range f.int() {
		params = append(params, f.object())
	}
	info := f.strct()
	info.long()
	info.long()
	info.int()
	info.int()
	info.int()
	flags := map[uint32]uint32{}
	for range info.int() {
		flags[info.id()] = info.uint()
	}
	if f.err != nil || info.err != nil {
		t.Fatalf("port_update: %v / %v", f.err, info.err)
	}
	var carried bool
	for _, p := range params {
		carried = carried || string(p) == string(offered)
	}
	if !carried || flags[paramFormat]&(paramInfoRead|paramInfoWrite) != paramInfoRead|paramInfoWrite || flags[paramBuffers]&paramInfoRead == 0 {
		t.Errorf("the port does not carry the taken format: format %v, flags %v", carried, flags)
	}
}

// An error the server reports on the node mid-stream ends the stream
// with that error.
func TestAServerErrorMidStreamEndsTheStream(t *testing.T) {
	s, r := startAgainst(t, func(s *fakeServer) {
		s.handshake("1.6.9")
		s.createNode()
		s.exportNode()
	})
	if r.err != nil {
		t.Fatal(r.err)
	}
	s.event(coreID, coreEventError, podStruct(podInt(firstObjectID), podInt(0), podInt(-int32(unix.EIO)), podString("node broke")))
	select {
	case <-r.p.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived the server's error")
	}
	var serverErr *ServerError
	if !errors.As(r.p.Err(), &serverErr) || serverErr.Errno != unix.EIO || !strings.Contains(serverErr.Error(), "node broke") {
		t.Errorf("Err = %v", r.p.Err())
	}
}

// A 1.0 server gets the client-node v5 interface and an activation the
// client leaves its version fields alone in, as they are padding there;
// a 1.2 or later server whose activation claims no version is refused,
// since its memory is not what its version promises.
func TestAServerBefore12GetsTheVersion0Activation(t *testing.T) {
	s, r := startAgainst(t, func(s *fakeServer) {
		s.handshake("1.0.5")
		s.createNode()
		s.exportNode()
	})
	if r.err != nil {
		t.Fatalf("a 1.0.5 server: %v", r.err)
	}
	if v := binary.LittleEndian.Uint32(s.act[540:]); v != 0 {
		t.Errorf("the client wrote %d into a 1.0 activation's padding", v)
	}

	_, bad := startAgainst(t, func(s *fakeServer) {
		s.handshake("1.6.9")
		s.createNode()
		s.transport(0)
	})
	if want := "pipewire: transport: activation version 0 from a server whose version promises 1"; bad.p != nil || bad.err == nil || !strings.Contains(bad.err.Error(), want) {
		t.Errorf("a 1.6.9 server with a version 0 activation: %v", bad.err)
	}
}
