package recorder

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"
	"github.com/stubbedev/lys-lib/media/mux"
	"github.com/stubbedev/lys-lib/media/opus"
)

// ringFeed writes a stream into a ring in arrival order: fps video
// frames a second, stamped exactly, keyframes every gop frames, and a 20 ms audio packet
// whenever one falls due, each sample's bytes made by its maker.
type ringFeed struct {
	r       *replayRing
	fps     int
	gop     int
	video   func(i int) []byte
	audio   func(i int) []byte
	frames  int
	packets int
}

// run feeds n more frames and the audio packets up to the last of them.
func (f *ringFeed) run(t testing.TB, n int) {
	t.Helper()
	for range n {
		pts := time.Duration(f.frames) * time.Second / time.Duration(f.fps)
		for f.audio != nil && time.Duration(f.packets)*audioFrameTime <= pts {
			if err := f.r.write(mux.Sample{Track: audioTrack, PTS: time.Duration(f.packets) * audioFrameTime, Data: f.audio(f.packets)}, true); err != nil {
				t.Fatal(err)
			}
			f.packets++
		}
		if err := f.r.write(mux.Sample{Track: videoTrack, PTS: pts, Data: f.video(f.frames)}, f.frames%f.gop == 0); err != nil {
			t.Fatal(err)
		}
		f.frames++
	}
}

func fixtureTracks(fx h264Fixture, audio bool) []mux.Track {
	tracks := []mux.Track{mux.H264{SPS: fx.sps, PPS: fx.pps}}
	if audio {
		tracks = append(tracks, mux.Opus{Channels: audioChannels, PreSkip: opus.PreSkip})
	}
	return tracks
}

// silentOpus is one encoded 20 ms packet of stereo silence.
func silentOpus(t *testing.T) []byte {
	t.Helper()
	enc, err := opus.NewEncoder(audioChannels, audioBitrate, opus.ConstrainedVBR)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := enc.Encode(nil, make([]int16, audioFrame*audioChannels))
	if err != nil {
		t.Fatal(err)
	}
	return packet
}

// A save writes a playable file from the oldest keyframe on: its video
// starts on a keyframe at zero, every frame decodes, and it runs at
// least the span and less than the span and one GOP; the audio comes
// along from the same cut.
func TestReplaySaveWritesTheLastSecondsPlayable(t *testing.T) {
	fx := loadFixture(t)
	period := time.Second / testFPS
	gopTime := time.Duration(fixtureGOP) * period
	span := time.Second
	packet := silentOpus(t)
	for _, format := range []config.RecorderFormat{config.RecorderFormatMp4, config.RecorderFormatMkv} {
		t.Run(string(format), func(t *testing.T) {
			r := newReplayRing(span, fixtureTracks(fx, true), nil)
			feed := &ringFeed{
				r: r, fps: testFPS, gop: fixtureGOP,
				video: func(i int) []byte { return fx.units[i%len(fx.units)] },
				audio: func(int) []byte { return packet },
			}
			feed.run(t, 3*testFPS+7)
			path := filepath.Join(t.TempDir(), "replay."+string(format))
			if err := r.save(path, format); err != nil {
				t.Fatal(err)
			}
			p := ffprobe(t, path)
			if len(p.Streams) != 2 || p.Streams[0].CodecName != "h264" || p.Streams[1].CodecName != "opus" {
				t.Fatalf("streams = %+v, want h264 and opus", p.Streams)
			}
			vpts := p.ptsOf(0)
			if len(vpts) == 0 || vpts[0] != 0 || p.Packets[slices.IndexFunc(p.Packets, func(pk probedPacket) bool { return pk.Stream == 0 })].Flags[0] != 'K' {
				t.Fatalf("video starts at %v, want a keyframe at 0", vpts)
			}
			if p.Streams[0].Frames != strconv.Itoa(len(vpts)) {
				t.Errorf("%s of %d frames decoded", p.Streams[0].Frames, len(vpts))
			}
			length := time.Duration(len(vpts)) * period
			if length < span || length >= span+gopTime {
				t.Errorf("replay holds %v of video, want at least %v and less than %v", length, span, span+gopTime)
			}
			if apts := p.ptsOf(1); len(apts) == 0 || slices.Min(apts) < -0.01 || slices.Max(apts)+2*audioFrameTime.Seconds() < slices.Max(vpts) {
				t.Errorf("audio spans %v, want it from the cut to the video's end %v", apts, slices.Max(vpts))
			}
		})
	}
}

