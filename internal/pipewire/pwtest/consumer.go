package pwtest

import (
	"encoding/binary"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Consumer is a reference consumer: a libpipewire input stream, linked
// to a node's first output port, reporting the size of every non-empty
// buffer it dequeues.
type Consumer struct {
	frames chan uint32
}

// consumerName is the node name every reference consumer takes.
const consumerName = "pwtest-consumer"

var (
	callbackOnce sync.Once
	onProcess    uintptr
	consumers    sync.Map
	nextHandle   uintptr
	handleMu     sync.Mutex
)

// streamEvents mirrors struct pw_stream_events (version 2) on LP64,
// up to process.
type streamEvents struct {
	version uint32
	_       uint32
	_       [7]uintptr
	process uintptr
	_       [3]uintptr
}

// pwBuffer, spaBuffer, spaData and spaChunk mirror the heads of the
// libpipewire structs a stream dequeues.
type pwBuffer struct{ buffer *spaBuffer }

type spaBuffer struct {
	_     [2]uint32
	_     unsafe.Pointer
	datas *spaData
}

type spaData struct {
	_     [2]uint32
	_     int64
	_     [2]uint32
	_     unsafe.Pointer
	chunk *spaChunk
}

type spaChunk struct{ _, size uint32 }

// consumerState is one consumer's stream as its loop thread sees it.
type consumerState struct {
	lib    *Lib
	stream uintptr
	frames chan uint32
}

// Consume connects a reference consumer offering any size of BGRx video
// and links node's output_0 to it with pw-link. The daemon must be
// running; the consumer goes away with the test.
func Consume(t *testing.T, node string) *Consumer {
	t.Helper()
	l, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	loop, err := l.StartLoop()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loop.Stop)
	callbackOnce.Do(func() { onProcess = purego.NewCallback(process) })
	handleMu.Lock()
	nextHandle++
	handle := nextHandle
	handleMu.Unlock()
	st := &consumerState{lib: l, frames: make(chan uint32, 64)}
	consumers.Store(handle, st)
	t.Cleanup(func() { consumers.Delete(handle) })
	events := &streamEvents{version: 2, process: onProcess}
	var pinner runtime.Pinner
	pinner.Pin(events)
	t.Cleanup(pinner.Unpin)
	var rc int32
	loop.Do(func() {
		props := l.PropertiesNew("media.type=Video media.category=Capture node.name=" + consumerName)
		st.stream = l.StreamNewSimple(loop.Handle(), consumerName, props, unsafe.Pointer(events), handle) //nolint:gosec
		rc = WithPods([][]byte{anyBGRx()}, func(params unsafe.Pointer, n uint32) int32 {
			return l.StreamConnect(st.stream, 0, 0xffffffff, 1<<2, params, n)
		})
	})
	t.Cleanup(func() {
		loop.Do(func() {
			l.StreamDisconnect(st.stream)
			l.StreamDestroy(st.stream)
		})
	})
	if rc < 0 {
		t.Fatalf("pwtest: connect the consumer: %d", rc)
	}
	link(t, node+":output_0", consumerName+":input_0")
	return &Consumer{frames: st.frames}
}

// process is the consumers' pw_stream_events.process.
func process(handle uintptr) {
	v, ok := consumers.Load(handle)
	if !ok {
		return
	}
	st, ok := v.(*consumerState)
	if !ok {
		return
	}
	raw := st.lib.StreamDequeue(st.stream)
	if raw == nil {
		return
	}
	defer st.lib.StreamQueue(st.stream, raw)
	size := (*pwBuffer)(raw).buffer.datas.chunk.size
	if size == 0 {
		return
	}
	select {
	case st.frames <- size:
	default:
	}
}

// Frame waits for the next non-empty buffer and returns its size.
func (c *Consumer) Frame(t *testing.T) uint32 {
	t.Helper()
	select {
	case size := <-c.frames:
		return size
	case <-time.After(5 * time.Second):
		t.Fatal("pwtest: the consumer received no frame")
		return 0
	}
}

// link joins two ports with pw-link, retrying while they appear.
func link(t *testing.T, output, input string) {
	t.Helper()
	bin, err := exec.LookPath("pw-link")
	if err != nil {
		t.Fatalf("pw-link not on PATH: %v", err)
	}
	var out []byte
	for range 100 {
		out, err = exec.Command(bin, output, input).CombinedOutput() //nolint:gosec
		if err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pw-link %s %s: %v: %s", output, input, err, out)
}

// anyBGRx is an EnumFormat pod for raw BGRx video of any size and rate,
// as an application's consumer offers.
func anyBGRx() []byte {
	const (
		objectFormat, paramEnumFormat           = 0x40003, 3
		typeID, typeRectangle, typeFraction     = 3, 10, 11
		typeObject, typeChoice, choiceRange     = 15, 19, 1
		keyMediaType, keySubtype                = 1, 2
		keyFormat, keySize, keyRate             = 0x20001, 0x20003, 0x20004
		mediaVideo, subtypeRaw, videoFormatBGRx = 2, 1, 8
	)
	le := binary.LittleEndian
	var body []byte
	prop := func(key uint32, typ uint32, value []byte) {
		body = le.AppendUint32(body, key)
		body = le.AppendUint32(body, 0)
		body = le.AppendUint32(body, uint32(len(value)))
		body = le.AppendUint32(body, typ)
		body = append(body, value...)
		for len(body)%8 != 0 {
			body = append(body, 0)
		}
	}
	pairs := func(vs ...uint32) []byte {
		var b []byte
		for _, v := range vs {
			b = le.AppendUint32(b, v)
		}
		return b
	}
	rangeOf := func(childType uint32, values ...uint32) []byte {
		return append(pairs(choiceRange, 0, 8, childType), pairs(values...)...)
	}
	body = append(pairs(objectFormat, paramEnumFormat), body...)
	prop(keyMediaType, typeID, pairs(mediaVideo))
	prop(keySubtype, typeID, pairs(subtypeRaw))
	prop(keyFormat, typeID, pairs(videoFormatBGRx))
	prop(keySize, typeChoice, rangeOf(typeRectangle, 640, 480, 1, 1, 8192, 8192))
	prop(keyRate, typeChoice, rangeOf(typeFraction, 30, 1, 0, 1, 1000, 1))
	return append(pairs(uint32(len(body)), typeObject), body...)
}
