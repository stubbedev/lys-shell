package recorder

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/stubbedev/lys-lib/gpu"

	"github.com/stubbedev/lys-shell/internal/atomicfile"
	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// outputFile keeps the output the user let the recorder remember, in
// the state directory, as the picker's payload.
const outputFile = "recorder-output"

// pickerAppID names the recorder to the share picker.
const pickerAppID = "lys-shell"

// dismissTimeout bounds taking down a picker whose start was
// cancelled.
const dismissTimeout = 2 * time.Second

// audioFragment is the PCM chunk a source delivers, in frames: 10 ms.
const audioFragment = audioRate / 100

// ErrPickerCancelled is a start the user dismissed at the picker.
var ErrPickerCancelled = errors.New("recorder: no output was picked")

// Picker shows the share picker: sharepicker.Client.
type Picker interface {
	Pick(ctx context.Context, caller portalreq.Caller, r sharepicker.Request) (string, error)
	Dismiss(ctx context.Context, handle string) error
}

// GPUEngine is the production Engine: the share picker for the output,
// screencopy into a buffer on the GPU behind it, the GPU's H.264
// encoder, and the audio service for the microphone and the desktop.
type GPUEngine struct {
	picker     Picker
	audio      *pulse.Service
	stateDir   string
	openDevice func(uint64) (*gpu.Device, error)
	picks      atomic.Uint64
}

// NewGPUEngine records through picker, with audio from audio (nil when
// there is no audio server, which fails a recording that asks for
// sound), remembering the picked output in stateDir ("" remembers
// nothing).
func NewGPUEngine(picker Picker, audio *pulse.Service, stateDir string) *GPUEngine {
	return &GPUEngine{picker: picker, audio: audio, stateDir: stateDir, openDevice: gpu.OpenNode}
}

// OpenSession implements Engine: the remembered output, else the one
// the user picks, opened for capture on its GPU.
func (e *GPUEngine) OpenSession(ctx context.Context, showCursor bool) (Session, error) {
	if name, ok := e.rememberedOutput(); ok {
		s, err := openScreen(name, showCursor, e.openDevice)
		if !errors.Is(err, errOutputGone) {
			return e.session(s, err)
		}
		log.Printf("recorder: the remembered output %s is gone; asking again", name)
		e.forgetOutput()
	}
	name, remember, err := e.pick(ctx)
	if err != nil {
		return nil, err
	}
	s, err := openScreen(name, showCursor, e.openDevice)
	if err == nil && remember {
		e.rememberOutput(name)
	}
	return e.session(s, err)
}

func (e *GPUEngine) session(s *screen, err error) (Session, error) {
	if err != nil {
		return nil, err
	}
	return &gpuSession{screen: s, audio: e.audio}, nil
}

// pick asks the share picker for one output. A cancelled ctx takes the
// picker down.
func (e *GPUEngine) pick(ctx context.Context) (name string, remember bool, err error) {
	handle := fmt.Sprintf("/dev/stubbe/lys/Recorder/pick/%d", e.picks.Add(1))
	reply, err := e.picker.Pick(ctx, portalreq.Caller{Handle: handle, AppID: pickerAppID}, sharepicker.Request{
		Sources: sharepicker.SourceOutputs, AllowToken: e.stateDir != "",
	})
	if ctx.Err() != nil {
		dctx, cancel := context.WithTimeout(context.Background(), dismissTimeout)
		defer cancel()
		if derr := e.picker.Dismiss(dctx, handle); derr != nil {
			log.Printf("recorder: take down the cancelled picker: %v", derr)
		}
		return "", false, ctx.Err()
	}
	if err != nil {
		return "", false, fmt.Errorf("recorder: the share picker: %w", err)
	}
	if reply == "" {
		return "", false, ErrPickerCancelled
	}
	remember, targets, ok := sharepicker.ParseReply(reply)
	if !ok || len(targets) != 1 || targets[0].Kind != sharepicker.TargetOutput {
		return "", false, fmt.Errorf("recorder: the share picker replied %q, not one output", reply)
	}
	return targets[0].Name, remember, nil
}

func (e *GPUEngine) outputPath() string { return filepath.Join(e.stateDir, outputFile) }

// rememberedOutput is the output a past pick let the recorder keep.
func (e *GPUEngine) rememberedOutput() (string, bool) {
	if e.stateDir == "" {
		return "", false
	}
	data, err := os.ReadFile(e.outputPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		log.Printf("recorder: read the remembered output: %v", err)
		return "", false
	}
	t, ok := sharepicker.ParseTarget(strings.TrimSpace(string(data)))
	if !ok || t.Kind != sharepicker.TargetOutput {
		log.Printf("recorder: %s holds %q, not an output; asking again", e.outputPath(), data)
		e.forgetOutput()
		return "", false
	}
	return t.Name, true
}

func (e *GPUEngine) rememberOutput(name string) {
	if err := os.MkdirAll(e.stateDir, 0o700); err != nil {
		log.Printf("recorder: remember output %s: %v", name, err)
		return
	}
	payload := sharepicker.Target{Kind: sharepicker.TargetOutput, Name: name}.Payload()
	if err := atomicfile.Write(e.outputPath(), []byte(payload+"\n"), 0o600); err != nil {
		log.Printf("recorder: remember output %s: %v", name, err)
	}
}

func (e *GPUEngine) forgetOutput() {
	if err := os.Remove(e.outputPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("recorder: forget the remembered output: %v", err)
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
		size := g.screen.size()
		parts.camRect = webcamRect(*opts.Webcam, int32(size.X), int32(size.Y))
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