// Before the first keyframe there is nothing a player could start on:
// a save says so and writes no file.
func TestReplaySaveBeforeAKeyframeFails(t *testing.T) {
	fx := loadFixture(t)
	r := newReplayRing(time.Second, fixtureTracks(fx, true), nil)
	if err := r.write(mux.Sample{Track: audioTrack, PTS: 0, Data: silentOpus(t)}, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "replay.mkv")
	if err := r.save(path, config.RecorderFormatMkv); !errors.Is(err, errReplayEmpty) {
		t.Fatalf("save = %v, want %v", err, errReplayEmpty)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file was left: %v", err)
	}
}

// A save into a directory it cannot write reports it and leaves the
// ring as it was.
func TestReplaySaveFailureLeavesTheRing(t *testing.T) {
	fx := loadFixture(t)
	r := newReplayRing(time.Second, fixtureTracks(fx, false), nil)
	feed := &ringFeed{r: r, fps: testFPS, gop: fixtureGOP, video: func(i int) []byte { return fx.units[i%len(fx.units)] }}
	feed.run(t, 2*testFPS)
	if err := r.save(filepath.Join(t.TempDir(), "missing", "replay.mkv"), config.RecorderFormatMkv); err == nil {
		t.Fatal("a save into a missing directory succeeded")
	}
	if r.pins != 0 {
		t.Errorf("%d pins left after a failed save", r.pins)
	}
	if err := r.save(filepath.Join(t.TempDir(), "replay.mkv"), config.RecorderFormatMkv); err != nil {
		t.Errorf("the next save: %v", err)
	}
}

// synthetic is a packet of n bytes, the first byte numbering it, so a
// stored sample can be told from another.
func synthetic(buf []byte, n, i int) []byte {
	buf = buf[:n]
	buf[0] = byte(i)
	return buf
}

// Over five minutes of a 60 fps stream with 2 s GOPs, the ring never
// holds more than the bitrate times the span plus one GOP (with the
// chunks its ends are cut into), and once warm it stores
// samples without allocating.
func TestReplayRingIsBoundedAndAllocationFreeOnceWarm(t *testing.T) {
	const (
		fps       = 60
		gop       = fps * keyframeEverySecs
		keySize   = 90 << 10
		frameSize = 12 << 10
		audioSize = 320
		seconds   = 30
	)
	span := seconds * time.Second
	keyBuf, frameBuf, audioBuf := make([]byte, keySize), make([]byte, frameSize), make([]byte, audioSize)
	r := newReplayRing(span, nil, nil)
	feed := &ringFeed{
		r: r, fps: fps, gop: gop,
		video: func(i int) []byte {
			if i%gop == 0 {
				return synthetic(keyBuf, keySize, i)
			}
			return synthetic(frameBuf, frameSize, i)
		},
		audio: func(i int) []byte { return synthetic(audioBuf, audioSize, i) },
	}
	gopBytes := keySize + (gop-1)*frameSize + keyframeEverySecs*int(time.Second/audioFrameTime)*audioSize
	bound := uint64(gopBytes*seconds/keyframeEverySecs + gopBytes + 2*ringChunk)
	var peak uint64
	for range 5 * 60 * fps / gop {
		feed.run(t, gop)
		peak = max(peak, r.Bytes())
	}
	if peak > bound {
		t.Errorf("the ring held %d bytes at its peak, past bitrate x span + one GOP + the two chunks it is cut into at its ends = %d", peak, bound)
	}
	if held := r.Bytes(); held < uint64(gopBytes*seconds/keyframeEverySecs) {
		t.Errorf("the ring holds %d bytes, less than the span's %d", held, gopBytes*seconds/keyframeEverySecs)
	}
	if allocs := testing.AllocsPerRun(20, func() { feed.run(t, gop) }); allocs != 0 {
		t.Errorf("a warm GOP allocated %v times, want none", allocs)
	}
}

