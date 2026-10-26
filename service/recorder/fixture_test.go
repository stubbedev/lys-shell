package recorder

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/gpu"
	"github.com/stubbedev/lys-lib/media/opus"
)

// requireFFmpeg skips a test that needs ffmpeg and ffprobe without
// them; the dev shell carries both.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not installed (the nix dev shell carries it)", bin)
		}
	}
}

// fixtureGOP is the keyframe interval of the fixture stream, whose
// length is a whole number of GOPs so it loops on a keyframe.
const fixtureGOP = 15

// h264Fixture is a real 64x48 H.264 stream from libx264, one Annex-B
// access unit per frame (without access unit delimiters), and its SPS
// and PPS.
type h264Fixture struct {
	units    [][]byte
	sps, pps []byte
}

var (
	fixtureOnce sync.Once
	fixture     h264Fixture
	fixtureErr  error
)

func loadFixture(t *testing.T) h264Fixture {
	t.Helper()
	requireFFmpeg(t)
	fixtureOnce.Do(func() { fixture, fixtureErr = encodeFixture() })
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	return fixture
}

func encodeFixture() (h264Fixture, error) {
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=64x48:r=30", "-frames:v", strconv.Itoa(4*fixtureGOP),
		"-pix_fmt", "yuv420p", "-c:v", "libx264", "-bf", "0", "-g", strconv.Itoa(fixtureGOP), "-bsf:v", "h264_metadata=aud=insert", "-f", "h264", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stream, err := cmd.Output()
	if err != nil {
		return h264Fixture{}, errors.New("ffmpeg: " + err.Error() + ": " + stderr.String())
	}
	aud := []byte{0, 0, 0, 1, 0x09}
	var f h264Fixture
	for _, au := range bytes.Split(stream, aud)[1:] {
		// Each unit keeps its start code: drop the AUD's payload byte.
		au = au[1:]
		for nal := range bytes.SplitSeq(au, []byte{0, 0, 1}) {
			if nal = bytes.TrimRight(nal, "\x00"); len(nal) == 0 {
				continue
			}
			switch nal[0] & 0x1f {
			case 7:
				f.sps = bytes.Clone(nal)
			case 8:
				f.pps = bytes.Clone(nal)
			}
		}
		f.units = append(f.units, au)
	}
	if f.sps == nil || f.pps == nil || len(f.units) != 4*fixtureGOP {
		return h264Fixture{}, errors.New("the fixture stream has no SPS/PPS or the wrong frame count")
	}
	return f, nil
}

// fakeVideo encodes the fixture's frames in order, one frame in flight
// as the GPU encoder keeps them: Frame returns the previous frame's
// packet, Flush the last.
type fakeVideo struct {
	fx h264Fixture

	mu       sync.Mutex
	pts      []time.Duration
	inFlight *gpu.Packet
	overlays []image.Rectangle
	flushes  int
	closes   int
	// failAt makes the nth Frame call fail with failErr.
	failAt  int
	failErr error
}

func (v *fakeVideo) ParameterSets() (sps, pps []byte) { return v.fx.sps, v.fx.pps }

func (v *fakeVideo) SetOverlay(_ *image.RGBA, dst image.Rectangle) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.overlays = append(v.overlays, dst)
	return nil
}

func (v *fakeVideo) Frame(pts time.Duration) ([]gpu.Packet, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failAt > 0 && len(v.pts)+1 == v.failAt {
		return nil, v.failErr
	}
	i := len(v.pts)
	v.pts = append(v.pts, pts)
	var out []gpu.Packet
	if v.inFlight != nil {
		out = append(out, *v.inFlight)
	}
	v.inFlight = &gpu.Packet{Data: v.fx.units[i%len(v.fx.units)], PTS: pts, Key: i%fixtureGOP == 0}
	return out, nil
}

func (v *fakeVideo) Flush() ([]gpu.Packet, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.flushes++
	if v.inFlight == nil {
		return nil, nil
	}
	p := *v.inFlight
	v.inFlight = nil
	return []gpu.Packet{p}, nil
}

func (v *fakeVideo) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closes++
}

func (v *fakeVideo) frames() []time.Duration {
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]time.Duration(nil), v.pts...)
}

// pcmBytes lays samples out as s16le.
func pcmBytes(samples ...int16) []byte {
	b := make([]byte, 0, len(samples)*sampleBytes)
	for _, s := range samples {
		b = binary.LittleEndian.AppendUint16(b, uint16(s))
	}
	return b
}

// fakeAudio is a source whose chunks the test pushes.
type fakeAudio struct {
	mu     sync.Mutex
	onData func([]byte)
	closed bool
}

func (a *fakeAudio) start(onData func([]byte)) (audioCapture, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onData = onData
	return a, nil
}

