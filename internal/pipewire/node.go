package pipewire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Client-node methods and events (src/pipewire/extensions/client-node.h)
// and the interface version this client speaks.
const (
	clientNodeFactory = "client-node"
	clientNodeIface   = "PipeWire:Interface:ClientNode"

	nodeMethodUpdate      = 2
	nodeMethodPortUpdate  = 3
	nodeMethodSetActive   = 4
	nodeMethodPortBuffers = 6

	nodeEventTransport      = 0
	nodeEventSetParam       = 1
	nodeEventSetIO          = 2
	nodeEventEvent          = 3
	nodeEventCommand        = 4
	nodeEventAddPort        = 5
	nodeEventRemovePort     = 6
	nodeEventPortSetParam   = 7
	nodeEventPortUseBuffers = 8
	nodeEventPortSetIO      = 9
	nodeEventSetActivation  = 10
	nodeEventPortSetMixInfo = 11
)

// Update masks and flags (client-node.h, spa/node/node.h,
// spa/param/param.h).
const (
	nodeUpdateParams = 1 << 0
	nodeUpdateInfo   = 1 << 1

	nodeChangeMaskFlags  = 1 << 0
	nodeChangeMaskProps  = 1 << 1
	nodeChangeMaskParams = 1 << 2
	nodeFlagRT           = 1 << 0
	nodeFlagAsync        = 1 << 6

	portChangeMaskFlags  = 1 << 0
	portChangeMaskRate   = 1 << 1
	portChangeMaskParams = 1 << 3
	portFlagCanAlloc     = 1 << 2

	paramInfoSerial = 1 << 0
	paramInfoRead   = 1 << 1
	paramInfoWrite  = 1 << 2

	paramProps          = 2
	paramProcessLatency = 16

	buffersFlagAlloc = 1 << 0
)

// Node commands (spa/node/command.h): an Object of type Command:Node
// whose id is the command.
const (
	commandTypeNode = 0x30002
	commandSuspend  = 0
	commandPause    = 1
	commandStart    = 2
)

// Port directions (enum spa_direction).
const (
	directionInput  = 0
	directionOutput = 1
)

// The limits of a buffer description (module-client-node).
const (
	maxBuffers = 64
	maxMetas   = 16
)

// portParam is one param id of the port: its info flags and the pods.
type portParam struct {
	id    uint32
	flags uint32
	pods  [][]byte
}

// nodeHandler is what a node does with its one port: the params it
// offers, the formats it takes, the buffer memory it provides when it
// allocates, and the work of a cycle. The node calls every method with
// its lock held, so they never run concurrently.
type nodeHandler interface {
	portParams() []portParam
	setFormat(format []byte) error
	allocData(b *buffer) error
	releaseData(b *buffer)
	process(n *node) int32
}

// buffer is one buffer of the port: its metas and chunk in the server's
// shared memory, and its data, mapped from the server or provided by
// the handler.
type buffer struct {
	id        uint32
	meta      mapping
	chunk     *cSpaChunk
	header    *cMetaHeader
	transform *uint32
	damage    []cMetaRegion
	busy      *cMetaBusy
	data      []byte
	dataMap   mapping

	allocated bool
	dataType  uint32
	dataFlags uint32
	fd        int
	maxsize   uint32
	dmabuf    Dmabuf
}

// mix is one link of the port: the io area it exchanges buffer ids
// through, a slot per cycle parity.
type mix struct {
	id    uint32
	io    mapping
	slots [2]*cIOBuffers
}

// target is a node this one wakes when it finishes a cycle.
type target struct {
	id         uint32
	act        mapping
	activation *nodeActivation
	fd         int
}

