package offheap

import (
	"errors"
	"fmt"
	"sync"

	"github.com/stubbedev/memory-go"
)

// bufferChunk is the first allocation a Buffer makes: a few of a
// transfer's reads, past the 8 KiB from which memory-go maps whole
// pages, so every later growth is a page-granular resize.
const bufferChunk = 64 << 10

// ErrBufferFull reports a write past the most a Buffer holds.
var ErrBufferFull = errors.New("offheap: buffer full")

// ErrBufferDone reports a write to, or a Take of, a Buffer already
// taken or freed.
var ErrBufferDone = errors.New("offheap: buffer already taken or freed")

// Buffer is an io.Writer whose bytes mem holds, for a payload of
// unknown size: it grows by doubling, from bufferChunk, never past its
// most. It holds the only handle to its memory, so no stale handle
// outlives a resize. One goroutine may write while another takes or
// frees it: a write after that fails with ErrBufferDone instead of
// touching freed memory.
type Buffer struct {
	mem   *memory.Shared
	limit int

	mu   sync.Mutex
	buf  memory.Slice[byte]
	n    int
	done bool
}

// NewBuffer is an empty Buffer holding at most limit bytes, which is
// positive.
func NewBuffer(mem *memory.Shared, limit int) (*Buffer, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("offheap: a buffer of at most %d bytes holds nothing", limit)
	}
	return &Buffer{mem: mem, limit: limit}, nil
}

// Write appends p whole, or nothing: ErrBufferFull past the most,
// ErrBufferDone once taken or freed.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return 0, ErrBufferDone
	}
	if len(p) > b.limit-b.n {
		return 0, fmt.Errorf("%w: %d bytes after %d of at most %d", ErrBufferFull, len(p), b.n, b.limit)
	}
	if need := b.n + len(p); need > b.buf.Len() {
		size := min(max(need, 2*b.buf.Len(), bufferChunk), b.limit)
		grown, err := memory.ResizeSlice(b.mem, b.buf, size)
		b.buf = grown
		if err != nil {
			return 0, fmt.Errorf("offheap: growing a buffer of %d bytes to %d: %w", b.n, size, err)
		}
	}
	copy(b.buf.Elems()[b.n:], p)
	b.n += len(p)
	return len(p), nil
}

// Take hands over the bytes written, trimmed to their length, as the
// caller's slice to free; empty when nothing was written. The Buffer
// holds nothing afterwards and refuses writes.
func (b *Buffer) Take() (memory.Slice[byte], error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return memory.Slice[byte]{}, ErrBufferDone
	}
	b.done = true
	trimmed, err := memory.ResizeSlice(b.mem, b.buf, b.n)
	b.buf = memory.Slice[byte]{}
	if err != nil {
		return memory.Slice[byte]{}, errors.Join(fmt.Errorf("offheap: trimming a buffer to its %d bytes: %w", b.n, err), Free(b.mem, trimmed))
	}
	return trimmed, nil
}

// Free frees whatever the Buffer holds and refuses writes afterwards.
// Freeing it again, or after Take, does nothing.
func (b *Buffer) Free() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.done {
		return nil
	}
	b.done = true
	buf := b.buf
	b.buf = memory.Slice[byte]{}
	return Free(b.mem, buf)
}
