package pwtest

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"github.com/stubbedev/lys-shell/internal/clib"
)

// Lib is libpipewire bound through purego: the reference
// implementation the tests' consumers run on, so the producer is
// checked against the library every other PipeWire client uses.
type Lib struct {
	PropertiesNew    func(s string) uintptr
	StreamNewSimple  func(loop uintptr, name string, props uintptr, events unsafe.Pointer, data uintptr) uintptr
	StreamConnect    func(stream uintptr, direction, target, flags uint32, params unsafe.Pointer, n uint32) int32
	StreamDisconnect func(stream uintptr) int32
	StreamDestroy    func(stream uintptr)
	StreamUpdate     func(stream uintptr, params unsafe.Pointer, n uint32) int32
	StreamDequeue    func(stream uintptr) unsafe.Pointer
	StreamQueue      func(stream uintptr, buffer unsafe.Pointer) int32

	initLib     func(argc, argv unsafe.Pointer)
	loopNew     func(props unsafe.Pointer) *cPwLoop
	loopDestroy func(loop *cPwLoop)
}

var (
	libOnce sync.Once
	lib     *Lib
	errLib  error
)

// Load binds libpipewire and initializes it, once per process.
func Load() (*Lib, error) {
	libOnce.Do(func() { lib, errLib = bind() })
	return lib, errLib
}

func bind() (*Lib, error) {
	h, err := clib.Open(clib.SystemCandidates("libpipewire-0.3.so.0"))
	if err != nil {
		return nil, fmt.Errorf("load libpipewire: %w", err)
	}
	l := &Lib{}
	for _, f := range []struct {
		fn   any
		name string
	}{
		{&l.initLib, "pw_init"},
		{&l.loopNew, "pw_loop_new"},
		{&l.loopDestroy, "pw_loop_destroy"},
		{&l.PropertiesNew, "pw_properties_new_string"},
		{&l.StreamNewSimple, "pw_stream_new_simple"},
		{&l.StreamConnect, "pw_stream_connect"},
		{&l.StreamDisconnect, "pw_stream_disconnect"},
		{&l.StreamDestroy, "pw_stream_destroy"},
		{&l.StreamUpdate, "pw_stream_update_params"},
		{&l.StreamDequeue, "pw_stream_dequeue_buffer"},
		{&l.StreamQueue, "pw_stream_queue_buffer"},
	} {
		sym, err := purego.Dlsym(h, f.name)
		if err != nil {
			return nil, fmt.Errorf("libpipewire: %s: %w", f.name, err)
		}
		purego.RegisterFunc(f.fn, sym)
	}
	l.initLib(nil, nil)
	return l, nil
}

// cPwLoop mirrors struct pw_loop; control is a spa_loop_control
// interface, whose methods pw_loop_enter / iterate / leave (static
// inlines in C) call through.
type cPwLoop struct {
	_       [2]unsafe.Pointer
	control *cSpaInterface
	_       unsafe.Pointer
	_       *byte
}

// cSpaInterface mirrors struct spa_interface.
type cSpaInterface struct {
	_       *byte
	_       uint32
	methods *cLoopControlMethods
	data    unsafe.Pointer
}

// cLoopControlMethods mirrors the head of struct
// spa_loop_control_methods.
type cLoopControlMethods struct {
	_       uint32
	_       uintptr
	_       uintptr
	enter   uintptr
	leave   uintptr
	iterate uintptr
}

func (l *cPwLoop) enter() { purego.SyscallN(l.control.methods.enter, uintptr(l.control.data)) }
func (l *cPwLoop) leave() { purego.SyscallN(l.control.methods.leave, uintptr(l.control.data)) }

func (l *cPwLoop) iterate(timeoutMs int) {
	purego.SyscallN(l.control.methods.iterate, uintptr(l.control.data), uintptr(timeoutMs))
}

// Loop runs one pw_loop on a goroutine locked to its OS thread, and
// every libpipewire call of its streams happens there: stream events
// arrive on a Go thread, inside iterate, which purego's cgo-less
// callbacks need.
type Loop struct {
	lib  *Lib
	loop *cPwLoop
	work chan func()
	done chan struct{}
}

// iterateMs is how long an idle iterate waits before taking queued
// work.
const iterateMs = 20

// StartLoop starts a loop thread.
func (l *Lib) StartLoop() (*Loop, error) {
	t := &Loop{lib: l, work: make(chan func()), done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer close(t.done)
		t.loop = l.loopNew(nil)
		if t.loop == nil {
			ready <- errors.New("pipewire: cannot create a loop")
			return
		}
		t.loop.enter()
		ready <- nil
		for {
			select {
			case fn, ok := <-t.work:
				if !ok {
					t.loop.leave()
					l.loopDestroy(t.loop)
					return
				}
				fn()
			default:
				t.loop.iterate(iterateMs)
			}
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return t, nil
}

// Handle is the loop for StreamNewSimple.
func (t *Loop) Handle() uintptr { return uintptr(unsafe.Pointer(t.loop)) } //nolint:gosec

// Do runs fn on the loop thread and waits for it.
func (t *Loop) Do(fn func()) {
	finished := make(chan struct{})
	t.work <- func() {
		defer close(finished)
		fn()
	}
	<-finished
}

// Stop ends the loop once its queued work ran.
func (t *Loop) Stop() {
	close(t.work)
	<-t.done
}

// WithPods hands C an array of pointers to the pods, pinned for the
// call (libpipewire copies the params).
func WithPods(pods [][]byte, call func(params unsafe.Pointer, n uint32) int32) int32 {
	var pinner runtime.Pinner
	defer pinner.Unpin()
	ptrs := make([]unsafe.Pointer, len(pods))
	for i, b := range pods {
		pinner.Pin(&b[0])
		ptrs[i] = unsafe.Pointer(&b[0]) //nolint:gosec
	}
	pinner.Pin(&ptrs[0])
	return call(unsafe.Pointer(&ptrs[0]), uint32(len(ptrs))) //nolint:gosec
}
