package audiomix

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"
)

func pcmBytes(samples ...int16) []byte {
	b := make([]byte, 0, len(samples)*2)
	for _, s := range samples {
		b = binary.LittleEndian.AppendUint16(b, uint16(s))
	}
	return b
}

// frameLen is a 20 ms stereo frame at 48 kHz.
const frameLen = 960 * 2

func frame() []int16 { return make([]int16, frameLen) }

func newMixer(sources int) *Mixer { return New(sources, 2, 24000) }

// Sources are summed sample by sample, clipped at the 16-bit range;
// what a source has not delivered is silence.
func TestMixerSumsClipsAndPads(t *testing.T) {
	m := newMixer(2)
	m.Push(0, pcmBytes(1000, -1000, 30000, -30000, 7, 8))
	m.Push(1, pcmBytes(500, 500, 10000, -10000))
	out := frame()
	m.Mix(out)
	if want := []int16{1500, -500, math.MaxInt16, math.MinInt16, 7, 8, 0, 0}; !slices.Equal(out[:8], want) {
		t.Errorf("mixed %v, want %v", out[:8], want)
	}
	if slices.ContainsFunc(out[8:], func(v int16) bool { return v != 0 }) {
		t.Error("the undelivered rest is not silence")
	}
	m.Mix(out)
	if slices.ContainsFunc(out, func(v int16) bool { return v != 0 }) {
		t.Error("mixed samples were not consumed")
	}
}

// A frame takes exactly one frame of samples; the rest waits for the
// next.
func TestMixerKeepsTheRemainder(t *testing.T) {
	m := newMixer(1)
	in := make([]int16, frameLen+2)
	in[len(in)-2], in[len(in)-1] = 11, 12
	m.Push(0, pcmBytes(in...))
	out := frame()
	m.Mix(out)
	m.Mix(out)
	if out[0] != 11 || out[1] != 12 || out[2] != 0 {
		t.Errorf("second frame starts %v, want the two left over", out[:3])
	}
}

// A sample split across two chunks is joined, not dropped or shifted.
func TestMixerJoinsASplitSample(t *testing.T) {
	m := newMixer(1)
	b := pcmBytes(-2, 300, 5)
	m.Push(0, b[:3])
	m.Push(0, b[3:])
	out := frame()
	m.Mix(out)
	if !slices.Equal(out[:3], []int16{-2, 300, 5}) {
		t.Errorf("mixed %v, want -2 300 5", out[:3])
	}
}

// A source the mixer falls behind is trimmed from its oldest samples
// to the backlog, whole stereo frames at a time.
func TestMixerBoundsTheBacklog(t *testing.T) {
	m := newMixer(1)
	over := m.limit + 6
	in := make([]int16, over)
	for i := range in {
		in[i] = int16(i % 2)
	}
	in[6], in[7] = 100, 101
	m.Push(0, pcmBytes(in...))
	if got := len(m.queues[0]); got != m.limit {
		t.Fatalf("queued %d samples, want the %d limit", got, m.limit)
	}
	out := frame()
	m.Mix(out)
	if out[0] != 100 || out[1] != 101 {
		t.Errorf("first kept frame %v, want the oldest six samples dropped", out[:2])
	}
}

// Queued counts what a source has waiting, a split sample's first byte
// not yet among it.
func TestMixerQueued(t *testing.T) {
	m := newMixer(2)
	m.Push(1, pcmBytes(1, 2, 3)[:5])
	if a, b := m.Queued(0), m.Queued(1); a != 0 || b != 2 {
		t.Errorf("queued %d and %d, want 0 and 2", a, b)
	}
}