// node is a client-node: a node implemented here and exported to the
// server, with one port in direction.
type node struct {
	k         *core
	h         nodeHandler
	abi       activationABI
	direction uint32
	props     []dictItem
	portFlags uint64

	id       uint32
	global   uint32
	exported chan struct{}
	isBound  bool
	hasXport bool

	portMu sync.Mutex
	infos  []portParam

	rtFile  *os.File
	rtDone  chan struct{}
	writeFd int

	mu         sync.Mutex
	act        mapping
	activation *nodeActivation
	position   *ioPosition
	posMap     mapping
	buffers    []buffer
	nextBuffer int
	mixes      []mix
	targets    []target
	prepared   bool
	portDirty  bool
	ts         unix.Timespec
	wake       [8]byte
}

// newNode registers a node on k; export creates it on the server.
func newNode(k *core, h nodeHandler, direction uint32, props []dictItem, portFlags uint64) *node {
	k.mu.Lock()
	abi := k.abi
	k.mu.Unlock()
	n := &node{k: k, h: h, abi: abi, direction: direction, props: props, portFlags: portFlags, exported: make(chan struct{}), writeFd: -1}
	n.wake[0] = 1
	k.newObject(func(id uint32) objectHandler {
		n.id = id
		return n
	})
	return n
}

// export creates the node on the server and waits until the server has
// registered it and handed over its transport.
func (n *node) export(timeout time.Duration) error {
	if err := n.k.createObject(clientNodeFactory, clientNodeIface, n.abi.clientNode, n.props, n.id); err != nil {
		return err
	}
	if err := n.update(); err != nil {
		return err
	}
	if err := n.updatePort(); err != nil {
		return err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-n.exported:
	case <-n.k.stopped:
		return n.k.stopErr()
	case <-timer.C:
		return fmt.Errorf("pipewire: the server did not export the node within %v", timeout)
	}
	return n.k.roundtrip(timeout)
}

// update sends the node's info: one port in its direction, its
// properties, asynchronous real-time scheduling.
func (n *node) update() error {
	maxIn, maxOut := int32(0), int32(0)
	if n.direction == directionInput {
		maxIn = 1
	} else {
		maxOut = 1
	}
	info := []pod{
		podInt(maxIn), podInt(maxOut),
		podLong(nodeChangeMaskFlags | nodeChangeMaskProps | nodeChangeMaskParams),
		podLong(nodeFlagRT | nodeFlagAsync),
	}
	info = appendInlineDict(info, n.props)
	info = append(info, podInt(0))
	body := podStruct(podInt(nodeUpdateParams|nodeUpdateInfo), podInt(0), podStruct(info...))
	_, err := n.k.c.send(n.id, nodeMethodUpdate, body.bytes(), nil)
	return err
}

// appendInlineDict is a dict as the node and port infos carry it: the
// count and the items inline, not in a Struct of their own.
func appendInlineDict(fields []pod, items []dictItem) []pod {
	fields = append(fields, podInt(int32(len(items))))
	for _, it := range items {
		fields = append(fields, podString(it.key), podString(it.value))
	}
	return fields
}

// updatePort sends the port's params as the handler has them, flipping
// the serial bit of every param id whose pods changed so the server
// renegotiates what depends on it.
func (n *node) updatePort() error {
	n.portMu.Lock()
	defer n.portMu.Unlock()
	n.mu.Lock()
	params := n.h.portParams()
	for i := range params {
		params[i].flags |= n.serial(params[i])
	}
	n.infos = params
	n.mu.Unlock()

	fields := []pod{podInt(int32(n.direction)), podInt(0), podInt(nodeUpdateParams | nodeUpdateInfo)}
	var pods []pod
	for _, p := range params {
		for _, b := range p.pods {
			pods = append(pods, podRaw(b))
		}
	}
	fields = append(fields, podInt(int32(len(pods))))
	fields = append(fields, pods...)
	info := []pod{
		podLong(portChangeMaskFlags | portChangeMaskRate | portChangeMaskParams),
		podLong(int64(n.portFlags)),
		podInt(0), podInt(1),
	}
	info = appendInlineDict(info, nil)
	info = append(info, podInt(int32(len(params))))
	for _, p := range params {
		info = append(info, podID(p.id), podInt(int32(p.flags)))
	}
	fields = append(fields, podStruct(info...))
	_, err := n.k.c.send(n.id, nodeMethodPortUpdate, podStruct(fields...).bytes(), nil)
	return err
}

