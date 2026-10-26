package recorder

import (
	"encoding/binary"
	"math"
	"time"

	"github.com/stubbedev/lys-lib/media/opus"
)

// The audio track's shape: 48 kHz stereo, 20 ms Opus packets. The
// sources are captured in exactly this format (the server converts), so
// the mixer only sums.
const (
	audioRate      = opus.SampleRate
	audioChannels  = 2
	audioFrame     = opus.FrameSize
	audioFrameTime = time.Second * audioFrame / audioRate
	audioBitrate   = 128_000
	sampleBytes    = 2
)

// audioLatency is how far the packets trail the clock while recording:
// a source's chunks arrive in fragments, so the mixer takes a frame
// only once every source has had time to deliver it.
const audioLatency = 100 * time.Millisecond

// audioBacklog bounds what a source may queue ahead of the mixer: a
// source running fast, or one the clock outran, is trimmed from its
// oldest samples rather than growing without end.
const audioBacklog = 500 * time.Millisecond

// mixer sums the sources' interleaved stereo s16le PCM into frames.
// Each source queues what it delivered; a frame takes the oldest
// samples of every source, missing ones as silence.
type mixer struct {
	queues [][]int16
	// partial holds a source's trailing byte of a sample split across
	// two chunks.
	partial [][]byte
	limit   int
}

func newMixer(sources int) *mixer {
	return &mixer{
		queues:  make([][]int16, sources),
		partial: make([][]byte, sources),
		limit:   int(audioBacklog * audioRate / time.Second * audioChannels),
	}
}

// push queues source's chunk of little-endian samples.
func (m *mixer) push(source int, chunk []byte) {
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
		over += over % audioChannels
		q = append(q[:0], q[over:]...)
	}
	m.queues[source] = q
}

// mix fills frame (audioFrame stereo samples) with the sum of the
// sources' oldest samples, clipped, and drops them from the queues.
func (m *mixer) mix(frame []int16) {
	var sum [audioFrame * audioChannels]int32
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

// framesDue is how many frames follow the emitted ones up to elapsed
// less latency on the recording's clock.
func framesDue(elapsed, latency time.Duration, emitted int) int {
	upTo := elapsed - latency
	if upTo <= 0 {
		return 0
	}
	return max(int(upTo/audioFrameTime)-emitted, 0)
}
