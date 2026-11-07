package recorder

import (
	"time"

	"github.com/stubbedev/lys-lib/media/mux"

	"github.com/stubbedev/lys-shell/config"
)

// replay is an armed instant replay: a recording whose samples go to
// the ring instead of a file.
type replay struct {
	rec  *recording
	ring *replayRing
}

// startReplay starts recording parts into a ring holding span; onKey
// runs after each keyframe enters it.
func startReplay(parts recordingParts, span time.Duration, term func(reason string), onKey func()) (*replay, error) {
	rp := &replay{}
	rec, err := startPipeline(parts, term, func(tracks []mux.Track) (sampleSink, error) {
		rp.ring = newReplayRing(span, tracks, onKey)
		return rp.ring, nil
	})
	if err != nil {
		return nil, err
	}
	rp.rec = rec
	return rp, nil
}

// Save writes the ring to path as format; capture goes on.
func (rp *replay) Save(path string, format config.RecorderFormat) error {
	return rp.ring.save(path, format)
}

// Bytes is the memory the ring holds.
func (rp *replay) Bytes() uint64 { return rp.ring.Bytes() }

// Stop ends the capture and frees the ring.
func (rp *replay) Stop() error { return rp.rec.Stop() }