// serial is the serial bit p carries: the previous one, flipped when
// its pods changed.
func (n *node) serial(p portParam) uint32 {
	for _, old := range n.infos {
		if old.id != p.id {
			continue
		}
		if podsEqual(old.pods, p.pods) && old.flags&^paramInfoSerial == p.flags {
			return old.flags & paramInfoSerial
		}
		return old.flags&paramInfoSerial ^ paramInfoSerial
	}
	return 0
}

func podsEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

// markPortChanged has the port's params sent again after the cycle.
func (n *node) markPortChanged() { n.portDirty = true }

func (n *node) bound(global uint32) {
	n.global = global
	n.isBound = true
	n.checkExported()
}

func (n *node) checkExported() {
	if n.isBound && n.hasXport {
		select {
		case <-n.exported:
		default:
			close(n.exported)
		}
	}
}

func (n *node) event(m *message) error {
	f := structFields(m.body)
	var err error
	switch m.opcode {
	case nodeEventTransport:
		err = n.transport(m, &f)
	case nodeEventSetParam:
		id, _, _ := f.id(), f.int(), f.object()
		if f.err == nil && id != paramProps && id != paramProcessLatency {
			err = n.k.reportError(n.id, m.seq, unix.ENOTSUP, fmt.Sprintf("set_param %d: the node has no such param", id))
		}
	case nodeEventSetIO:
		id, memID, off, size := f.id(), f.uint(), f.uint(), f.uint()
		if f.err == nil {
			err = n.setIO(id, memID, off, size)
		}
	case nodeEventEvent:
		f.object()
	case nodeEventCommand:
		cmd := f.object()
		if f.err == nil {
			err = n.command(cmd)
		}
	case nodeEventAddPort, nodeEventRemovePort:
		err = n.k.reportError(n.id, m.seq, unix.ENOTSUP, "the node's ports are fixed")
	case nodeEventPortSetParam:
		dir, port, id, _, param := f.uint(), f.uint(), f.id(), f.int(), f.object()
		if f.err == nil {
			err = n.portSetParam(m.seq, dir, port, id, param)
		}
	case nodeEventPortUseBuffers:
		err = n.portUseBuffers(m.seq, &f)
	case nodeEventPortSetIO:
		dir, port, mixID, id, memID, off, size := f.uint(), f.uint(), f.uint(), f.id(), f.uint(), f.uint(), f.uint()
		if f.err == nil {
			err = n.portSetIO(m.seq, dir, port, mixID, id, memID, off, size)
		}
	case nodeEventSetActivation:
		err = n.setActivation(m, &f)
	case nodeEventPortSetMixInfo:
		dir, port, mixID, peer := f.uint(), f.uint(), f.uint(), f.uint()
		f.dict()
		if f.err == nil {
			err = n.portSetMixInfo(m.seq, dir, port, mixID, peer)
		}
	default:
		return fmt.Errorf("pipewire: client-node event %d", m.opcode)
	}
	if f.err != nil {
		return fmt.Errorf("pipewire: client-node event %d: %w", m.opcode, f.err)
	}
	return err
}

