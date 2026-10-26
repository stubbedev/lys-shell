// Package audiomix sums several sources of interleaved 16-bit PCM into
// one stream, sample by sample with clipping, padding with silence what
// a source has not delivered yet.
package audiomix

import (
	"encoding/binary"
	"math"
)

const sampleBytes = 2

// Mixer sums sources of interleaved s16le PCM into frames. Each source
// queues what it delivered; a frame takes the oldest samples of every
// source, missing ones as silence.
type Mixer struct {
	queues [][]int16
	// partial holds a source's trailing byte of a sample split across
	// two chunks.
	partial  [][]byte
	channels int
	limit    int
	sum      []int32
}

// New mixes sources of channels interleaved channels, each queueing at
// most backlog frames ahead of Mix: a source running fast is trimmed
// from its oldest samples rather than growing without end.
func New(sources, channels, backlog int) *Mixer {
	return &Mixer{
		queues:   make([][]int16, sources),
		partial:  make([][]byte, sources),
		channels: channels,
		limit:    backlog * channels,
	}
}

// Push queues source's chunk of little-endian samples.
func (m *Mixer) Push(source int, chunk []byte) {
	q := m.queues[source]
	if p := m.partial[source]; len(p) > 0 && len(chunk) > 0 {
		q = append(q, int16(binary.LittleEndian.Uint16([]byte{p[0], chunk[0]})))
		chunk = chunk[1:]
		m.partial[source] = p[:0]
	}
	for len(chunk) >= sampleBytes {
		q = append(q, int16(binary.LittleEndian.Uint16(chunk)))
		chunk = chunk[sampleBytes:]
	}
	m.partial[source] = append(m.partial[source], chunk...)
	if over := len(q) - m.limit; over > 0 {
		// Whole stereo frames, so the channels stay in place.
		over += over % m.channels
		q = append(q[:0], q[over:]...)
	}
	m.queues[source] = q
}

// Queued is the samples source has queued.
func (m *Mixer) Queued(source int) int { return len(m.queues[source]) }

// Mix fills frame, whole interleaved frames, with the sum of the
// sources' oldest samples, clipped, and drops them from the queues.
func (m *Mixer) Mix(frame []int16) {
	sum := m.sum[:0]
	sum = append(sum, make([]int32, len(frame))...)
	m.sum = sum
	for i, q := range m.queues {
		n := min(len(q), len(sum))
		for j, v := range q[:n] {
			sum[j] += int32(v)
		}
		m.queues[i] = append(q[:0], q[n:]...)
	}
	for i, v := range sum {
		frame[i] = int16(min(max(v, math.MinInt16), math.MaxInt16))
	}
}
