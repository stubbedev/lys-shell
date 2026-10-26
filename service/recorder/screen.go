package recorder

import (
	"errors"
	"fmt"
	"image"
	"log"
	"slices"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/gpu"

	"github.com/stubbedev/lys-shell/internal/dmabufprobe"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// The H.264 stream: a keyframe every two seconds at constant quality.
const (
	videoQP           = 20
	keyframeEverySecs = 2
)

// ErrNoVideoEncoder is a GPU that cannot encode H.264: the recorder
// has no other encoder.
var ErrNoVideoEncoder = gpu.ErrNoVideoEncoder

// errOutputGone is an output that left the compositor.
var errOutputGone = errors.New("recorder: the output is gone")

// screen is an output captured into GPU memory: the capture connection,
// the Vulkan device behind the DRM device the compositor names, and one
// buffer on it, in the first modifier that probes, that the compositor
// copies each frame of the whole output into. source is the part of
// that buffer recorded: all of it for an output, a region's pixels for
// a region.
type screen struct {
	client   *capture.Client
	output   capture.Output
	cursor   bool
	dev      *gpu.Device
	buf      *gpu.Buffer
	frame    *capture.DmabufBuffer
	modifier uint64
	source   image.Rectangle
}

// openScreen opens target, an output or a region of one in its logical
// coordinates (measured by logical), for capture into a buffer the
// device the compositor names can encode. A device without an H.264
// encoder is ErrNoVideoEncoder, found before any buffer is allocated.
func openScreen(target sharepicker.Target, cursor bool, logical LogicalSize, openDevice func(uint64) (*gpu.Device, error)) (_ *screen, err error) {
	output := target.Name
	c, err := capture.Connect()
	if err != nil {
		return nil, fmt.Errorf("recorder: connect to the compositor: %w", err)
	}
	s := &screen{client: c, cursor: cursor}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	o, ok := c.OutputByName(output)
	if !ok {
		return nil, fmt.Errorf("%w: %s", errOutputGone, output)
	}
	s.output = o
	offered, err := c.DmabufFormat(o, cursor)
	if err != nil {
		return nil, fmt.Errorf("recorder: output %s offers no screencopy into GPU memory: %w", output, err)
	}
	if s.source, err = targetSource(target, logical, image.Pt(offered.Width, offered.Height), o.Transform); err != nil {
		return nil, err
	}
	if s.dev, err = openDevice(offered.Device); err != nil {
		return nil, fmt.Errorf("recorder: the GPU output %s is drawn on: %w", output, err)
	}
	if err := requireH264(s.dev); err != nil {
		return nil, err
	}
	exported, err := s.dev.ExportModifiers(offered.Fourcc)
	if err != nil {
		return nil, fmt.Errorf("recorder: the modifiers %s allocates %v in: %w", s.dev.Name(), capture.FormatFromFourcc(offered.Fourcc), err)
	}
	probed, layout, err := dmabufprobe.First(dmabufprobe.Layouts(offered.Modifiers, nil, exported), func(l dmabufprobe.Layout) (*probedBuffer, error) {
		return s.probe(offered, l.Modifier)
	})
	if err != nil {
		return nil, fmt.Errorf("recorder: output %s: no %dx%d %v buffer on %s takes its frames: %w", output, offered.Width, offered.Height, capture.FormatFromFourcc(offered.Fourcc), s.dev.Name(), err)
	}
	s.buf, s.frame, s.modifier = probed.buf, probed.frame, layout.Modifier
	log.Printf("recorder: capturing %s (%dx%d %v, transform %d, recording %v) on %s in modifier %#x", target.Payload(), offered.Width, offered.Height,
		capture.FormatFromFourcc(offered.Fourcc), o.Transform, s.source, s.dev.Name(), s.modifier)
	return s, nil
}

// targetSource is the part of the output's buffer target records: the
// whole buffer for an output, the region's pixels for a region.
func targetSource(target sharepicker.Target, logical LogicalSize, buffer image.Point, t capture.Transform) (image.Rectangle, error) {
	if target.Kind != sharepicker.TargetRegion {
		return image.Rectangle{Max: buffer}, nil
	}
	size, ok := logical(target.Name)
	if !ok {
		return image.Rectangle{}, fmt.Errorf("%w: %s has no logical size", errOutputGone, target.Name)
	}
	region := image.Rect(int(target.X), int(target.Y), int(target.X+target.Width), int(target.Y+target.Height))
	return regionSource(region, size, buffer, t)
}

// requireH264 is ErrNoVideoEncoder for a device whose video queue does
// not encode H.264, or that has none.
func requireH264(d *gpu.Device) error {
	if !slices.Contains(d.Codecs(), gpu.H264) {
		return fmt.Errorf("recorder: %w: %s encodes no %s (it encodes %v)", ErrNoVideoEncoder, d.Name(), gpu.H264, d.Codecs())
	}
	return nil
}

type probedBuffer struct {
	buf   *gpu.Buffer
	frame *capture.DmabufBuffer
}

// probe allocates a buffer in modifier, has the compositor import it,
// and copies a frame into it.
func (s *screen) probe(offered capture.DmabufFormat, modifier uint64) (*probedBuffer, error) {
	b, err := s.dev.NewBuffer(offered.Width, offered.Height, offered.Fourcc, []uint64{modifier})
	if err != nil {
		return nil, err
	}
	frame, err := s.client.ImportDmabuf(b.Dmabuf())
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("import: %w", err)
	}
	if err := s.client.CopyOutputDmabuf(s.output, s.cursor, frame); err != nil {
		frame.Destroy()
		b.Close()
		return nil, fmt.Errorf("screencopy into it: %w", err)
	}
	return &probedBuffer{buf: b, frame: frame}, nil
}

