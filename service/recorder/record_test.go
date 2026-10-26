package recorder

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
)

const testFPS = 30

// termLog collects the reasons a recording reported ending on its own.
type termLog struct {
	mu      sync.Mutex
	reasons []string
}

func (l *termLog) term(reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reasons = append(l.reasons, reason)
}

func (l *termLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.reasons)
}

func startTest(t *testing.T, parts recordingParts, format config.RecorderFormat) (*recording, string, *termLog) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out."+string(format))
	terms := &termLog{}
	if parts.now == nil {
		parts.now = time.Now
	}
	if parts.fps == 0 {
		parts.fps = testFPS
	}
	r, err := startRecording(parts, Options{OutputPath: path, Format: format}, terms.term)
	if err != nil {
		t.Fatal(err)
	}
	return r, path, terms
}

// MP4 and Matroska each come out playable with a video and an Opus
// track: every frame encoded is in the file, the one still in flight
// at Stop included (the drain), each frame decodes, and the audio runs
// as long as the video.
func TestRecordingWritesAPlayableFileWithAudio(t *testing.T) {
	fx := loadFixture(t)
	for _, format := range []config.RecorderFormat{config.RecorderFormatMp4, config.RecorderFormatMkv} {
		t.Run(string(format), func(t *testing.T) {
			video := &fakeVideo{fx: fx}
			desktop, mic := &fakeAudio{}, &fakeAudio{}
			r, path, terms := startTest(t, recordingParts{video: video, audio: []startAudio{desktop.start, mic.start}}, format)
			stopTones := make(chan struct{})
			go desktop.tone(4000, stopTones)
			go mic.tone(-1000, stopTones)
			time.Sleep(700 * time.Millisecond)
			if err := r.Stop(); err != nil {
				t.Fatal(err)
			}
			close(stopTones)
			if video.flushes != 1 || video.closes != 1 {
				t.Errorf("flushed %d, closed %d times, want once each", video.flushes, video.closes)
			}
			p := ffprobe(t, path)
			if len(p.Streams) != 2 || p.Streams[0].CodecName != "h264" || p.Streams[1].CodecName != "opus" {
				t.Fatalf("streams = %+v, want h264 and opus", p.Streams)
			}
			frames := video.frames()
			if p.Streams[0].Frames != strconv.Itoa(len(frames)) {
				t.Errorf("%s video frames decoded, want all %d encoded (the drained one included)", p.Streams[0].Frames, len(frames))
			}
			vpts, apts := p.ptsOf(0), p.ptsOf(1)
			if last := frames[len(frames)-1].Seconds(); len(vpts) == 0 || !approx(slices.Max(vpts), last, 0.002) {
				t.Errorf("last video packet at %v, want the drained frame at %v", vpts, last)
			}
			if len(apts) == 0 || slices.Max(apts)+2*audioFrameTime.Seconds() < slices.Max(vpts) {
				t.Errorf("audio ends at %v, a frame or more before the video's %v", apts, slices.Max(vpts))
			}
			if got := terms.all(); len(got) != 0 {
				t.Errorf("a requested stop reported %q", got)
			}
		})
	}
}

// A pause cuts its span from both tracks: no frame is captured and no
// audio is kept while paused, and the timestamps run on without a gap.
func TestRecordingPauseCutsTheSpan(t *testing.T) {
	fx := loadFixture(t)
	video := &fakeVideo{fx: fx}
	src := &fakeAudio{}
	r, path, _ := startTest(t, recordingParts{video: video, audio: []startAudio{src.start}}, config.RecorderFormatMkv)
	stopTone := make(chan struct{})
	go src.tone(2000, stopTone)
	time.Sleep(300 * time.Millisecond)
	r.SetPaused(true)
	r.mu.Lock()
	pausedAt := r.clock.elapsed(time.Now())
	r.mu.Unlock()
	time.Sleep(600 * time.Millisecond)
	// A frame taken after the pause would carry the stopped clock.
	if i := slices.IndexFunc(video.frames(), func(pts time.Duration) bool { return pts >= pausedAt }); i >= 0 {
		t.Errorf("frame %d was captured while paused, at %v", i, video.frames()[i])
	}
	r.SetPaused(false)
	time.Sleep(300 * time.Millisecond)
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	close(stopTone)
	frames := video.frames()
	for i := 1; i < len(frames); i++ {
		if gap := frames[i] - frames[i-1]; gap > 150*time.Millisecond {
			t.Errorf("frames %d and %d are %v apart: the pause was not cut", i-1, i, gap)
		}
	}
	p := ffprobe(t, path)
	if d := slices.Max(p.ptsOf(1)); d > 0.8 {
		t.Errorf("audio runs to %vs over 0.6s of recording: the pause was kept", d)
	}
}

// Audio delivered while paused is dropped; what was queued before the
// pause stays to fill the frames up to it, and delivery after the
// resume queues again.
func TestPauseDropsAudioDeliveredWhilePaused(t *testing.T) {
	fx := loadFixture(t)
	src := &fakeAudio{}
	r, _, _ := startTest(t, recordingParts{video: &fakeVideo{fx: fx}, audio: []startAudio{src.start}}, config.RecorderFormatMkv)
	defer func() { _ = r.Stop() }()
	queued := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.mixer.queues[0])
	}
	r.SetPaused(true)
	src.push(pcmBytes(1, 2, 3, 4))
	if n := queued(); n != 0 {
		t.Errorf("%d samples queued while paused", n)
	}
	r.SetPaused(false)
	src.push(pcmBytes(5, 6))
	if n := queued(); n != 2 {
		t.Errorf("%d samples queued after the resume, want 2", n)
	}
}