// transport maps the node's activation and starts its cycles: the
// server wakes the node through the read fd.
func (n *node) transport(m *message, f *podIter) error {
	rIdx, wIdx, memID, off, size := f.fd(), f.fd(), f.uint(), f.uint(), f.uint()
	if f.err != nil {
		return nil
	}
	if n.hasXport {
		return errors.New("pipewire: a second transport for the node")
	}
	readFd, err := m.takeFd(rIdx)
	if err != nil {
		return fmt.Errorf("pipewire: transport: %w", err)
	}
	if err := unix.SetNonblock(readFd, true); err != nil {
		_ = unix.Close(readFd)
		return fmt.Errorf("pipewire: transport: %w", err)
	}
	rt := os.NewFile(uintptr(readFd), "pipewire-node")
	writeFd, err := m.takeFd(wIdx)
	if err != nil {
		_ = rt.Close()
		return fmt.Errorf("pipewire: transport: %w", err)
	}
	act, a, err := n.mapActivation(memID, off, size)
	if err != nil {
		_ = rt.Close()
		_ = unix.Close(writeFd)
		return fmt.Errorf("pipewire: transport: %w", err)
	}
	if n.abi.v1() {
		if v := a.serverVersion.Load(); v < activationVersion {
			act.unmap()
			_ = rt.Close()
			_ = unix.Close(writeFd)
			return fmt.Errorf("pipewire: transport: activation version %d from a server whose version promises %d", v, activationVersion)
		}
		a.clientVersion.Store(activationVersion)
	}
	n.mu.Lock()
	n.act, n.activation, n.position = act, a, &a.position
	n.mu.Unlock()
	n.rtFile, n.rtDone, n.writeFd, n.hasXport = rt, make(chan struct{}), writeFd, true
	go n.realtime()
	if _, err := n.k.c.send(n.id, nodeMethodSetActive, podStruct(podBool(true)).bytes(), nil); err != nil {
		return err
	}
	n.checkExported()
	return nil
}

// mapActivation maps a node's activation record.
func (n *node) mapActivation(memID, off, size uint32) (mapping, *nodeActivation, error) {
	act, err := n.k.mem.mapID(memID, off, size)
	if err != nil {
		return mapping{}, nil, err
	}
	a, err := view[nodeActivation](act.data, 0)
	if err != nil {
		act.unmap()
		return mapping{}, nil, fmt.Errorf("activation of memory %d: %w", memID, err)
	}
	return act, a, nil
}