// frameSize is the recorded frame's size in pixels: the source turned
// upright.
func (s *screen) frameSize() image.Point {
	return uprightSize(s.source.Size(), s.output.Transform)
}

// Close releases the buffer, the device and the connection.
func (s *screen) Close() {
	if s.frame != nil {
		s.frame.Destroy()
	}
	if s.buf != nil {
		s.buf.Close()
	}
	if s.dev != nil {
		s.dev.Close()
	}
	_ = s.client.Close()
}

// gpuVideo encodes the screen's buffer after each copy: the production
// videoEncoder. The encoder lives on the screen's device and is closed
// with the recording; the screen stays the session's.
type gpuVideo struct {
	s   *screen
	enc *gpu.Encoder
	// failing is whether the copies fail, logged when that starts and
	// when it stops rather than per frame.
	failing bool
}

// newGPUVideo makes the H.264 encoder for the screen at fps: its source
// turned upright by the output's transform.
func newGPUVideo(s *screen, fps uint32) (*gpuVideo, error) {
	size := s.frameSize()
	enc, err := s.dev.NewEncoder(gpu.EncoderConfig{
		Codec: gpu.H264, Width: size.X, Height: size.Y, Source: s.source, Transform: gpu.Transform(s.output.Transform),
		FrameRate: int(fps), KeyInterval: int(fps) * keyframeEverySecs, QP: videoQP,
	})
	if err != nil {
		return nil, fmt.Errorf("recorder: a %dx%d H.264 encoder at %d fps on %s: %w", size.X, size.Y, fps, s.dev.Name(), err)
	}
	if ps := enc.ParameterSets(); len(ps) != 2 {
		enc.Close()
		return nil, fmt.Errorf("recorder: the H.264 encoder on %s gave %d parameter sets, want an SPS and a PPS", s.dev.Name(), len(ps))
	}
	return &gpuVideo{s: s, enc: enc}, nil
}

func (v *gpuVideo) ParameterSets() (sps, pps []byte) {
	ps := v.enc.ParameterSets()
	return ps[0], ps[1]
}

func (v *gpuVideo) SetOverlay(img *image.RGBA, dst image.Rectangle) error {
	return v.enc.SetOverlay(img, dst)
}

// Frame copies the output into the buffer and encodes it. A failed copy
// skips the frame, unless the output is gone, which ends the recording.
func (v *gpuVideo) Frame(pts time.Duration) ([]gpu.Packet, error) {
	if err := v.s.client.CopyOutputDmabuf(v.s.output, v.s.cursor, v.s.frame); err != nil {
		if rerr := v.s.client.Refresh(); rerr != nil {
			return nil, fmt.Errorf("recorder: the compositor connection failed after a failed copy (%w): %w", err, rerr)
		}
		if _, ok := v.s.client.OutputByName(v.s.output.Name); !ok {
			return nil, fmt.Errorf("%w: %s", errOutputGone, v.s.output.Name)
		}
		if !v.failing {
			log.Printf("recorder: copying %s failing, frames skipped: %v", v.s.output.Name, err)
			v.failing = true
		}
		return nil, nil
	}
	if v.failing {
		log.Printf("recorder: copying %s recovered", v.s.output.Name)
		v.failing = false
	}
	pkts, err := v.enc.Encode(v.s.buf, pts)
	if err != nil {
		return nil, fmt.Errorf("recorder: encode the frame at %v: %w", pts, err)
	}
	return pkts, nil
}

func (v *gpuVideo) Flush() ([]gpu.Packet, error) { return v.enc.Flush() }

func (v *gpuVideo) Close() { v.enc.Close() }
