package recorder

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stubbedev/lys-lib/gpu"

	"github.com/stubbedev/lys-shell/internal/atomicfile"
	"github.com/stubbedev/lys-shell/internal/intconv"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// sourceFile keeps the output or region the user let the recorder
// remember, in the state directory, as the picker's payload.
const sourceFile = "recorder-source"

// pickerAppID names the recorder to the share picker.
const pickerAppID = "lys-shell"

// dismissTimeout bounds taking down a picker whose start was
// cancelled.
const dismissTimeout = 2 * time.Second

// audioFragment is the PCM chunk a source delivers, in frames: 10 ms.
const audioFragment = audioRate / 100

// ErrPickerCancelled is a start the user dismissed at the picker.
var ErrPickerCancelled = errors.New("recorder: nothing was picked to record")

// Picker shows the share picker: sharepicker.Client.
type Picker interface {
	Pick(ctx context.Context, caller portalreq.Caller, r sharepicker.Request) (string, error)
	Dismiss(ctx context.Context, handle string) error
}

// LogicalSize is an output's size in the compositor's logical space,
// false when the output is not known: what a region is measured in.
type LogicalSize func(output string) (image.Point, bool)

// GPUEngine is the production Engine: the share picker for an output or
// a region of one, screencopy into a buffer on the GPU behind it, the
// GPU's H.264 encoder, and the audio service for the microphone and the
// desktop.
type GPUEngine struct {
	picker     Picker
	audio      *pulse.Service
	stateDir   string
	logical    LogicalSize
	openDevice func(uint64) (*gpu.Device, error)
	picks      atomic.Uint64
}

// NewGPUEngine records through picker, with audio from audio (nil when
// there is no audio server, which fails a recording that asks for
// sound), regions measured by logical, remembering the pick in stateDir
// ("" remembers nothing).
func NewGPUEngine(picker Picker, audio *pulse.Service, logical LogicalSize, stateDir string) *GPUEngine {
	return &GPUEngine{picker: picker, audio: audio, logical: logical, stateDir: stateDir, openDevice: gpu.OpenNode}
}

// OpenSession implements Engine: the remembered output or region, else
// the one the user picks, opened for capture on its GPU.
func (e *GPUEngine) OpenSession(ctx context.Context, showCursor bool) (Session, error) {
	if target, ok := e.rememberedSource(); ok {
		s, err := openScreen(target, showCursor, e.logical, e.openDevice)
		if !errors.Is(err, errOutputGone) {
			return e.session(s, err)
		}
		log.Printf("recorder: the output of the remembered %s is gone; asking again", target.Payload())
		e.forgetSource()
	}
	target, remember, err := e.pick(ctx)
	if err != nil {
		return nil, err
	}
	s, err := openScreen(target, showCursor, e.logical, e.openDevice)
	if err == nil && remember {
		e.rememberSource(target)
	}
	return e.session(s, err)
}

// recordable is a target the recorder captures: an output or a region.
func recordable(t sharepicker.Target) bool {
	return t.Kind == sharepicker.TargetOutput || t.Kind == sharepicker.TargetRegion
}

func (e *GPUEngine) session(s *screen, err error) (Session, error) {
	if err != nil {
		return nil, err
	}
	return &gpuSession{screen: s, audio: e.audio}, nil
}

// pick asks the share picker for one output or region. A cancelled ctx
// takes the picker down.
func (e *GPUEngine) pick(ctx context.Context) (target sharepicker.Target, remember bool, err error) {
	handle := fmt.Sprintf("/dev/stubbe/lys/Recorder/pick/%d", e.picks.Add(1))
	reply, err := e.picker.Pick(ctx, portalreq.Caller{Handle: handle, AppID: pickerAppID}, sharepicker.Request{
		Sources: sharepicker.SourceOutputs | sharepicker.SourceRegions, AllowToken: e.stateDir != "",
	})
	if ctx.Err() != nil {
		dctx, cancel := context.WithTimeout(context.Background(), dismissTimeout)
		defer cancel()
		if derr := e.picker.Dismiss(dctx, handle); derr != nil {
			log.Printf("recorder: take down the cancelled picker: %v", derr)
		}
		return sharepicker.Target{}, false, ctx.Err()
	}
	if err != nil {
		return sharepicker.Target{}, false, fmt.Errorf("recorder: the share picker: %w", err)
	}
	if reply == "" {
		return sharepicker.Target{}, false, ErrPickerCancelled
	}
	remember, targets, ok := sharepicker.ParseReply(reply)
	if !ok || len(targets) != 1 || !recordable(targets[0]) {
		return sharepicker.Target{}, false, fmt.Errorf("recorder: the share picker replied %q, not one output or region", reply)
	}
	return targets[0], remember, nil
}

