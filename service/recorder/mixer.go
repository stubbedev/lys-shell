package recorder

import (
	"time"

	"github.com/stubbedev/lys-lib/media/opus"

	"github.com/stubbedev/lys-shell/internal/audiomix"
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

// newMixer mixes sources of the audio track's shape.
func newMixer(sources int) *audiomix.Mixer {
	return audiomix.New(sources, audioChannels, int(audioBacklog*audioRate/time.Second))
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