// setIO takes the position the node's driver publishes. With the v1
// activation the node then tells the driver it follows it
// (active_driver_id); until then the driver skips it.
func (n *node) setIO(id, memID, off, size uint32) error {
	if id != ioPositionID {
		return nil
	}
	var pm mapping
	var pos *ioPosition
	if memID != idInvalid {
		var err error
		if pm, err = n.k.mem.mapID(memID, off, size); err != nil {
			return fmt.Errorf("pipewire: set_io position: %w", err)
		}
		if pos, err = view[ioPosition](pm.data, 0); err != nil {
			pm.unmap()
			return fmt.Errorf("pipewire: set_io position: %w", err)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.posMap.unmap()
	n.posMap, n.position = pm, pos
	if pos != nil && n.activation != nil && n.abi.v1() {
		n.activation.activeDriverID.Store(pos.clock.id.Load())
	}
	return nil
}

// command starts or stops the node's cycles. With the v1 activation a
// client-node v6 client moves its own activation out of INACTIVE when
// started and back when paused; before, the server alone keeps it.
func (n *node) command(cmd []byte) error {
	typ, body, ok := podAt(cmd, 0)
	if !ok || typ != typeObject || len(body) < 8 {
		return errors.New("pipewire: command: not an object")
	}
	if t := binary.LittleEndian.Uint32(body); t != commandTypeNode {
		return fmt.Errorf("pipewire: command of object type %#x", t)
	}
	id := binary.LittleEndian.Uint32(body[4:])
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.activation == nil {
		return fmt.Errorf("pipewire: command %d before the transport", id)
	}
	switch id {
	case commandStart:
		if !n.prepared && n.abi.v1() {
			n.activation.status.Store(activationFinished)
		}
		n.prepared = true
	case commandPause, commandSuspend:
		return n.unprepare()
	}
	return nil
}

// unprepare takes the node out of the graph; a synchronous node that
// was due to wake its peers wakes them, so the graph does not stall.
func (n *node) unprepare() error {
	if !n.prepared {
		return nil
	}
	n.prepared = false
	if !n.abi.v1() {
		return nil
	}
	old := n.activation.status.Swap(activationInactive)
	if old <= activationAwake && !n.async() {
		return n.triggerTargets(n.now())
	}
	return nil
}

// async is whether the driver resumes the node's peers itself, which
// only a v1 activation can say.
func (n *node) async() bool {
	return n.abi.v1() && n.activation.flags.Load()&activationFlagAsync != 0
}

func (n *node) portSetParam(seq, dir, port, id uint32, param []byte) error {
	if dir != n.direction || port != 0 {
		return n.k.reportError(n.id, seq, unix.EINVAL, fmt.Sprintf("port_set_param on port %d.%d, the node has only %d.0", dir, port, n.direction))
	}
	if id != paramFormat {
		return nil
	}
	n.mu.Lock()
	err := n.h.setFormat(param)
	if err == nil {
		n.clearBuffers()
	}
	n.mu.Unlock()
	if err != nil {
		return n.k.reportError(n.id, seq, unix.EINVAL, fmt.Sprintf("port_set_param(Format): %v", err))
	}
	return n.updatePort()
}

// clearBuffers drops the port's buffers and what was allocated for
// them; the links keep their io.
func (n *node) clearBuffers() {
	for i := range n.buffers {
		b := &n.buffers[i]
		if b.allocated {
			n.h.releaseData(b)
		}
		b.dataMap.unmap()
		b.meta.unmap()
	}
	n.buffers, n.nextBuffer = nil, 0
}

// portUseBuffers takes the buffers the server laid out for the port:
// their metas and chunks in its memory, their data either in it too or,
// with the alloc flag, provided by the handler and announced back.
func (n *node) portUseBuffers(seq uint32, f *podIter) error {
	dir, port, mixID, flags, count := f.uint(), f.uint(), f.uint(), f.uint(), f.uint()
	if f.err != nil {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.clearBuffers()
	refuse := func(err error) error {
		n.clearBuffers()
		return n.k.reportError(n.id, seq, unix.EINVAL, fmt.Sprintf("port_use_buffers: %v", err))
	}
	switch {
	case dir != n.direction || port != 0:
		return refuse(fmt.Errorf("port %d.%d, the node has only %d.0", dir, port, n.direction))
	case mixID != idInvalid:
		return refuse(fmt.Errorf("buffers for mix %d, the port's buffers are its own", mixID))
	case count > maxBuffers:
		return refuse(fmt.Errorf("%d buffers, more than %d", count, maxBuffers))
	}
	alloc := flags&buffersFlagAlloc != 0
	n.buffers = make([]buffer, 0, count)
	for i := range count {
		n.buffers = append(n.buffers, buffer{id: i, fd: -1})
		if err := n.layBuffer(&n.buffers[i], f, alloc); err != nil {
			if f.err != nil {
				n.clearBuffers()
				return nil
			}
			return refuse(fmt.Errorf("buffer %d: %w", i, err))
		}
	}
	if !alloc || count == 0 {
		return nil
	}
	return n.portBuffers()
}

// layBuffer reads one buffer's description and maps it.
func (n *node) layBuffer(b *buffer, f *podIter, alloc bool) error {
	memID, off, size, nMetas := f.uint(), f.uint(), f.uint(), f.uint()
	if f.err != nil {
		return f.err
	}
	if nMetas > maxMetas {
		return fmt.Errorf("%d metas, more than %d", nMetas, maxMetas)
	}
	var err error
	if b.meta, err = n.k.mem.mapID(memID, off, size); err != nil {
		return err
	}
	at := uint32(0)
	for range nMetas {
		typ, msize := f.id(), f.uint()
		if f.err != nil {
			return f.err
		}
		if err := b.layMeta(typ, at, msize); err != nil {
			return err
		}
		at += (msize + 7) &^ 7
	}
	if nDatas := f.uint(); f.err == nil && nDatas != 1 {
		return fmt.Errorf("%d data blocks, the port declares one", nDatas)
	}
	typ, dataID, dflags, mapOffset, maxsize := f.id(), f.uint(), f.uint(), f.uint(), f.uint()
	if f.err != nil {
		return f.err
	}
	if b.chunk, err = view[cSpaChunk](b.meta.data, at); err != nil {
		return fmt.Errorf("chunk: %w", err)
	}
	if alloc {
		if err := n.h.allocData(b); err != nil {
			return fmt.Errorf("allocate data: %w", err)
		}
		b.allocated = true
		return nil
	}
	if dflags&dataFlagWritable == 0 && n.direction == directionOutput {
		return fmt.Errorf("data flags %#x: not writable", dflags)
	}
	b.dataType, b.dataFlags, b.maxsize = typ, dflags, maxsize
	switch typ {
	case dataMemID:
		if b.dataMap, err = n.k.mem.mapID(dataID, mapOffset, maxsize); err != nil {
			return fmt.Errorf("data: %w", err)
		}
		b.data = b.dataMap.data
	case dataMemPtr:
		if uint64(dataID)+uint64(maxsize) > uint64(len(b.meta.data)) {
			return fmt.Errorf("data of %d bytes at %d of a %d-byte buffer", maxsize, dataID, len(b.meta.data))
		}
		b.data = b.meta.data[dataID : dataID+maxsize]
	default:
		return fmt.Errorf("data of type %d, neither MemId nor MemPtr", typ)
	}
	return nil
}

// layMeta points the buffer's meta of typ at its place in the buffer's
// memory; metas this client does not write stay unread.
func (b *buffer) layMeta(typ, at, size uint32) error {
	var err error
	switch typ {
	case metaHeader:
		b.header, err = view[cMetaHeader](b.meta.data, at)
	case metaVideoTransform:
		b.transform, err = view[uint32](b.meta.data, at)
	case metaVideoDamage:
		b.damage, err = viewSlice[cMetaRegion](b.meta.data, at, size/metaRegionSize)
	case metaBusy:
		b.busy, err = view[cMetaBusy](b.meta.data, at)
	}
	if err != nil {
		return fmt.Errorf("meta %d: %w", typ, err)
	}
	return nil
}

// portBuffers announces the data the handler allocated, one fd per
// buffer.
func (n *node) portBuffers() error {
	fields := []pod{podInt(int32(n.direction)), podInt(0), podInt(-1), podInt(int32(len(n.buffers)))}
	fds := make([]int, 0, len(n.buffers))
	for i := range n.buffers {
		b := &n.buffers[i]
		fields = append(fields, podInt(1), podID(b.dataType), podFd(len(fds)), podInt(int32(b.dataFlags)), podInt(0), podInt(int32(b.maxsize)))
		fds = append(fds, b.fd)
	}
	_, err := n.k.c.send(n.id, nodeMethodPortBuffers, podStruct(fields...).bytes(), fds)
	return err
}

// portSetIO takes the io area of one of the port's links.
func (n *node) portSetIO(seq, dir, port, mixID, id, memID, off, size uint32) error {
	if id != ioBuffers && id != ioAsyncBuffers {
		return nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	mx := n.findMix(dir, port, mixID)
	if mx == nil {
		return n.k.reportError(n.id, seq, unix.ENOENT, fmt.Sprintf("port_set_io on unknown mix %d.%d.%d", dir, port, mixID))
	}
	mx.io.unmap()
	mx.slots = [2]*cIOBuffers{}
	if memID == idInvalid {
		return nil
	}
	io, err := n.k.mem.mapID(memID, off, size)
	if err != nil {
		return n.k.reportError(n.id, seq, unix.EINVAL, fmt.Sprintf("port_set_io: %v", err))
	}
	if id == ioBuffers {
		slot, err := view[cIOBuffers](io.data, 0)
		if err != nil {
			io.unmap()
			return n.k.reportError(n.id, seq, unix.EINVAL, fmt.Sprintf("port_set_io buffers: %v", err))
		}
		mx.io, mx.slots = io, [2]*cIOBuffers{slot, slot}
		return nil
	}
	pair, err := viewSlice[cIOBuffers](io.data, 0, 2)
	if err != nil {
		io.unmap()
		return n.k.reportError(n.id, seq, unix.EINVAL, fmt.Sprintf("port_set_io async buffers: %v", err))
	}
	mx.io, mx.slots = io, [2]*cIOBuffers{&pair[n.direction], &pair[n.direction^1]}
	return nil
}

func (n *node) findMix(dir, port, mixID uint32) *mix {
	if dir != n.direction || port != 0 {
		return nil
	}
	for i := range n.mixes {
		if n.mixes[i].id == mixID {
			return &n.mixes[i]
		}
	}
	return nil
}

// portSetMixInfo adds a link of the port, or removes one when the peer
// is gone.
func (n *node) portSetMixInfo(seq, dir, port, mixID, peer uint32) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	mx := n.findMix(dir, port, mixID)
	switch {
	case dir != n.direction || port != 0:
		return n.k.reportError(n.id, seq, unix.ENOENT, fmt.Sprintf("port_set_mix_info on port %d.%d", dir, port))
	case peer == idInvalid && mx == nil:
		return n.k.reportError(n.id, seq, unix.EINVAL, fmt.Sprintf("removing unknown mix %d", mixID))
	case peer == idInvalid:
		mx.io.unmap()
		n.mixes = slices.DeleteFunc(n.mixes, func(m mix) bool { return m.id == mixID })
	case mx != nil:
		return n.k.reportError(n.id, seq, unix.EEXIST, fmt.Sprintf("mix %d exists", mixID))
	default:
		n.mixes = append(n.mixes, mix{id: mixID})
	}
	return nil
}

// setActivation adds a node to wake at the end of a cycle, or removes
// one when its memory is gone.
func (n *node) setActivation(m *message, f *podIter) error {
	nodeID, sigIdx, memID, off, size := f.uint(), f.fd(), f.uint(), f.uint(), f.uint()
	if f.err != nil {
		return nil
	}
	if memID == idInvalid {
		n.mu.Lock()
		defer n.mu.Unlock()
		i := slices.IndexFunc(n.targets, func(t target) bool { return t.id == nodeID })
		if i < 0 {
			return n.k.reportError(n.id, m.seq, unix.ENOENT, fmt.Sprintf("set_activation: removing unknown peer %d", nodeID))
		}
		n.targets[i].release()
		n.targets = slices.Delete(n.targets, i, i+1)
		return nil
	}
	fd, err := m.takeFd(sigIdx)
	if err != nil {
		return fmt.Errorf("pipewire: set_activation: %w", err)
	}
	act, a, err := n.mapActivation(memID, off, size)
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("pipewire: set_activation of %d: %w", nodeID, err)
	}
	n.mu.Lock()
	n.targets = append(n.targets, target{id: nodeID, act: act, activation: a, fd: fd})
	n.mu.Unlock()
	return nil
}

func (t *target) release() {
	t.act.unmap()
	_ = unix.Close(t.fd)
}

// realtime runs the node's cycles: each wake-up of the read fd is one.
func (n *node) realtime() {
	defer close(n.rtDone)
	var count [8]byte
	for {
		if _, err := n.rtFile.Read(count[:]); err != nil {
			if !errors.Is(err, os.ErrClosed) {
				n.k.abort(fmt.Errorf("pipewire: wait for the node's cycle: %w", err))
			}
			return
		}
		n.mu.Lock()
		err := n.cycle()
		dirty := n.portDirty
		n.portDirty = false
		n.mu.Unlock()
		if err == nil && dirty {
			err = n.updatePort()
		}
		if err != nil {
			n.k.abort(err)
			return
		}
	}
}

// cycle is one process cycle, as libpipewire's process_node runs it:
// with the v1 activation claim the trigger, process, mark the cycle
// finished and, unless the driver resumes the node asynchronously, wake
// its peers; before 1.2 a woken node always runs and wakes them. It
// allocates nothing.
func (n *node) cycle() error {
	a := n.activation
	v1 := n.abi.v1()
	if v1 && !a.status.CompareAndSwap(activationTriggered, activationAwake) {
		return nil
	}
	if !v1 {
		a.status.Store(activationAwake)
	}
	a.awakeTime.Store(n.now())
	a.bits.And(^uint32(activationBitPendingSync))
	status := int32(statusHaveData)
	if n.prepared {
		status = n.h.process(n)
	}
	a.state[0].status.Store(status)
	now := n.now()
	finished := true
	if v1 {
		finished = a.status.CompareAndSwap(activationAwake, activationFinished)
	} else {
		a.status.Store(activationFinished)
	}
	a.finishTime.Store(now)
	if finished && !n.async() {
		return n.triggerTargets(now)
	}
	return nil
}

// now is CLOCK_MONOTONIC in nanoseconds, the clock the graph keeps its
// times in.
func (n *node) now() uint64 {
	_ = unix.ClockGettime(unix.CLOCK_MONOTONIC, &n.ts)
	return uint64(n.ts.Nano())
}

// triggerTargets counts the node off each target's pending dependencies
// and wakes the target that has none left.
func (n *node) triggerTargets(now uint64) error {
	for i := range n.targets {
		t := &n.targets[i]
		if t.activation.state[0].pending.Add(-1) != 0 {
			continue
		}
		if !n.abi.v1() {
			t.activation.status.Store(activationTriggered)
		} else if !t.activation.status.CompareAndSwap(activationNotTriggered, activationTriggered) {
			continue
		}
		t.activation.signalTime.Store(now)
		if _, err := unix.Write(t.fd, n.wake[:]); err != nil {
			return fmt.Errorf("pipewire: wake node %d: %w", t.id, err)
		}
	}
	return nil
}

// cycleSlot is the io slot this cycle writes, by the driver's cycle
// parity; false without a position. Before 1.2 the cycle count is
// padding, but such a server only hands out plain io, whose two slots
// are one.
func (n *node) cycleSlot() (int, bool) {
	if n.position == nil {
		return 0, false
	}
	return int(n.position.clock.cycle.Load() & 1), true
}

// dequeue takes the next buffer no consumer holds, marking it busy;
// nil when there is none.
func (n *node) dequeue() *buffer {
	for range n.buffers {
		b := &n.buffers[n.nextBuffer]
		n.nextBuffer = (n.nextBuffer + 1) % len(n.buffers)
		if b.busy == nil || b.busy.count.Add(1) == 1 {
			return b
		}
		b.busy.count.Add(^uint32(0))
	}
	return nil
}

// release gives up a dequeued buffer's busy mark.
func (b *buffer) release() {
	if b.busy != nil {
		b.busy.count.Add(^uint32(0))
	}
}

// queue hands b to every link in this cycle's slot.
func (n *node) queue(slot int, b *buffer) {
	b.release()
	n.writeSlots(slot, statusHaveData, b.id)
}

// idle tells every link this cycle brings no buffer.
func (n *node) idle(slot int) { n.writeSlots(slot, statusNeedData, idInvalid) }

func (n *node) writeSlots(slot int, status int32, id uint32) {
	for i := range n.mixes {
		if s := n.mixes[i].slots[slot]; s != nil {
			s.bufferID.Store(id)
			s.status.Store(status)
		}
	}
}

// stop ends the cycles and waits for the realtime goroutine.
func (n *node) stop() {
	if n.rtFile == nil {
		return
	}
	_ = n.rtFile.Close()
	<-n.rtDone
}

// deactivate takes the node out of the graph before it goes away.
func (n *node) deactivate() error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.activation == nil {
		return nil
	}
	return n.unprepare()
}

// release frees everything the node holds. The event loop and the
// cycles must have stopped.
func (n *node) release() {
	n.clearBuffers()
	for i := range n.mixes {
		n.mixes[i].io.unmap()
	}
	n.mixes = nil
	for i := range n.targets {
		n.targets[i].release()
	}
	n.targets = nil
	n.posMap.unmap()
	n.position, n.activation = nil, nil
	n.act.unmap()
	if n.writeFd >= 0 {
		_ = unix.Close(n.writeFd)
		n.writeFd = -1
	}
}
