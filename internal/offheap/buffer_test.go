package offheap

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stubbedev/memory-go"
)

// pattern is n bytes that differ at every offset a resize could
// misplace.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + i/251)
	}
	return b
}

// writeChunks writes src in chunk-sized writes, as a transfer does.
func writeChunks(t *testing.T, b *Buffer, src []byte, chunk int) {
	t.Helper()
	for off := 0; off < len(src); off += chunk {
		end := min(off+chunk, len(src))
		if n, err := b.Write(src[off:end]); err != nil || n != end-off {
			t.Fatalf("write at %d: %d, %v", off, n, err)
		}
	}
}

func newBuffer(t *testing.T, mem *memory.Shared, limit int) *Buffer {
	t.Helper()
	b, err := NewBuffer(mem, limit)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A payload grown through every resize comes out byte for byte, in one
// allocation of exactly its length.
func TestBufferTakesTheBytesWrittenExactly(t *testing.T) {
	for _, size := range []int{1, 5000, bufferChunk, bufferChunk + 1, 4<<20 + 17} {
		var mem memory.Shared
		src := pattern(size)
		b := newBuffer(t, &mem, 8<<20)
		writeChunks(t, b, src, 32<<10-3)
		got, err := b.Take()
		if err != nil {
			t.Fatal(err)
		}
		if got.Len() != size || !bytes.Equal(got.Elems(), src) {
			t.Fatalf("%d bytes: took %d, equal %v", size, got.Len(), bytes.Equal(got.Elems(), src))
		}
		if st := mem.Stats(); st.Live != 1 {
			t.Fatalf("%d bytes: %d allocations, want the taken one", size, st.Live)
		}
		if err := Free(&mem, got); err != nil {
			t.Fatal(err)
		}
		if st := mem.Stats(); st.Live != 0 {
			t.Fatalf("%d bytes: %d allocations left", size, st.Live)
		}
		if _, err := b.Write([]byte("x")); !errors.Is(err, ErrBufferDone) {
			t.Fatalf("a write after Take: %v", err)
		}
	}
}

// Nothing written takes as nothing, holding no memory.
func TestBufferEmptyTakeHoldsNothing(t *testing.T) {
	var mem memory.Shared
	got, err := newBuffer(t, &mem, 16).Take()
	if err != nil || got.Len() != 0 || mem.Stats().Live != 0 {
		t.Fatalf("took %d bytes, %v, %d allocations", got.Len(), err, mem.Stats().Live)
	}
}

// A write past the most is refused whole, keeping what fit, and Free
// lets go of it all; a second Free and a Take after it do nothing.
func TestBufferRefusesPastItsMostAndFreesThePartialPayload(t *testing.T) {
	var mem memory.Shared
	b := newBuffer(t, &mem, 100<<10)
	writeChunks(t, b, pattern(96<<10), 32<<10)
	if n, err := b.Write(make([]byte, 8<<10)); n != 0 || !errors.Is(err, ErrBufferFull) {
		t.Fatalf("a write past the most: %d, %v", n, err)
	}
	if st := mem.Stats(); st.Live != 1 || st.Bytes < 96<<10 || st.Bytes > 100<<10+4096 {
		t.Fatalf("partial payload: %d allocations of %d bytes", st.Live, st.Bytes)
	}
	if err := b.Free(); err != nil {
		t.Fatal(err)
	}
	if st := mem.Stats(); st.Live != 0 || st.Bytes != 0 {
		t.Fatalf("after Free: %d allocations of %d bytes", st.Live, st.Bytes)
	}
	if err := b.Free(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Take(); !errors.Is(err, ErrBufferDone) {
		t.Fatalf("Take after Free: %v", err)
	}
	if _, err := b.Write([]byte("x")); !errors.Is(err, ErrBufferDone) || mem.Stats().Live != 0 {
		t.Fatalf("a write after Free: %v, %d allocations", err, mem.Stats().Live)
	}
}

// A writer still running when its buffer is freed gets an error, never
// freed memory.
func TestBufferFreedMidTransferRefusesTheRest(t *testing.T) {
	var mem memory.Shared
	b := newBuffer(t, &mem, 8<<20)
	src := pattern(32 << 10)
	stop := make(chan error)
	go func() {
		for {
			if _, err := b.Write(src); err != nil {
				stop <- err
				return
			}
		}
	}()
	for mem.Stats().Live == 0 {
		continue
	}
	if err := b.Free(); err != nil {
		t.Fatal(err)
	}
	if err := <-stop; !errors.Is(err, ErrBufferDone) && !errors.Is(err, ErrBufferFull) {
		t.Fatalf("the writer stopped with %v", err)
	}
	if err := b.Free(); err != nil || mem.Stats().Live != 0 {
		t.Fatalf("%v, %d allocations", err, mem.Stats().Live)
	}
}

func TestNewBufferRefusesNoRoom(t *testing.T) {
	var mem memory.Shared
	for _, limit := range []int{0, -1} {
		if _, err := NewBuffer(&mem, limit); err == nil {
			t.Errorf("a buffer of at most %d bytes was made", limit)
		}
	}
}
