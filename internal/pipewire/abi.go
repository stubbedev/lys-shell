package pipewire

import "sync/atomic"

// nodeActivation is the head of struct pw_node_activation
// (src/pipewire/private.h): everything up to and including the
// position, which is all a node reads or writes. The server's struct
// continues past it; the transport's size must cover at least this
// much. This and the other shared-memory mirrors here are ABI, stable
// from PipeWire 1.2.0 (the oldest server this client accepts) to 1.6;
// abi_test.go pins every offset against the C compiler's. The fields
// the server and its clients share across processes are atomics.
type nodeActivation struct {
	status         atomic.Uint32
	bits           atomic.Uint32
	state          [2]activationState
	signalTime     atomic.Uint64
	awakeTime      atomic.Uint64
	finishTime     atomic.Uint64
	_              uint64
	_              [activationVersionOffset - 64]byte
	clientVersion  atomic.Uint32
	serverVersion  atomic.Uint32
	activeDriverID atomic.Uint32
	_              uint32
	flags          atomic.Uint32
	position       ioPosition
}

// activationVersionOffset is where client_version sits, past the
// reposition and segment updates and the segment owners.
const activationVersionOffset = 540

// activationState is struct pw_node_activation_state: the status of
// the node's last cycle, and how many of its dependencies are pending.
type activationState struct {
	status  atomic.Int32
	_       int32
	pending atomic.Int32
}

// Activation statuses (PW_NODE_ACTIVATION_*).
const (
	activationNotTriggered = 0
	activationTriggered    = 1
	activationAwake        = 2
	activationFinished     = 3
	activationInactive     = 4
)

const (
	// activationBitPendingSync is the pending_sync bitfield: a node that
	// does not take part in transport sync clears it every cycle.
	activationBitPendingSync = 1 << 1
	// activationFlagAsync marks a node the driver schedules
	// asynchronously: it triggers no peers itself.
	activationFlagAsync = 1 << 1
	// activationVersion is PW_VERSION_NODE_ACTIVATION, the semantics
	// this client speaks (status changes by compare-and-swap; a
	// client-node v6 client moves itself out of INACTIVE).
	activationVersion = 1
)

// ioPosition is struct spa_io_position; only its clock is read.
type ioPosition struct {
	clock ioClock
	_     [ioPositionSize - ioClockSize]byte
}

// ioClock is struct spa_io_clock: the driver's id, and past its name
// and timing the count of graph cycles.
type ioClock struct {
	_     uint32
	id    atomic.Uint32
	_     [ioClockCycleOffset - 8]byte
	cycle atomic.Uint32
	_     [ioClockSize - ioClockCycleOffset - 4]byte
}

const (
	ioPositionSize     = 1688
	ioClockSize        = 160
	ioClockCycleOffset = 148
)

// cIOBuffers is struct spa_io_buffers, the area a link exchanges buffer
// ids through; spa_io_async_buffers is two of them.
type cIOBuffers struct {
	status   atomic.Int32
	bufferID atomic.Uint32
}

// spa_io_buffers statuses (SPA_STATUS_*).
const (
	statusNeedData = 1 << 0
	statusHaveData = 1 << 1
)

// IO area ids (enum spa_io_type).
const (
	ioBuffers      = 1
	ioPositionID   = 7
	ioAsyncBuffers = 10
)

// cMetaHeader mirrors struct spa_meta_header.
type cMetaHeader struct {
	flags, offset uint32
	pts           int64
	dtsOffset     int64
	seq           uint64
}

// cMetaRegion mirrors struct spa_meta_region.
type cMetaRegion struct {
	x, y          int32
	width, height uint32
}

// cMetaBusy mirrors struct spa_meta_busy: how many holders a buffer
// has.
type cMetaBusy struct {
	_     uint32
	count atomic.Uint32
}

// cSpaChunk mirrors struct spa_chunk: where a buffer's valid data is.
type cSpaChunk struct {
	offset, size uint32
	stride       int32
	_            int32
}
