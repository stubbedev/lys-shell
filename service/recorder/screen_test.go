package recorder

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/gpu"
	"github.com/stubbedev/lys-lib/gputest"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/gpuhost"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/pulse/native"
	"github.com/stubbedev/lys-shell/service/pulse/pulsetest"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
	"github.com/stubbedev/lys-shell/swaytest"
)

// encoderNode is the host's first render node with a Vulkan device
// that encodes H.264 behind it. No device there (gpuhost.Node), or no
// H.264 encoder on it, is gpuhost.Absent.
func encoderNode(t *testing.T) string {
	t.Helper()
	node, _, d := gpuhost.Node(t)
	if err := requireH264(d); err != nil {
		gpuhost.Absent(t, "the GPU behind %s cannot record: %v", node, err)
	}
	return node
}

// gpuSway starts a private headless sway rendering with Vulkan on the
// encoder's node and returns its output's name.
func gpuSway(t *testing.T) string {
	t.Helper()
	node := encoderNode(t)
	swaytest.Start(t, "WLR_RENDERER=vulkan", "WLR_RENDER_DRM_DEVICE="+node)
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	outs := c.Outputs()
	if len(outs) == 0 {
		t.Fatal("the headless sway has no output")
	}
	return outs[0].Name
}

// audioServer is a private PulseAudio server with a speaker, its
// monitor and a microphone, and the audio service on it.
func audioServer(t *testing.T) (*pulsetest.Server, *pulse.Service) {
	t.Helper()
	srv := pulsetest.New(t)
	srv.PutSink(pulsetest.Sink(1, "speakers", "Speakers", 2))
	srv.PutSource(pulsetest.Source(2, "speakers.monitor", "Monitor of Speakers", 1, "speakers"))
	srv.PutSource(pulsetest.Source(10, "mic", "Microphone", native.InvalidIndex, ""))
	srv.SetDefaults("speakers", "mic")
	s, err := pulse.Connect(context.Background(), srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return srv, s
}

// feedRecords pushes 10 ms of a constant level into each record stream in
// real time until stop closes.
func feedRecords(recs []*pulsetest.Record, stop <-chan struct{}) {
	chunk := make([]byte, audioFragment*audioChannels*sampleBytes)
	for i := 0; i < len(chunk); i += 2 {
		chunk[i], chunk[i+1] = 0x00, 0x10
	}
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			for _, r := range recs {
				r.Push(chunk)
			}
		}
	}
}

