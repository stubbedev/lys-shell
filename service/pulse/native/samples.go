package native

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// uploadChunk is the most audio one upload frame carries, libpulse's
// default mempool block.
const uploadChunk = 64 << 10

// Sample is audio for the server's sample cache: interleaved frames in
// SampleSpec's format, laid out by ChannelMap.
type Sample struct {
	SampleSpec SampleSpec
	ChannelMap ChannelMap
	Data       []byte
}

func (s Sample) validate() error {
	frame := s.SampleSpec.FrameSize()
	switch {
	case frame == 0 || s.SampleSpec.Rate == 0:
		return fmt.Errorf("pulse: sample: unsupported spec %+v", s.SampleSpec)
	case len(s.ChannelMap) != int(s.SampleSpec.Channels):
		return fmt.Errorf("pulse: sample: channel map has %d positions for %d channels", len(s.ChannelMap), s.SampleSpec.Channels)
	case len(s.Data) == 0 || len(s.Data)%frame != 0:
		return fmt.Errorf("pulse: sample: %d bytes is not a whole number of %d-byte frames", len(s.Data), frame)
	case len(s.Data) > maxFrameSize:
		return fmt.Errorf("pulse: sample: %d bytes exceeds the %d the server caches", len(s.Data), maxFrameSize)
	}
	return nil
}

// UploadSample stores s in the server's sample cache under name
// (CREATE_UPLOAD_STREAM, the audio, FINISH_UPLOAD_STREAM), replacing any
// sample of that name. PlaySample then plays it without sending it again.
// The upload's event.id and media.name are name: pipewire-pulse files the
// sample under those rather than the name argument.
func (c *Conn) UploadSample(ctx context.Context, name string, s Sample) error {
	if err := s.validate(); err != nil {
		return err
	}
	var w Writer
	w.String(name)
	w.SampleSpec(s.SampleSpec)
	w.ChannelMap(s.ChannelMap)
	w.U32(uint32(len(s.Data))) //nolint:gosec // validate bounds the data by maxFrameSize
	w.PropList(PropList{"event.id": name, "media.name": name})
	reply, err := c.request(ctx, CmdCreateUploadStream, &w, nil)
	if err != nil {
		return fmt.Errorf("pulse: upload %s: %w", name, err)
	}
	channel := reply.U32()
	if err := reply.Err(); err != nil {
		return fmt.Errorf("pulse: upload %s: reply: %w", name, err)
	}
	if err := c.sendAudio(ctx, channel, s.Data); err != nil {
		c.fail(fmt.Errorf("pulse: write: %w", err))
		return fmt.Errorf("pulse: upload %s: %w", name, err)
	}
	if err := c.exec(ctx, CmdFinishUploadStream, byIndex(channel)); err != nil {
		return fmt.Errorf("pulse: upload %s: %w", name, err)
	}
	return nil
}

func (c *Conn) sendAudio(ctx context.Context, channel uint32, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.nc.SetWriteDeadline(deadline)
		defer func() { _ = c.nc.SetWriteDeadline(time.Time{}) }()
	}
	for len(data) > 0 {
		n := min(len(data), uploadChunk)
		if err := WriteFrame(c.nc, Frame{Channel: channel, Payload: data[:n]}); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// SamplePlay is one play of a cached sample: Name on the sink named Sink,
// or on the default sink when Sink is empty, at Volume; Props describe this
// playback (media.role and friends).
type SamplePlay struct {
	Name   string
	Sink   string
	Volume Volume
	Props  PropList
}

// ErrNoSample is a sample the server's cache does not hold.
var ErrNoSample = errors.New("pulse: no such sample in the server's cache")

// PlaySample plays a cached sample (PLAY_SAMPLE). A name the cache lacks
// is ErrNoSample.
func (c *Conn) PlaySample(ctx context.Context, p SamplePlay) error {
	var w Writer
	w.U32(InvalidIndex)
	w.OptString(p.Sink)
	w.U32(uint32(p.Volume))
	w.String(p.Name)
	w.PropList(p.Props)
	if _, err := c.request(ctx, CmdPlaySample, &w, nil); err != nil {
		if errors.Is(err, ErrNoEntity) {
			return fmt.Errorf("pulse: play %s: %w", p.Name, ErrNoSample)
		}
		return fmt.Errorf("pulse: play %s: %w", p.Name, err)
	}
	return nil
}
