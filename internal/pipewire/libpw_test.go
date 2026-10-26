package pipewire

import (
	"unsafe"

	"github.com/stubbedev/lys-shell/internal/pipewire/pwtest"
)

// libpw is pwtest.Lib under the names the reference consumer in
// daemon_test.go calls libpipewire by.
type libpw struct {
	propertiesNew    func(s string) uintptr
	streamNewSimple  func(loop uintptr, name string, props uintptr, events unsafe.Pointer, data uintptr) uintptr
	streamConnect    func(stream uintptr, direction, target, flags uint32, params unsafe.Pointer, n uint32) int32
	streamDisconnect func(stream uintptr) int32
	streamDestroy    func(stream uintptr)
	streamUpdate     func(stream uintptr, params unsafe.Pointer, n uint32) int32
	streamDequeue    func(stream uintptr) unsafe.Pointer
	streamQueue      func(stream uintptr, buffer unsafe.Pointer) int32
}

func load() (*libpw, error) {
	l, err := pwtest.Load()
	if err != nil {
		return nil, err
	}
	return &libpw{
		propertiesNew:    l.PropertiesNew,
		streamNewSimple:  l.StreamNewSimple,
		streamConnect:    l.StreamConnect,
		streamDisconnect: l.StreamDisconnect,
		streamDestroy:    l.StreamDestroy,
		streamUpdate:     l.StreamUpdate,
		streamDequeue:    l.StreamDequeue,
		streamQueue:      l.StreamQueue,
	}, nil
}

// loopThread is a pwtest.Loop under the names daemon_test.go uses.
type loopThread struct {
	t    *pwtest.Loop
	loop uintptr
}

func startLoop(*libpw) (*loopThread, error) {
	l, err := pwtest.Load()
	if err != nil {
		return nil, err
	}
	t, err := l.StartLoop()
	if err != nil {
		return nil, err
	}
	return &loopThread{t: t, loop: t.Handle()}, nil
}

func (t *loopThread) do(fn func()) { t.t.Do(fn) }
func (t *loopThread) stop()        { t.t.Stop() }

func withPods(pods [][]byte, call func(params unsafe.Pointer, n uint32) int32) int32 {
	return pwtest.WithPods(pods, call)
}

const (
	idAny          = idInvalid
	flagMapBuffers = 1 << 2
)

// cStreamEvents mirrors struct pw_stream_events (version 2) on LP64.
type cStreamEvents struct {
	version      uint32
	_            uint32
	destroy      uintptr
	stateChanged uintptr
	controlInfo  uintptr
	ioChanged    uintptr
	paramChanged uintptr
	addBuffer    uintptr
	removeBuffer uintptr
	process      uintptr
	drained      uintptr
	command      uintptr
	triggerDone  uintptr
}

// cPwBuffer mirrors struct pw_buffer.
type cPwBuffer struct {
	buffer   *cSpaBuffer
	userData unsafe.Pointer
	size     uint64
}

// cSpaBuffer, cSpaMeta and cSpaData mirror spa/buffer as libpipewire
// hands them to a stream.
type cSpaBuffer struct {
	nMetas uint32
	_      uint32
	metas  *cSpaMeta
	datas  *cSpaData
}

type cSpaMeta struct {
	typ, size uint32
	data      unsafe.Pointer
}

type cSpaData struct {
	typ   uint32
	_     uint32
	fd    int64
	_     [2]uint32
	data  unsafe.Pointer
	chunk *cSpaChunk
}