// On a real GPU behind a private sway: the picked output is remembered,
// MP4 and Matroska each record the screen at its size with the desktop
// and the microphone mixed in, every frame the clock makes due lands in
// the file at its time with the pause cut, and every frame decodes.
func TestRecordsAnOutputOnTheGPU(t *testing.T) {
	requireFFmpeg(t)
	output := gpuSway(t)
	logged := teeLog(t)
	srv, audio := audioServer(t)
	stateDir := t.TempDir()
	for i, format := range []config.RecorderFormat{config.RecorderFormatMp4, config.RecorderFormatMkv} {
		t.Run(string(format), func(t *testing.T) {
			p := &fakePicker{reply: "r/screen:" + output}
			e := NewGPUEngine(p, audio, noLogical, stateDir)
			period := time.Second / 30
			wall := newFakeWall(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC), period, audioFrameTime)
			e.wall = wall
			s, err := e.OpenSession(context.Background(), true)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			path := filepath.Join(t.TempDir(), "screen."+string(format))
			terms := &termLog{}
			rec, err := s.Start(Options{OutputPath: path, Format: format, Framerate: 30, ShowCursor: true, SystemAudio: true, Microphone: true}, terms.term)
			if err != nil {
				t.Fatal(err)
			}
			recs := srv.WaitRecords(t, 2)
			stop := make(chan struct{})
			go feedRecords(recs, stop)
			frames, recorded := wall.playPaused(rec, period, 30, 15, 60)
			err = rec.Stop()
			close(stop)
			if err != nil {
				t.Fatal(err)
			}
			if got := terms.all(); len(got) != 0 {
				t.Errorf("reported %q", got)
			}
			if !encodingLogged.MatchString(logged.String()) {
				t.Errorf("no recording announced its device and backend in:\n%s", logged.String())
			}
			pr := ffprobe(t, path)
			if len(pr.Streams) != 2 || pr.Streams[0].CodecName != "h264" || pr.Streams[1].CodecName != "opus" {
				t.Fatalf("streams %+v, want h264 and opus", pr.Streams)
			}
			t.Logf("%s from %s: %ss, %s frames decoded, streams %+v", path, output, pr.Format.Duration, pr.Streams[0].Frames, pr.Streams)
			if n, _ := strconv.Atoi(pr.Streams[0].Frames); n != len(frames) {
				t.Errorf("%d frames decode, want the %d the clock made due", n, len(frames))
			}
			if got := slices.Sorted(slices.Values(pr.ptsOf(0))); !slices.EqualFunc(got, frames, func(g float64, w time.Duration) bool { return approx(g, w.Seconds(), 0.001) }) {
				t.Errorf("frames at %v, want each at its time on the clock, %v", got, frames)
			}
			lastAudio := lastAudioPacket(recorded)
			if a := pr.ptsOf(1); len(a) == 0 || !approx(slices.Max(a), lastAudio.Seconds(), 0.001) {
				t.Errorf("audio ends at %v, want its last packet at %v of %v recorded", a, lastAudio.Seconds(), recorded.Seconds())
			}
			if want := 1 - min(i, 1); len(p.asked) != want {
				t.Errorf("asked the picker %d times, want %d: the first pick is remembered", len(p.asked), want)
			}
			if data, err := os.ReadFile(filepath.Join(stateDir, sourceFile)); err != nil || string(data) != "screen:"+output+"\n" {
				t.Errorf("remembered %q (%v)", data, err)
			}
		})
	}
}

// encodingLogged matches the line a recording announces its encoder
// with, whichever backend the GPU encodes through.
var encodingLogged = regexp.MustCompile(`(?m)recorder: encoding H\.264 on .+ through (Vulkan video|VA-API)$`)

// syncBuffer is a log destination tests read while recordings write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// teeLog copies the process log into a buffer for the test, still
// writing it where it went.
func teeLog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	out := log.Writer()
	log.SetOutput(io.MultiWriter(out, buf))
	t.Cleanup(func() { log.SetOutput(out) })
	return buf
}

// The encoder's log line names the device and the backend, the Vulkan
// video queue or the VA-API driver.
func TestEncodingLine(t *testing.T) {
	for backend, want := range map[gpu.EncoderBackend]string{
		gpu.VulkanVideo: "recorder: encoding H.264 on Intel(R) Graphics (LNL) through Vulkan video",
		gpu.VAAPI:       "recorder: encoding H.264 on Intel(R) Graphics (LNL) through VA-API",
	} {
		if got := encodingLine("Intel(R) Graphics (LNL)", backend); got != want {
			t.Errorf("%d: %q, want %q", backend, got, want)
		}
	}
}

