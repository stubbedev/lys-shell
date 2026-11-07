package pulsetest

import (
	"maps"
	"slices"

	"github.com/stubbedev/lys-shell/service/pulse/native"
)

// Play is one PLAY_SAMPLE the fake answered: Sink is the sink it named,
// empty for the default one.
type Play struct {
	Name   string
	Sink   string
	Volume native.Volume
	Props  native.PropList
}

type upload struct {
	name   string
	sample native.Sample
	length uint32
}

func (s *Server) uploadData(f native.Frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.uploads[f.Channel]; ok {
		u.sample.Data = append(u.sample.Data, f.Payload...)
	}
}

// Samples is the sample cache by name.
func (s *Server) Samples() map[string]native.Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.samples)
}

// Uploads counts the finished uploads.
func (s *Server) Uploads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploaded
}

// Plays lists the samples played, in order.
func (s *Server) Plays() []Play {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.plays)
}

// ForgetSamples empties the cache, as a server restart does.
func (s *Server) ForgetSamples() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = nil
}

func (c *conn) createUpload(tag uint32, r *native.Reader, failed func() bool) bool {
	name, _ := r.String()
	u := &upload{name: name}
	u.sample.SampleSpec = r.SampleSpec()
	u.sample.ChannelMap = r.ChannelMap()
	u.length = r.U32()
	if c.negotiated() >= 13 {
		_ = r.PropList()
	}
	if r.Err() != nil || !r.EOF() {
		return false
	}
	if failed() {
		return true
	}
	s := c.s
	s.mu.Lock()
	s.nextChannel++
	channel := s.nextChannel
	if s.uploads == nil {
		s.uploads = map[uint32]*upload{}
	}
	s.uploads[channel] = u
	s.mu.Unlock()
	var w native.Writer
	w.U32(channel)
	w.U32(u.length)
	c.reply(tag, &w)
	return true
}

func (c *conn) finishUpload(tag uint32, r *native.Reader, failed func() bool) bool {
	channel := r.U32()
	if r.Err() != nil || !r.EOF() {
		return false
	}
	if failed() {
		return true
	}
	s := c.s
	s.mu.Lock()
	u, ok := s.uploads[channel]
	delete(s.uploads, channel)
	if ok && len(u.sample.Data) == int(u.length) {
		if s.samples == nil {
			s.samples = map[string]native.Sample{}
		}
		s.samples[u.name] = u.sample
		s.uploaded++
	}
	s.mu.Unlock()
	if !ok {
		c.fail(tag, native.ErrNoEntity)
		return true
	}
	if len(u.sample.Data) != int(u.length) {
		c.fail(tag, native.ErrInvalid)
		return true
	}
	c.reply(tag, nil)
	return true
}

func (c *conn) playSample(tag uint32, r *native.Reader, failed func() bool) bool {
	_ = r.U32()
	sink, _ := r.String()
	volume := native.Volume(r.U32())
	name, _ := r.String()
	var props native.PropList
	if c.negotiated() >= 13 {
		props = r.PropList()
	}
	if r.Err() != nil || !r.EOF() {
		return false
	}
	if failed() {
		return true
	}
	s := c.s
	s.mu.Lock()
	_, ok := s.samples[name]
	if ok && sink != "" {
		_, ok = named(s.sinks, sink)
	}
	if ok {
		s.plays = append(s.plays, Play{Name: name, Sink: sink, Volume: volume, Props: props})
	}
	s.mu.Unlock()
	if !ok {
		c.fail(tag, native.ErrNoEntity)
		return true
	}
	var w native.Writer
	w.U32(0)
	c.reply(tag, &w)
	return true
}