func (e *GPUEngine) sourcePath() string { return filepath.Join(e.stateDir, sourceFile) }

// rememberedSource is the output or region a past pick let the
// recorder keep.
func (e *GPUEngine) rememberedSource() (sharepicker.Target, bool) {
	if e.stateDir == "" {
		return sharepicker.Target{}, false
	}
	data, err := os.ReadFile(e.sourcePath())
	if errors.Is(err, os.ErrNotExist) {
		return sharepicker.Target{}, false
	}
	if err != nil {
		log.Printf("recorder: read the remembered source: %v", err)
		return sharepicker.Target{}, false
	}
	t, ok := sharepicker.ParseTarget(strings.TrimSpace(string(data)))
	if !ok || !recordable(t) {
		log.Printf("recorder: %s holds %q, not an output or region; asking again", e.sourcePath(), data)
		e.forgetSource()
		return sharepicker.Target{}, false
	}
	return t, true
}

func (e *GPUEngine) rememberSource(t sharepicker.Target) {
	if err := os.MkdirAll(e.stateDir, 0o700); err != nil {
		log.Printf("recorder: remember %s: %v", t.Payload(), err)
		return
	}
	if err := atomicfile.Write(e.sourcePath(), []byte(t.Payload()+"\n"), 0o600); err != nil {
		log.Printf("recorder: remember %s: %v", t.Payload(), err)
	}
}

func (e *GPUEngine) forgetSource() {
	if err := os.Remove(e.sourcePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("recorder: forget the remembered source: %v", err)
	}
}

// gpuSession is an opened screen, held across the start delay.
type gpuSession struct {
	screen *screen
	audio  *pulse.Service
}

// Start implements Session: the encoder on the screen's GPU, the webcam
// and the audio sources opts asks for, into a new file.
func (g *gpuSession) Start(opts Options, term func(reason string)) (Recording, error) {
	sources, err := audioSources(g.audio, opts)
	if err != nil {
		return nil, err
	}
	fps := max(opts.Framerate, 1)
	video, err := newGPUVideo(g.screen, fps)
	if err != nil {
		return nil, err
	}
	parts := recordingParts{video: video, audio: sources, fps: fps, now: time.Now}
	if opts.Webcam != nil {
		size := g.screen.frameSize()
		width, errW := intconv.Exact[int32](size.X)
		height, errH := intconv.Exact[int32](size.Y)
		if err := errors.Join(errW, errH); err != nil {
			video.Close()
			return nil, fmt.Errorf("recorder: a %dx%d frame: %w", size.X, size.Y, err)
		}
		parts.camRect = webcamRect(*opts.Webcam, width, height)
		cam, err := openWebcam(opts.Webcam.Device, parts.camRect.Size())
		if err != nil {
			video.Close()
			return nil, err
		}
		parts.cam = cam
	}
	return startRecording(parts, opts, term)
}

// Close releases the screen.
func (g *gpuSession) Close() { g.screen.Close() }

// audioSources are the captures opts asks for: the desktop (the
// default sink's monitor) and the microphone.
func audioSources(audio *pulse.Service, opts Options) ([]startAudio, error) {
	var targets []pulse.CaptureTarget
	if opts.SystemAudio {
		targets = append(targets, pulse.CaptureDefaultMonitor())
	}
	if opts.Microphone {
		mic := pulse.CaptureDefaultInput()
		if opts.MicrophoneDevice != "" {
			var err error
			if mic, err = pulse.CaptureNamed(opts.MicrophoneDevice); err != nil {
				return nil, fmt.Errorf("recorder: microphone: %w", err)
			}
		}
		targets = append(targets, mic)
	}
	if len(targets) == 0 {
		return nil, nil
	}
	if audio == nil {
		return nil, fmt.Errorf("recorder: %s asked for, but no audio server is connected", targets)
	}
	sources := make([]startAudio, len(targets))
	for i, t := range targets {
		sources[i] = func(onData func([]byte)) (audioCapture, error) {
			c, err := audio.Capture(t, pulse.CaptureSpec{Name: "lys-shell recorder", Rate: audioRate, Channels: audioChannels, FragmentFrames: audioFragment}, onData)
			if err != nil {
				return nil, fmt.Errorf("recorder: capture %s: %w", t, err)
			}
			return c, nil
		}
	}
	return sources, nil
}