// A save in progress keeps the chunks it reads from being recycled
// while capture goes on and old GOPs leave: what it reads is what was
// there when it started.
func TestPinnedChunksAreNotRecycledUnderASave(t *testing.T) {
	const size = 40 << 10
	buf := make([]byte, size)
	r := newReplayRing(time.Second, nil, nil)
	feed := &ringFeed{r: r, fps: 30, gop: 15, video: func(i int) []byte {
		for j := range buf {
			buf[j] = byte(i)
		}
		return buf
	}}
	feed.run(t, 60)
	v, err := r.pin()
	if err != nil {
		t.Fatal(err)
	}
	want := make([][]byte, len(v.entries))
	for i, e := range v.entries {
		want[i] = bytes.Clone(v.bytes(e))
	}
	feed.run(t, 120)
	for i, e := range v.entries {
		if !bytes.Equal(v.bytes(e), want[i]) {
			t.Fatalf("entry %d changed under the pinned save", i)
		}
	}
	r.unpin()
}

// Closing the ring lets go of every chunk: the heap returns to where it
// was before it filled.
func TestClosedRingFreesItsMemory(t *testing.T) {
	heap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	before := heap()
	buf := make([]byte, 64<<10)
	r := newReplayRing(10*time.Second, nil, nil)
	feed := &ringFeed{r: r, fps: 60, gop: 120, video: func(i int) []byte { return synthetic(buf, len(buf), i) }}
	feed.run(t, 600)
	if held := r.Bytes(); held < 32<<20 {
		t.Fatalf("the ring holds only %d bytes", held)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	if held := r.Bytes(); held != 0 {
		t.Errorf("a closed ring reports %d bytes", held)
	}
	if after := heap(); after > before+2<<20 {
		t.Errorf("heap %d bytes after closing, %d before: the ring's chunks were kept", after, before)
	}
}

// A replay saves while it captures: the frames keep coming, none is
// lost or repeated across the save, and stopping it drains the encoder
// once and frees the ring.
func TestReplaySavesWithoutDisturbingTheCapture(t *testing.T) {
	fx := loadFixture(t)
	video := &fakeVideo{fx: fx}
	period := time.Second / testFPS
	wall := newFakeWall(time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC), period, audioFrameTime)
	terms := &termLog{}
	var keys atomic.Int64
	rp, err := startReplay(recordingParts{video: video, fps: testFPS, wall: wall}, time.Second, terms.term, func() { keys.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	frames := func(n int) {
		for range n {
			wall.advance(period)
			wall.tick(period)
		}
	}
	frames(2 * testFPS)
	first := filepath.Join(t.TempDir(), "first.mkv")
	if err := rp.Save(first, config.RecorderFormatMkv); err != nil {
		t.Fatal(err)
	}
	frames(testFPS)
	second := filepath.Join(t.TempDir(), "second.mkv")
	if err := rp.Save(second, config.RecorderFormatMkv); err != nil {
		t.Fatal(err)
	}
	waitRecording(t, "the last frame in the ring", func() bool { return rp.rec.videoFrames.Load() == uint64(len(video.frames())-1) })
	encoded := video.frames()
	if len(encoded) != 3*testFPS {
		t.Fatalf("%d frames encoded, want %d: the saves held up the capture", len(encoded), 3*testFPS)
	}
	for i, pts := range encoded {
		if want := time.Duration(i+1) * period; pts != want {
			t.Fatalf("frame %d at %v, want %v: a frame was dropped or repeated", i, pts, want)
		}
	}
	a, b := ffprobe(t, first).ptsOf(0), ffprobe(t, second).ptsOf(0)
	for name, pts := range map[string][]float64{"first": a, "second": b} {
		for i := 1; i < len(pts); i++ {
			if !approx(pts[i]-pts[i-1], period.Seconds(), 0.0015) {
				t.Fatalf("%s save: frames at %v and %v, not one period apart", name, pts[i-1], pts[i])
			}
		}
	}
	if n := keys.Load(); n != 3*testFPS/fixtureGOP {
		t.Errorf("%d keyframe reports, want one per GOP: %d", n, 3*testFPS/fixtureGOP)
	}
	if err := rp.Stop(); err != nil {
		t.Fatal(err)
	}
	if video.flushes != 1 || video.closes != 1 {
		t.Errorf("flushed %d, closed %d times, want once each", video.flushes, video.closes)
	}
	if held := rp.Bytes(); held != 0 {
		t.Errorf("a stopped replay holds %d bytes", held)
	}
	if got := terms.all(); len(got) != 0 {
		t.Errorf("a requested stop reported %q", got)
	}
}
