package recorder

import (
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"sync"
	"time"

	"github.com/stubbedev/lys-lib/gpu"
	"github.com/stubbedev/lys-lib/media/mux"
	"github.com/stubbedev/lys-lib/media/opus"

	"github.com/stubbedev/lys-shell/config"
)

// minFrameGap is the least time between two video frames: the muxers
// keep a 90 kHz clock, and a frame right after a resume could
// otherwise land on its predecessor's tick.
const minFrameGap = time.Millisecond

// Track indexes in the file.
const (
	videoTrack = 0
	audioTrack = 1
)

// videoEncoder captures the screen and encodes it: the GPU in
// production. It belongs to the recording's video goroutine.
type videoEncoder interface {
	// ParameterSets are the stream's SPS and PPS NAL units.
	ParameterSets() (sps, pps []byte)
	// SetOverlay draws img over every following frame, scaled into dst.
	SetOverlay(img *image.RGBA, dst image.Rectangle) error
	// Frame captures a frame stamped pts and returns the packets that
	// finished meanwhile; none when the capture was skipped.
	Frame(pts time.Duration) ([]gpu.Packet, error)
	// Flush returns the frames still in flight.
	Flush() ([]gpu.Packet, error)
	Close()
}

// camera is a webcam delivering frames already padded to the overlay's
// aspect. Frames closes when the camera stops, Err then saying why.
type camera interface {
	Frames() <-chan *image.RGBA
	// Recycle hands a frame back once it is uploaded.
	Recycle(*image.RGBA)
	Err() error
	Close()
}

// audioCapture is one running audio source; Close stops its chunks.
type audioCapture interface{ Close() }

// startAudio starts a source whose chunks of 48 kHz stereo s16le go to
// onData, from any goroutine.
type startAudio func(onData func([]byte)) (audioCapture, error)

// fileMuxer is mux.MP4 or mux.MKV.
type fileMuxer interface {
	WriteSample(mux.Sample) error
	Close() error
}

// newMuxer writes format's container to w.
func newMuxer(format config.RecorderFormat, w io.Writer, tracks ...mux.Track) (fileMuxer, error) {
	switch format {
	case config.RecorderFormatMp4:
		return mux.NewMP4(w, tracks...)
	case config.RecorderFormatMkv:
		return mux.NewMKV(w, tracks...)
	}
	return nil, fmt.Errorf("recorder: no muxer for output format %q", format)
}

// recordingParts are what a recording runs on; it owns them all from
// the start, the camera with its rectangle on the frame.
type recordingParts struct {
	video   videoEncoder
	cam     camera
	camRect image.Rectangle
	audio   []startAudio
	fps     uint32
	now     func() time.Time
}

// recording is one running capture: a video goroutine capturing and
// encoding at the frame rate, an audio goroutine mixing and encoding
// every 20 ms, both writing to one muxer on one clock.
type recording struct {
	p    recordingParts
	file *os.File
	path string
	term func(reason string)

	muxMu sync.Mutex
	mux   fileMuxer

	// mu guards the clock and the mixer: the audio sources push from
	// their own goroutines.
	mu       sync.Mutex
	clock    activeClock
	mixer    *mixer
	captures []audioCapture

	opus *opus.Encoder

	stop     chan struct{}
	loops    sync.WaitGroup
	failOnce sync.Once
	stopOnce sync.Once
	stopErr  error
	// drainErr is the audio loop's last drain on stop, read after it
	// ends.
	drainErr error
}

