package pipewire

import (
	"encoding/binary"
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// shared is zeroed memory standing in for a mapping the server shares,
// aligned for any of its structs.
func shared(size uintptr) []byte {
	words := make([]uint64, (size+7)/8)
	return unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*8)
}

// manyDamage reports more changed rectangles than a buffer has room for,
// from a slice it owns.
type manyDamage struct{ rects []Rect }

func (m *manyDamage) Fill(dst []byte) ([]Rect, bool) {
	dst[0] = 0x33
	return m.rects, true
}

// cycleRig is a started node over Go memory: its activation, two buffers
// with every meta the stream writes and the Busy meta, and one link's
// async io.
type cycleRig struct {
	n   *node
	act *nodeActivation
	io  []cIOBuffers
}

func newCycleRig(t testing.TB, src Source) *cycleRig {
	t.Helper()
	act, err := view[nodeActivation](shared(unsafe.Sizeof(nodeActivation{})), 0)
	if err != nil {
		t.Fatal(err)
	}
	act.serverVersion.Store(activationVersion)
	act.flags.Store(activationFlagAsync)
	act.status.Store(activationFinished)
	p := &Producer{
		cfg:    Config{Width: 64, Height: 32, Stride: 256, Format: VideoBGRx, FPS: 30, Transform: 3, Source: src},
		damage: make([]Rect, 0, maxDamageRegions),
	}
	n := &node{h: p, abi: abiV1, direction: directionOutput, activation: act, position: &act.position, prepared: true}
	n.wake[0] = 1
	for i := range uint32(2) {
		b := buffer{id: i, meta: mapping{data: shared(1024)}, data: make([]byte, 256*32), fd: -1}
		at := uint32(0)
		for _, m := range [][2]uint32{{metaHeader, metaHeaderSize}, {metaVideoTransform, metaTransformSize}, {metaVideoDamage, 4 * metaRegionSize}, {metaBusy, metaBusySize}} {
			if err := b.layMeta(m[0], at, m[1]); err != nil {
				t.Fatal(err)
			}
			at += (m[1] + 7) &^ 7
		}
		if b.chunk, err = view[cSpaChunk](b.meta.data, at); err != nil {
			t.Fatal(err)
		}
		n.buffers = append(n.buffers, b)
	}
	io, err := viewSlice[cIOBuffers](shared(16), 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	n.mixes = []mix{{id: 0, slots: [2]*cIOBuffers{&io[directionOutput], &io[directionInput]}}}
	return &cycleRig{n: n, act: act, io: io}
}

// run triggers one cycle of the driver's next position, as the server
// would, and runs it.
func (r *cycleRig) run(t testing.TB) {
	r.act.position.clock.cycle.Add(1)
	r.act.status.Store(activationTriggered)
	if err := r.n.cycle(); err != nil {
		t.Fatal(err)
	}
}

// ioValue is what an io slot holds.
type ioValue struct {
	status int32
	id     uint32
}

func loadIO(s *cIOBuffers) ioValue { return ioValue{s.status.Load(), s.bufferID.Load()} }

// slot is what the link reads next cycle from this one.
func (r *cycleRig) slot() ioValue {
	return loadIO(&r.io[directionOutput^r.act.position.clock.cycle.Load()&1])
}

// A cycle, the frame copy and the metas included, allocates nothing:
// the damage of a source reporting more rectangles than fit collapses
// into the stream's own slice.
func TestACycleAllocatesNothing(t *testing.T) {
	src := &manyDamage{rects: []Rect{{0, 0, 1, 1}, {1, 1, 1, 1}, {2, 2, 1, 1}, {3, 3, 1, 1}, {4, 4, 1, 1}, {5, 5, 1, 1}}}
	r := newCycleRig(t, src)
	if allocs := mallocs(func() {
		for range 200 {
			r.run(t)
		}
	}); allocs != 0 {
		t.Errorf("200 cycles from the first allocate %d times", allocs)
	}
	if s := r.slot(); s.status != statusHaveData || r.act.status.Load() != activationFinished {
		t.Errorf("the measured cycles sent nothing: slot %+v, status %d", s, r.act.status.Load())
	}
	if d := r.n.buffers[0].damage; d[0] != (cMetaRegion{0, 0, 64, 32}) || d[1] != (cMetaRegion{}) {
		t.Errorf("damage = %v, want the whole frame", d)
	}
}

// The activations of a 1.2 and later server and of a 1.0 one.
var (
	abiV1 = activationABI{activation: activationVersion, clientNode: 6}
	abiV0 = activationABI{activation: 0, clientNode: 5}
)

// version0 makes the rig a 1.0 server's: no cycle count, so one plain
// io slot.
func (r *cycleRig) version0() {
	r.n.abi = abiV0
	r.n.mixes[0].slots = [2]*cIOBuffers{&r.io[0], &r.io[0]}
}

// mallocs counts the heap allocations fn makes, its first call
// included (testing.AllocsPerRun discards a warm-up run).
func mallocs(fn func()) uint64 {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.Mallocs - before.Mallocs
}

func BenchmarkCycle(b *testing.B) {
	r := newCycleRig(b, solid{1})
	b.ReportAllocs()
	for b.Loop() {
		r.run(b)
	}
}

// Each cycle sends the next buffer in the slot the link reads, with
// the chunk and metas filled; a buffer the consumer still holds (Busy)
// is passed over until it is released; a cycle the server did not
// trigger does nothing.
func TestCyclesHandOutFreeBuffersInTurn(t *testing.T) {
	r := newCycleRig(t, damaged{})
	var ids []uint32
	for range 3 {
		r.run(t)
		ids = append(ids, r.slot().id)
	}
	if ids[0] != 0 || ids[1] != 1 || ids[2] != 0 {
		t.Errorf("buffers %v, want 0 1 0", ids)
	}
	b := r.n.buffers[0]
	if b.chunk.size != 256*32 || b.chunk.stride != 256 || b.data[0] != 0x42 || *b.transform != 3 || b.header.seq != 2 {
		t.Errorf("chunk %+v, first byte %#x, transform %d, seq %d", *b.chunk, b.data[0], *b.transform, b.header.seq)
	}

	r.n.buffers[1].busy.count.Store(1)
	r.run(t)
	if id := r.slot().id; id != 0 {
		t.Errorf("a held buffer was sent: %d", id)
	}
	r.n.buffers[1].busy.count.Store(0)
	r.run(t)
	if id := r.slot().id; id != 1 {
		t.Errorf("a released buffer was passed over: %d", id)
	}

	before := r.slot()
	r.act.position.clock.cycle.Add(2)
	r.act.status.Store(activationNotTriggered)
	if err := r.n.cycle(); err != nil || r.slot() != before || r.act.status.Load() != activationNotTriggered {
		t.Errorf("an untriggered cycle ran: %v, slot %+v, status %d", err, r.slot(), r.act.status.Load())
	}
}

// A node the driver schedules synchronously counts itself off each
// target and wakes the one with nothing left pending; an asynchronous
// node leaves its targets to the driver. A 1.0 server has no
// asynchronous nodes: whatever its flags hold, the node wakes its peers.
func TestASynchronousCycleWakesItsPeers(t *testing.T) {
	for name, tc := range map[string]struct {
		abi            activationABI
		flags, pending uint32
		wantPending    int32
		wantWoken      bool
	}{
		"synchronous, last dependency":  {abiV1, 0, 1, 0, true},
		"synchronous, more outstanding": {abiV1, 0, 2, 1, false},
		"asynchronous":                  {abiV1, activationFlagAsync, 1, 1, false},
		"1.0, last dependency":          {abiV0, activationFlagAsync, 1, 0, true},
		"1.0, more outstanding":         {abiV0, 0, 2, 1, false},
	} {
		r := newCycleRig(t, solid{1})
		if tc.abi == abiV0 {
			r.version0()
		}
		r.act.flags.Store(tc.flags)
		peer, err := view[nodeActivation](shared(unsafe.Sizeof(nodeActivation{})), 0)
		if err != nil {
			t.Fatal(err)
		}
		peer.state[0].pending.Store(int32(tc.pending))
		efd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
		if err != nil {
			t.Fatal(err)
		}
		r.n.targets = []target{{id: 9, activation: peer, fd: efd}}
		r.run(t)
		var count [8]byte
		_, readErr := unix.Read(efd, count[:])
		woken := readErr == nil && count[0] == 1
		_ = unix.Close(efd)
		if peer.state[0].pending.Load() != tc.wantPending || woken != tc.wantWoken || (peer.status.Load() == activationTriggered) != tc.wantWoken {
			t.Errorf("%s: pending %d, woken %v (%v), status %d", name, peer.state[0].pending.Load(), woken, readErr, peer.status.Load())
		}
		if tc.wantWoken && peer.signalTime.Load() == 0 {
			t.Errorf("%s: no signal time", name)
		}
		if !tc.wantWoken && !errors.Is(readErr, unix.EAGAIN) {
			t.Errorf("%s: eventfd read %v", name, readErr)
		}
	}
}

// Start moves the node out of INACTIVE and Pause back into it; a
// paused node's triggers run nothing.
func TestStartAndPauseMoveTheActivation(t *testing.T) {
	r := newCycleRig(t, solid{1})
	r.n.prepared = false
	r.act.status.Store(activationInactive)
	command := func(id uint32) {
		t.Helper()
		if err := r.n.command(object(commandTypeNode, id)); err != nil {
			t.Fatal(err)
		}
	}
	command(commandStart)
	if r.act.status.Load() != activationFinished || !r.n.prepared {
		t.Errorf("after Start: status %d", r.act.status.Load())
	}
	command(commandPause)
	if r.act.status.Load() != activationInactive || r.n.prepared {
		t.Errorf("after Pause: status %d", r.act.status.Load())
	}
	r.act.status.Store(activationTriggered)
	if err := r.n.cycle(); err != nil || r.slot().status != 0 {
		t.Errorf("a paused node sent a buffer: %v %+v", err, r.slot())
	}
}

// A link's async io is two slots: in cycle c the producer writes the
// one its consumer reads in cycle c+1, never the one being read; a
// plain io is one slot for every cycle. A link the node does not have
// is refused to the server.
func TestALinksIOSlotsFollowTheCycleParity(t *testing.T) {
	a, b := connPair(t)
	fd, mem, err := memfd(int(pageSize))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Munmap(mem)
	n := &node{k: &core{c: a}, id: firstObjectID, direction: directionOutput, mixes: []mix{{id: 5}}}
	if err := n.k.mem.add(0, fd, memFlagReadable|memFlagWritable); err != nil {
		t.Fatal(err)
	}
	defer n.release()
	defer n.k.mem.close()
	slot := func(i int) ioValue {
		return ioValue{int32(binary.LittleEndian.Uint32(mem[8*i:])), binary.LittleEndian.Uint32(mem[8*i+4:])}
	}
	if err := n.portSetIO(0, directionOutput, 0, 5, ioAsyncBuffers, 0, 0, 16); err != nil {
		t.Fatal(err)
	}
	for parity := range 2 {
		n.writeSlots(parity, statusHaveData, uint32(10+parity))
		if read := (parity + 1) & 1; slot(read) != (ioValue{statusHaveData, uint32(10 + parity)}) {
			t.Errorf("cycle parity %d wrote %v %v, want slot %d", parity, slot(0), slot(1), read)
		}
	}
	if err := n.portSetIO(0, directionOutput, 0, 5, ioBuffers, 0, 64, 8); err != nil {
		t.Fatal(err)
	}
	n.writeSlots(0, statusNeedData, idInvalid)
	n.writeSlots(1, statusHaveData, 3)
	if s := slot(8); s != (ioValue{statusHaveData, 3}) {
		t.Errorf("plain io = %v", s)
	}

	if err := n.portSetIO(9, directionOutput, 0, 6, ioBuffers, 0, 0, 8); err != nil {
		t.Fatal(err)
	}
	m, err := b.recv()
	if err != nil {
		t.Fatal(err)
	}
	f := structFields(m.body)
	if id, seq, res, msg := f.uint(), f.uint(), f.int(), f.string(); m.opcode != coreMethodError || id != firstObjectID || seq != 9 || res != -int32(unix.ENOENT) || msg != "port_set_io on unknown mix 1.0.6" {
		t.Errorf("unknown link: %d %d %d %d %q", m.opcode, id, seq, res, msg)
	}
}

// A 1.0 server wakes a node without marking it triggered and keeps its
// status itself: the node runs on every wake-up, into the one io slot,
// and Start and Pause leave the status alone, where the 1.2 activation
// would have refused the untriggered cycle.
func TestAVersion0NodeRunsOnEveryWakeUp(t *testing.T) {
	r := newCycleRig(t, solid{1})
	r.version0()
	for i := range 3 {
		r.act.status.Store(activationNotTriggered)
		if err := r.n.cycle(); err != nil {
			t.Fatal(err)
		}
		if s := loadIO(&r.io[0]); s.status != statusHaveData || s.id != uint32(i%2) || r.act.status.Load() != activationFinished {
			t.Errorf("wake-up %d: slot %+v, status %d", i, s, r.act.status.Load())
		}
	}
	r.act.status.Store(activationNotTriggered)
	for _, id := range []uint32{commandPause, commandStart} {
		if err := r.n.command(object(commandTypeNode, id)); err != nil {
			t.Fatal(err)
		}
		if st := r.act.status.Load(); st != activationNotTriggered {
			t.Errorf("command %d moved the status to %d", id, st)
		}
	}
}