// The webcam overlay lands in the encoded picture: a red frame scaled
// into its rectangle decodes red there, and the screen outside stays
// as it was.
func TestWebcamOverlayIsEncodedIntoTheFrame(t *testing.T) {
	requireFFmpeg(t)
	output := gpuSway(t)
	s, err := openScreen(sharepicker.Target{Kind: sharepicker.TargetOutput, Name: output}, false, noLogical, gpu.OpenNode)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	video, err := newGPUVideo(s, 30)
	if err != nil {
		t.Fatal(err)
	}
	size := s.frameSize()
	rect := webcamRect(Webcam{X: 0, Y: 0, Size: 30}, int32(size.X), int32(size.Y))
	red := image.NewRGBA(image.Rect(0, 0, 32, 18))
	for i := 0; i < len(red.Pix); i += 4 {
		copy(red.Pix[i:], []byte{255, 0, 0, 255})
	}
	cam := &fakeCamera{frames: make(chan *image.RGBA, 1), recycled: make(chan *image.RGBA, 1)}
	cam.frames <- red
	path := filepath.Join(t.TempDir(), "cam.mkv")
	rec, err := startRecording(recordingParts{video: video, cam: cam, camRect: rect, fps: 30, wall: systemClock{}}, Options{OutputPath: path, Format: config.RecorderFormatMkv}, func(r string) { t.Errorf("reported %q", r) })
	if err != nil {
		t.Fatal(err)
	}
	<-cam.recycled
	time.Sleep(500 * time.Millisecond)
	if err := rec.Stop(); err != nil {
		t.Fatal(err)
	}
	frame := lastFrame(t, path, size)
	center := image.Pt((rect.Min.X+rect.Max.X)/2, (rect.Min.Y+rect.Max.Y)/2)
	if c := frame.RGBAAt(center.X, center.Y); c.R < 200 || c.G > 60 || c.B > 60 {
		t.Errorf("the overlay's centre %v decodes %v, want red", center, c)
	}
	outside := image.Pt(rect.Max.X+20, rect.Max.Y+20)
	if c := frame.RGBAAt(outside.X, outside.Y); c.R > 200 && c.G < 60 {
		t.Errorf("outside the overlay %v decodes red %v", outside, c)
	}
}

// lastFrame decodes the file's last frame to RGBA.
func lastFrame(t *testing.T, path string, size image.Point) *image.RGBA {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-sseof", "-0.2", "-i", path, "-frames:v", "1", "-pix_fmt", "rgba", "-f", "rawvideo", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || len(out) != size.X*size.Y*4 {
		t.Fatalf("decode the last frame of %s: %d bytes for %v, %v: %s", path, len(out), size, err, stderr.String())
	}
	return &image.RGBA{Pix: out, Stride: size.X * 4, Rect: image.Rectangle{Max: size}}
}

// A device whose video queue does not encode H.264 (lavapipe, or one
// with no video queue) is the defined ErrNoVideoEncoder, from the check
// and from the encoder itself; one that does passes the check.
func TestNoH264EncoderIsTheDefinedError(t *testing.T) {
	if _, err := gputest.SetupTest(t); err != nil {
		t.Fatal(err)
	}
	d, err := gpu.Open()
	if errors.Is(err, gpu.ErrNoVulkan) || errors.Is(err, gpu.ErrNoDevice) {
		gpuhost.Absent(t, "no Vulkan device at all: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	err = requireH264(d)
	if slices.Contains(d.Codecs(), gpu.H264) {
		if err != nil {
			t.Errorf("%s encodes H.264, yet: %v", d.Name(), err)
		}
		t.Logf("%s encodes %v", d.Name(), d.Codecs())
		return
	}
	_, encErr := d.NewEncoder(gpu.EncoderConfig{Codec: gpu.H264, Width: 64, Height: 64, FrameRate: 30, KeyInterval: 60, QP: videoQP})
	if !errors.Is(encErr, ErrNoVideoEncoder) {
		t.Fatalf("an encoder on %s: %v, want ErrNoVideoEncoder", d.Name(), encErr)
	}
	// The recorder's error is the device's whole reason, which the
	// state shows, toasts and logs as it is.
	if !errors.Is(err, ErrNoVideoEncoder) || err.Error() != "recorder: "+encErr.Error() || !strings.Contains(err.Error(), d.Name()) {
		t.Errorf("%s without H.264: %q, want ErrNoVideoEncoder with the device's reason %q", d.Name(), err, encErr)
	}
	t.Logf("%s cannot record: %v", d.Name(), err)
}
