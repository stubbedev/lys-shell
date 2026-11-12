package recorder

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/atomicfile"
	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/media/mux"
)

// ringChunk is the unit the replay ring stores encoded bytes in.
const ringChunk = 256 << 10

// errReplayEmpty is a save before the first keyframe reached the ring.
var errReplayEmpty = errors.New("recorder: the replay holds no keyframe yet")

// queue is a growable circular buffer: it allocates only while it
// grows past its largest size so far.
type queue[T any] struct {
	buf  []T
	head int
	n    int
}

func (q *queue[T]) len() int { return q.n }

func (q *queue[T]) at(i int) *T { return &q.buf[(q.head+i)%len(q.buf)] }

func (q *queue[T]) push(v T) {
	if q.n == len(q.buf) {
		grown := make([]T, max(2*len(q.buf), 16))
		for i := range q.n {
			grown[i] = *q.at(i)
		}
		q.buf, q.head = grown, 0
	}
	q.buf[(q.head+q.n)%len(q.buf)] = v
	q.n++
}

func (q *queue[T]) popFront() T {
	v := *q.at(0)
	var zero T
	*q.at(0) = zero
	q.head = (q.head + 1) % len(q.buf)
	q.n--
	return v
}

// ringEntry is one encoded sample in the ring: where its bytes start
// in the ring's byte stream, how many, its time, track and whether it
// is a keyframe.
type ringEntry struct {
	off   int64
	size  int
	pts   time.Duration
	track int
	key   bool
}

// keyMark is a keyframe's entry, by its sequence number, and its time.
type keyMark struct {
	seq uint64
	pts time.Duration
}

// replayRing keeps the last span of a recording's encoded samples, in
// the order they arrive, cut on keyframes: it holds from the newest
// keyframe at least span before the newest frame, so a save starts on
// a keyframe and runs at least span, and at most span and one GOP. The
// bytes live in fixed chunks recycled as old GOPs leave, so once warm
// a sample is stored without allocating.
type replayRing struct {
	span   time.Duration
	tracks []mux.Track
	onKey  func()

	mu      sync.Mutex
	entries queue[ringEntry]
	keys    queue[keyMark]
	first   uint64
	chunks  queue[[]byte]
	free    queue[[]byte]
	base    int64
	end     int64
	held    int
	pins    int
}

func newReplayRing(span time.Duration, tracks []mux.Track, onKey func()) *replayRing {
	return &replayRing{span: span, tracks: tracks, onKey: onKey}
}

func (r *replayRing) write(s mux.Sample, key bool) error {
	r.mu.Lock()
	video := s.Track == videoTrack
	if video {
		r.evict(s.PTS)
	}
	r.entries.push(ringEntry{off: r.end, size: len(s.Data), pts: s.PTS, track: s.Track, key: key})
	r.store(s.Data)
	marked := video && key
	if marked {
		r.keys.push(keyMark{seq: r.first + intconv.Must[uint64](r.entries.len()-1), pts: s.PTS})
	}
	r.mu.Unlock()
	if marked && r.onKey != nil {
		r.onKey()
	}
	return nil
}

// store appends data to the byte stream, taking a chunk when the tail
// one is full.
func (r *replayRing) store(data []byte) {
	for len(data) > 0 {
		within := int(r.end - r.base - int64(r.chunks.len()-1)*ringChunk)
		if r.chunks.len() == 0 || within == ringChunk {
			r.chunks.push(r.takeChunk())
			within = 0
		}
		n := copy((*r.chunks.at(r.chunks.len() - 1))[within:], data)
		data = data[n:]
		r.end += int64(n)
	}
}

func (r *replayRing) takeChunk() []byte {
	if r.free.len() > 0 {
		return r.free.popFront()
	}
	r.held += ringChunk
	return make([]byte, ringChunk)
}

// evict drops, before a frame at newest is stored, the GOPs before the
// newest keyframe that still leaves span up to it, and the chunks only
// they used: the ring never holds more than span and one GOP.
func (r *replayRing) evict(newest time.Duration) {
	for r.keys.len() >= 2 && newest-r.keys.at(1).pts >= r.span {
		r.keys.popFront()
		for cut := r.keys.at(0).seq; r.first < cut; r.first++ {
			r.entries.popFront()
		}
	}
	if r.entries.len() == 0 {
		return
	}
	for live := r.entries.at(0).off; r.chunks.len() > 1 && r.base+ringChunk <= live; r.base += ringChunk {
		c := r.chunks.popFront()
		if r.pins == 0 {
			r.free.push(c)
			continue
		}
		r.held -= ringChunk
	}
}

// Bytes is the memory the ring holds.
func (r *replayRing) Bytes() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return intconv.Must[uint64](r.held)
}

// close lets go of every chunk.
func (r *replayRing) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries, r.keys, r.chunks, r.free = queue[ringEntry]{}, queue[keyMark]{}, queue[[]byte]{}, queue[[]byte]{}
	r.held = 0
	return nil
}

func (r *replayRing) discard() error { return r.close() }

// ringView is what a save reads: the entries from the oldest keyframe,
// and the chunks holding them, which stay unrecycled until it is done.
type ringView struct {
	entries []ringEntry
	chunks  [][]byte
	base    int64
	scratch []byte
}

// pin takes a view of the ring from its oldest keyframe on.
func (r *replayRing) pin() (*ringView, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start := -1
	for i := range r.entries.len() {
		if e := r.entries.at(i); e.track == videoTrack && e.key {
			start = i
			break
		}
	}
	if start < 0 {
		return nil, errReplayEmpty
	}
	v := &ringView{entries: make([]ringEntry, 0, r.entries.len()-start), chunks: make([][]byte, r.chunks.len()), base: r.base}
	for i := start; i < r.entries.len(); i++ {
		v.entries = append(v.entries, *r.entries.at(i))
	}
	for i := range v.chunks {
		v.chunks[i] = *r.chunks.at(i)
	}
	r.pins++
	return v, nil
}

func (r *replayRing) unpin() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pins--
}

// bytes is e's data: a slice of its chunk, or a copy into the view's
// scratch when it spans two.
func (v *ringView) bytes(e ringEntry) []byte {
	rel := e.off - v.base
	idx, within := int(rel/ringChunk), int(rel%ringChunk)
	if within+e.size <= ringChunk {
		return v.chunks[idx][within : within+e.size]
	}
	v.scratch = v.scratch[:0]
	for need := e.size; need > 0; idx, within = idx+1, 0 {
		n := min(need, ringChunk-within)
		v.scratch = append(v.scratch, v.chunks[idx][within:within+n]...)
		need -= n
	}
	return v.scratch
}

// save muxes the ring from its oldest keyframe to now into path as
// format, through a temporary renamed into place. Capture goes on
// meanwhile: the ring is locked only to take the view.
func (r *replayRing) save(path string, format config.RecorderFormat) error {
	v, err := r.pin()
	if err != nil {
		return err
	}
	defer r.unpin()
	start := v.entries[0].pts
	err = atomicfile.WriteSeekable(path, 0o644, func(w io.WriteSeeker) error {
		m, err := newMuxer(format, w, r.tracks...)
		if err != nil {
			return err
		}
		for _, e := range v.entries {
			if e.pts < start {
				continue
			}
			if err := m.WriteSample(mux.Sample{Track: e.track, PTS: e.pts - start, Data: v.bytes(e)}); err != nil {
				return err
			}
		}
		return m.Close()
	})
	if err != nil {
		return fmt.Errorf("recorder: save the replay to %s: %w", path, err)
	}
	return nil
}