// Two tracks failing on one full disk report it once.
func TestFailuresOfBothTracksAreReportedOnce(t *testing.T) {
	fx := loadFixture(t)
	src := &fakeAudio{}
	r, _, terms := startTest(t, recordingParts{video: &fakeVideo{fx: fx}, audio: []startAudio{src.start}}, config.RecorderFormatMp4)
	stopTone := make(chan struct{})
	defer close(stopTone)
	go src.tone(100, stopTone)
	_ = r.file.Close()
	time.Sleep(2500 * time.Millisecond)
	if got := terms.all(); len(got) != 1 || !strings.Contains(got[0], "file already closed") {
		t.Errorf("reported %q, want the write failure once", got)
	}
	if err := r.Stop(); err == nil {
		t.Error("finalizing onto a closed file succeeded")
	}
}

// A capture that fails ends the recording once, with the reason, and
// what was recorded before it is still finalized on Stop.
func TestRecordingFailureIsReportedAndStillFinalized(t *testing.T) {
	fx := loadFixture(t)
	video := &fakeVideo{fx: fx, failAt: 4, failErr: errors.New("recorder: the output is gone: HDMI-A-1")}
	r, path, terms := startTest(t, recordingParts{video: video}, config.RecorderFormatMp4)
	deadline := time.Now().Add(3 * time.Second)
	for len(terms.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := terms.all(); !slices.Equal(got, []string{"recorder: the output is gone: HDMI-A-1"}) {
		t.Fatalf("reported %q, want the failure once", got)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(); err != nil {
		t.Errorf("a second Stop: %v", err)
	}
	if video.closes != 1 {
		t.Errorf("closed %d times", video.closes)
	}
	if p := ffprobe(t, path); p.Streams[0].Frames != "3" {
		t.Errorf("%s frames, want the 3 before the failure", p.Streams[0].Frames)
	}
}

// fakeCamera hands out the frames the test sends; closing frames ends
// it with err.
type fakeCamera struct {
	frames   chan *image.RGBA
	recycled chan *image.RGBA
	err      error
	closed   bool
}

func (c *fakeCamera) Frames() <-chan *image.RGBA { return c.frames }
func (c *fakeCamera) Recycle(img *image.RGBA)    { c.recycled <- img }
func (c *fakeCamera) Err() error                 { return c.err }
func (c *fakeCamera) Close()                     { c.closed = true }

// Each camera frame becomes the overlay at the webcam's rectangle and
// goes back to the camera; a camera that stops ends the recording with
// its reason.
func TestWebcamFramesBecomeTheOverlay(t *testing.T) {
	fx := loadFixture(t)
	video := &fakeVideo{fx: fx}
	cam := &fakeCamera{frames: make(chan *image.RGBA), recycled: make(chan *image.RGBA, 2), err: errors.New("/dev/video0 hung up")}
	rect := image.Rect(10, 20, 90, 65)
	r, _, terms := startTest(t, recordingParts{video: video, cam: cam, camRect: rect}, config.RecorderFormatMkv)
	img := image.NewRGBA(image.Rect(0, 0, 16, 9))
	cam.frames <- img
	if got := <-cam.recycled; got != img {
		t.Error("another frame came back")
	}
	close(cam.frames)
	deadline := time.Now().Add(3 * time.Second)
	for len(terms.all()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := terms.all(); len(got) != 1 || !strings.Contains(got[0], "the webcam stopped: /dev/video0 hung up") {
		t.Errorf("reported %q, want the camera's reason", got)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(video.overlays, []image.Rectangle{rect}) || !cam.closed {
		t.Errorf("overlays %v, camera closed %v", video.overlays, cam.closed)
	}
}

// A start that cannot create its file, or whose container or audio
// fails, releases what it was handed and leaves no file behind; an
// existing file is never overwritten.
func TestStartRecordingFailsCleanly(t *testing.T) {
	fx := loadFixture(t)
	dir := t.TempDir()
	existing := filepath.Join(dir, "taken.mkv")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	failing := func(func([]byte)) (audioCapture, error) {
		return nil, errors.New("recorder: capture default input: refused")
	}
	ok := &fakeAudio{}
	for name, c := range map[string]struct {
		opts  Options
		audio []startAudio
		want  string
	}{
		"existing":   {Options{OutputPath: existing, Format: config.RecorderFormatMkv}, nil, "file exists"},
		"unwritable": {Options{OutputPath: filepath.Join(dir, "missing", "x.mkv"), Format: config.RecorderFormatMkv}, nil, "no such file"},
		"format":     {Options{OutputPath: filepath.Join(dir, "x.webm"), Format: "webm"}, nil, `no muxer for output format "webm"`},
		"audio":      {Options{OutputPath: filepath.Join(dir, "a.mkv"), Format: config.RecorderFormatMkv}, []startAudio{ok.start, failing}, "refused"},
	} {
		video := &fakeVideo{fx: fx}
		cam := &fakeCamera{}
		_, err := startRecording(recordingParts{video: video, cam: cam, audio: c.audio, fps: testFPS, now: time.Now}, c.opts, func(string) {})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
		if video.closes != 1 || !cam.closed {
			t.Errorf("%s: video closed %d, camera closed %v: the parts leaked", name, video.closes, cam.closed)
		}
		if name != "existing" {
			if _, err := os.Stat(c.opts.OutputPath); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s: the output was left behind: %v", name, err)
			}
		}
	}
	if !ok.closed {
		t.Error("the audio source that started was not closed")
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep" {
		t.Errorf("the existing file now holds %q", data)
	}
}

func approx(a, b, eps float64) bool { return a-b < eps && b-a < eps }