// push delivers a chunk unless the source is closed.
func (a *fakeAudio) push(chunk []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.onData != nil && !a.closed {
		a.onData(chunk)
	}
}

func (a *fakeAudio) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
}

// tone feeds the source 10 ms chunks of a constant stereo level in
// real time until stop closes.
func (a *fakeAudio) tone(level int16, stop <-chan struct{}) {
	chunk := pcmBytes(make([]int16, audioFragment*audioChannels)...)
	for i := 0; i < len(chunk); i += 2 {
		chunk[i], chunk[i+1] = byte(level), byte(uint16(level)>>8)
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			a.push(chunk)
		}
	}
}

// probed is what ffprobe reads back from a file.
type probed struct {
	Streams []struct {
		CodecName string `json:"codec_name"`
		CodecType string `json:"codec_type"`
		Profile   string `json:"profile"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		Frames    string `json:"nb_read_frames"`
	} `json:"streams"`
	Packets []struct {
		Stream int    `json:"stream_index"`
		PTS    string `json:"pts_time"`
		Flags  string `json:"flags"`
	} `json:"packets"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// ffprobe demuxes path's packets and decodes every frame: a file it
// fails to read back, or a frame it fails to decode, fails the test.
func ffprobe(t *testing.T, path string) probed {
	t.Helper()
	cmd := exec.Command("ffprobe", "-v", "error", "-count_frames", "-show_entries",
		"stream=codec_name,codec_type,profile,width,height,nb_read_frames:packet=stream_index,pts_time,flags:format=duration", "-of", "json", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || stderr.Len() > 0 {
		t.Fatalf("ffprobe %s: %v: %s", path, err, stderr.String())
	}
	var p probed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return p
}

// ptsOf lists one stream's packet times in seconds.
func (p probed) ptsOf(stream int) []float64 {
	var out []float64
	for _, pk := range p.Packets {
		if pk.Stream != stream {
			continue
		}
		v, err := strconv.ParseFloat(pk.PTS, 64)
		if err == nil {
			out = append(out, v)
		}
	}
	return out
}

// fakeWall is a wall clock that moves only when the test advances it,
// with a ticker for each period it was made with that ticks only when
// the test ticks it.
type fakeWall struct {
	mu    sync.Mutex
	read  *sync.Cond
	now   time.Time
	reads int
	ticks map[time.Duration]chan time.Time
}

func newFakeWall(start time.Time, periods ...time.Duration) *fakeWall {
	w := &fakeWall{now: start, ticks: make(map[time.Duration]chan time.Time, len(periods))}
	w.read = sync.NewCond(&w.mu)
	for _, p := range periods {
		w.ticks[p] = make(chan time.Time)
	}
	return w
}

func (w *fakeWall) Now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reads++
	w.read.Broadcast()
	return w.now
}

func (w *fakeWall) Ticker(period time.Duration) (<-chan time.Time, func()) {
	c, ok := w.ticks[period]
	if !ok {
		panic(fmt.Sprintf("fakeWall: a ticker every %v, which it was not made with", period))
	}
	return c, func() {}
}

func (w *fakeWall) advance(d time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = w.now.Add(d)
}

// tick ticks the ticker of period and returns once the loop it paces
// has read the time for that tick: nothing else may read the clock
// meanwhile, so the test ticks one ticker at a time.
func (w *fakeWall) tick(period time.Duration) {
	w.mu.Lock()
	before, now := w.reads, w.now
	w.mu.Unlock()
	w.ticks[period] <- now
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.reads == before {
		w.read.Wait()
	}
}

// playPaused records on w at period: before frames, half a period
// more, a pause through paused ticks of the video loop, then after
// frames. It returns the times the frames fall due at on the
// recording's clock, and the time it recorded.
func (w *fakeWall) playPaused(rec Recording, period time.Duration, before, paused, after int) ([]time.Duration, time.Duration) {
	want := make([]time.Duration, 0, before+after)
	run := func(frames int, from time.Duration) {
		for i := range frames {
			w.advance(period)
			w.tick(period)
			want = append(want, from+time.Duration(i+1)*period)
		}
	}
	run(before, 0)
	w.advance(period / 2)
	rec.SetPaused(true)
	pausedAt := time.Duration(before)*period + period/2
	for range paused {
		w.advance(period)
		w.tick(period)
	}
	rec.SetPaused(false)
	run(after, pausedAt)
	return want, pausedAt + time.Duration(after)*period
}

// lastAudioPacket is the time of the last Opus packet in a file that
// recorded recorded: whole packets up to it, less the encoder's
// pre-skip.
func lastAudioPacket(recorded time.Duration) time.Duration {
	preSkip := time.Duration(opus.PreSkip) * time.Second / audioRate
	return time.Duration(int(recorded/audioFrameTime)-1)*audioFrameTime - preSkip
}