// startRecording opens opts.OutputPath and starts recording parts into
// it. On failure everything parts held is released and the file
// removed.
func startRecording(parts recordingParts, opts Options, term func(reason string)) (_ *recording, err error) {
	r := &recording{p: parts, path: opts.OutputPath, term: term, stop: make(chan struct{})}
	defer func() {
		if err != nil {
			r.release()
		}
	}()
	sps, pps := parts.video.ParameterSets()
	tracks := []mux.Track{mux.H264{SPS: sps, PPS: pps}}
	if len(parts.audio) > 0 {
		tracks = append(tracks, mux.Opus{Channels: audioChannels, PreSkip: opus.PreSkip})
		if r.opus, err = opus.NewEncoder(audioChannels, audioBitrate, opus.ConstrainedVBR); err != nil {
			return nil, fmt.Errorf("recorder: Opus encoder: %w", err)
		}
		r.mixer = newMixer(len(parts.audio))
	}
	r.file, err = os.OpenFile(opts.OutputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // the user's recording, readable as their other videos
	if err != nil {
		return nil, fmt.Errorf("recorder: create the output: %w", err)
	}
	if r.mux, err = newMuxer(opts.Format, r.file, tracks...); err != nil {
		return nil, fmt.Errorf("recorder: %s: %w", opts.OutputPath, err)
	}
	r.clock = newActiveClock(parts.now())
	for i, start := range parts.audio {
		c, err := start(func(chunk []byte) { r.pushAudio(i, chunk) })
		if err != nil {
			return nil, err
		}
		r.captures = append(r.captures, c)
	}
	r.loops.Add(1)
	go r.videoLoop()
	if r.mixer != nil {
		r.loops.Add(1)
		go r.audioLoop()
	}
	return r, nil
}

// release closes what startRecording acquired when it fails, the
// empty output included.
func (r *recording) release() {
	for _, c := range r.captures {
		c.Close()
	}
	if r.p.cam != nil {
		r.p.cam.Close()
	}
	r.p.video.Close()
	if r.file != nil {
		_ = r.file.Close()
		_ = os.Remove(r.path)
	}
}

// fail reports the first error that ends the recording on its own. The
// report runs off the loop: the state's teardown calls Stop, which waits
// for the loops.
func (r *recording) fail(err error) {
	r.failOnce.Do(func() { go r.term(err.Error()) })
}

func (r *recording) writeSample(s mux.Sample) error {
	r.muxMu.Lock()
	defer r.muxMu.Unlock()
	if err := r.mux.WriteSample(s); err != nil {
		return fmt.Errorf("recorder: write %s: %w", r.path, err)
	}
	return nil
}

func (r *recording) writeVideo(pkts []gpu.Packet) error {
	for _, p := range pkts {
		if err := r.writeSample(mux.Sample{Track: videoTrack, PTS: p.PTS, Data: p.Data}); err != nil {
			return err
		}
	}
	return nil
}

// videoTime is the clock now, and whether it is stopped.
func (r *recording) videoTime() (time.Duration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.clock.elapsed(r.p.now()), r.clock.paused
}

// videoLoop captures a frame every frame period while the clock runs,
// and uploads each new webcam frame as the overlay.
func (r *recording) videoLoop() {
	defer r.loops.Done()
	ticker := time.NewTicker(time.Second / time.Duration(max(r.p.fps, 1)))
	defer ticker.Stop()
	var frames <-chan *image.RGBA
	if r.p.cam != nil {
		frames = r.p.cam.Frames()
	}
	last := time.Duration(-1)
	for {
		select {
		case <-r.stop:
			return
		case img, ok := <-frames:
			if !ok {
				r.fail(fmt.Errorf("recorder: the webcam stopped: %w", r.p.cam.Err()))
				return
			}
			err := r.p.video.SetOverlay(img, r.p.camRect)
			r.p.cam.Recycle(img)
			if err != nil {
				r.fail(fmt.Errorf("recorder: overlay the webcam: %w", err))
				return
			}
			continue
		case <-ticker.C:
		}
		pts, paused := r.videoTime()
		if paused || (last >= 0 && pts-last < minFrameGap) {
			continue
		}
		pkts, err := r.p.video.Frame(pts)
		if err == nil {
			err = r.writeVideo(pkts)
		}
		if err != nil {
			r.fail(err)
			return
		}
		last = pts
	}
}

// pushAudio queues a source's chunk; a paused recording drops it.
func (r *recording) pushAudio(source int, chunk []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.clock.paused {
		r.mixer.push(source, chunk)
	}
}

// audioLoop mixes and encodes the frames due every packet period,
// trailing the clock by audioLatency (a stopped clock makes none due);
// on stop it drains up to the clock itself.
func (r *recording) audioLoop() {
	defer r.loops.Done()
	ticker := time.NewTicker(audioFrameTime)
	defer ticker.Stop()
	emitted := 0
	pcm := make([]int16, audioFrame*audioChannels)
	var packet []byte
	pump := func(latency time.Duration) error {
		for {
			r.mu.Lock()
			if framesDue(r.clock.elapsed(r.p.now()), latency, emitted) == 0 {
				r.mu.Unlock()
				return nil
			}
			r.mixer.mix(pcm)
			r.mu.Unlock()
			var err error
			if packet, err = r.opus.Encode(packet[:0], pcm); err != nil {
				return fmt.Errorf("recorder: Opus-encode the audio at %v: %w", time.Duration(emitted)*audioFrameTime, err)
			}
			if err := r.writeSample(mux.Sample{Track: audioTrack, PTS: time.Duration(emitted) * audioFrameTime, Data: packet}); err != nil {
				return err
			}
			emitted++
		}
	}
	for {
		select {
		case <-r.stop:
			r.drainErr = pump(0)
			return
		case <-ticker.C:
			if err := pump(audioLatency); err != nil {
				r.fail(err)
				return
			}
		}
	}
}

// SetPaused stops or restarts the clock. While it stands, the sources'
// chunks are dropped and no frame falls due; what they queued before
// the pause still fills the frames up to it, so the audio after the
// pause stays aligned with the video.
func (r *recording) SetPaused(paused bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if paused {
		r.clock.pause(r.p.now())
		return
	}
	r.clock.resume(r.p.now())
}

// Stop stops the sources, lets the loops finish their last frames,
// drains the encoder's frames in flight into the file, and finalizes
// it. A second call returns the first's result.
func (r *recording) Stop() error {
	r.stopOnce.Do(func() { r.stopErr = r.finish() })
	return r.stopErr
}

func (r *recording) finish() error {
	for _, c := range r.captures {
		c.Close()
	}
	close(r.stop)
	r.loops.Wait()
	if r.p.cam != nil {
		r.p.cam.Close()
	}
	errs := []error{r.drainErr}
	pkts, err := r.p.video.Flush()
	if err != nil {
		errs = append(errs, fmt.Errorf("recorder: drain the video encoder: %w", err))
	}
	if err := r.writeVideo(pkts); err != nil {
		errs = append(errs, err)
	}
	r.p.video.Close()
	r.muxMu.Lock()
	if err := r.mux.Close(); err != nil {
		errs = append(errs, fmt.Errorf("recorder: finalize %s: %w", r.path, err))
	}
	r.muxMu.Unlock()
	if err := r.file.Sync(); err != nil {
		errs = append(errs, fmt.Errorf("recorder: sync %s: %w", r.path, err))
	}
	if err := r.file.Close(); err != nil {
		errs = append(errs, fmt.Errorf("recorder: close %s: %w", r.path, err))
	}
	return errors.Join(errs...)
}
