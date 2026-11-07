// Package pulse is the audio service, the Go port of
// crates/wayle-audio: sinks, sources, and per-application streams kept
// live from PulseAudio's subscription events, the default devices,
// per-channel volume and mute, ports, stream routing, and record
// streams, all over the native protocol (package native), so it works
// against PulseAudio and pipewire-pulse alike with no libpulse.
package pulse

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/stubbedev/lys-shell/service/pulse/native"
)

// clientName is the application.name the Rust backend's context used.
const clientName = "lys-shell-pulse"

// subscriptionMask is backend/events's interest set.
const subscriptionMask = native.MaskSink | native.MaskSource | native.MaskSinkInput |
	native.MaskSourceOutput | native.MaskServer

const dialTimeout = 5 * time.Second

var (
	reconnectMin = 250 * time.Millisecond
	reconnectMax = 5 * time.Second
)

// ErrNoDefaultDevice reports that the server names no default device
// of the requested kind, or names one not discovered yet.
var ErrNoDefaultDevice = errors.New("pulse: no default device")

// ErrEmptyVolume rejects a volume with no channels (PulseAudio would
// answer PA_ERR_INVALID).
var ErrEmptyVolume = errors.New("pulse: volume has no channels")

// ErrDisconnected reports a request made while the service has no
// connection to the server.
var ErrDisconnected = errors.New("pulse: not connected to the server")

// ErrClosed reports a request made after Close.
var ErrClosed = errors.New("pulse: service closed")

// Service is the live audio state. Every change PulseAudio announces
// is folded in as it arrives; the getters return snapshots that the
// caller owns. A lost connection empties the state and is redialled
// until Close, refilling it. It is safe for concurrent use.
type Service struct {
	server  string
	life    context.Context
	close   context.CancelFunc
	stopped chan struct{}

	mu            sync.RWMutex
	sess          *session
	outputs       map[uint32]OutputDevice
	inputs        map[uint32]InputDevice
	streams       map[StreamKey]AudioStream
	defaultSink   string
	defaultSource string
	subs          map[int]chan struct{}
	nextSub       int
}

type session struct {
	conn  *native.Conn
	ready chan struct{}
	abort chan struct{}
	// samplesMu serializes sample plays; samples are the names uploaded
	// to this connection's server.
	samplesMu sync.Mutex
	samples   map[string]bool
}

type snapshot struct {
	sinks, sources []native.DeviceInfo
	inputs         []native.StreamInfo
	outputs        []native.StreamInfo
	info           native.ServerInfo
}

func newService(server string) *Service {
	life, cancel := context.WithCancel(context.Background())
	return &Service{
		server:  server,
		life:    life,
		close:   cancel,
		stopped: make(chan struct{}),
		outputs: map[uint32]OutputDevice{},
		inputs:  map[uint32]InputDevice{},
		streams: map[StreamKey]AudioStream{},
		subs:    map[int]chan struct{}{},
	}
}

// Connect dials the server (server "" resolves like libpulse:
// $PULSE_SERVER, then $XDG_RUNTIME_DIR/pulse/native) and discovers
// every device, stream, and default before returning, so the first
// snapshot is complete; a later loss reconnects as Start does.
func Connect(ctx context.Context, server string) (*Service, error) {
	s := newService(server)
	sess, err := s.open(ctx)
	if err != nil {
		s.close()
		return nil, err
	}
	go s.supervise(sess)
	return s, nil
}

// Start returns at once and connects in the background, retrying until
// the server is up and again whenever the connection is lost; the
// state is empty while there is none.
func Start(server string) *Service {
	s := newService(server)
	go s.supervise(nil)
	return s
}

// open dials one connection and installs its discovery. The
// subscription comes first so no change in between is lost: events
// queue until ready closes.
func (s *Service) open(ctx context.Context) (*session, error) {
	sess := &session{ready: make(chan struct{}), abort: make(chan struct{})}
	conn, err := native.Dial(ctx, native.Options{
		Server:      s.server,
		ClientName:  clientName,
		OnSubscribe: func(ev native.SubscribeEvent) { s.handleEvent(sess, ev) },
	})
	if err != nil {
		return nil, fmt.Errorf("pulse: cannot connect to the server: %w", err)
	}
	sess.conn = conn
	snap, err := discover(ctx, conn)
	if err != nil {
		close(sess.abort)
		_ = conn.Close()
		return nil, err
	}
	s.install(sess, snap)
	close(sess.ready)
	return sess, nil
}

func discover(ctx context.Context, conn *native.Conn) (snapshot, error) {
	var snap snapshot
	var err error
	if err = conn.Subscribe(ctx, subscriptionMask); err != nil {
		return snap, err
	}
	if snap.sinks, err = conn.Sinks(ctx); err != nil {
		return snap, err
	}
	if snap.sources, err = conn.Sources(ctx); err != nil {
		return snap, err
	}
	if snap.inputs, err = conn.SinkInputs(ctx); err != nil {
		return snap, err
	}
	if snap.outputs, err = conn.SourceOutputs(ctx); err != nil {
		return snap, err
	}
	snap.info, err = conn.ServerInfo(ctx)
	return snap, err
}

func (s *Service) install(sess *session, snap snapshot) {
	s.mu.Lock()
	s.sess = sess
	s.clearLocked()
	for _, d := range snap.sinks {
		s.outputs[d.Index] = fromSink(d)
	}
	for _, d := range snap.sources {
		s.inputs[d.Index] = fromSource(d)
	}
	for _, st := range snap.inputs {
		s.streams[StreamKey{Index: st.Index, Type: StreamPlayback}] = fromStream(st, StreamPlayback)
	}
	for _, st := range snap.outputs {
		s.streams[StreamKey{Index: st.Index, Type: StreamRecord}] = fromStream(st, StreamRecord)
	}
	s.defaultSink, s.defaultSource = snap.info.DefaultSink, snap.info.DefaultSource
	s.mu.Unlock()
	s.notify()
}

func (s *Service) drop(sess *session) {
	s.mu.Lock()
	if s.sess != sess {
		s.mu.Unlock()
		return
	}
	s.sess = nil
	s.clearLocked()
	s.mu.Unlock()
	s.notify()
}

func (s *Service) clearLocked() {
	clear(s.outputs)
	clear(s.inputs)
	clear(s.streams)
	s.defaultSink, s.defaultSource = "", ""
}

func (s *Service) supervise(sess *session) {
	defer close(s.stopped)
	for {
		if sess != nil {
			select {
			case <-sess.conn.Done():
				log.Printf("pulse: connection lost: %v; reconnecting", sess.conn.Err())
				s.drop(sess)
			case <-s.life.Done():
				_ = sess.conn.Close()
				s.drop(sess)
				return
			}
		}
		if sess = s.reconnect(); sess == nil {
			return
		}
	}
}

func (s *Service) reconnect() *session {
	delay := reconnectMin
	failing := false
	for {
		ctx, cancel := context.WithTimeout(s.life, dialTimeout)
		sess, err := s.open(ctx)
		cancel()
		if err == nil {
			if failing {
				log.Print("pulse: connected to the server")
			}
			return sess
		}
		if s.life.Err() != nil {
			return nil
		}
		if !failing {
			log.Printf("%v; retrying until it is up", err)
			failing = true
		}
		select {
		case <-s.life.Done():
			return nil
		case <-time.After(delay):
		}
		delay = min(2*delay, reconnectMax)
	}
}

// Close disconnects for good and closes every subscriber channel.
func (s *Service) Close() error {
	s.close()
	<-s.stopped
	s.closeSubscribers()
	return nil
}

func (s *Service) current() (*native.Conn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentLocked()
}

func (s *Service) currentLocked() (*native.Conn, error) {
	if s.life.Err() != nil {
		return nil, ErrClosed
	}
	if s.sess == nil {
		return nil, ErrDisconnected
	}
	return s.sess.conn, nil
}

// handleEvent folds one subscription event of sess (backend/events):
// a removal drops the object, a new or changed object is re-queried
// by index, a server change re-reads the defaults. Queries that race a
// removal (no such entity) are dropped, as the Rust callbacks never
// fire for them, and so is anything from a connection since replaced.
func (s *Service) handleEvent(sess *session, ev native.SubscribeEvent) {
	select {
	case <-sess.ready:
	case <-sess.abort:
		return
	}
	ctx := context.Background()
	var changed bool
	var err error
	switch ev.Facility {
	case native.FacilitySink:
		changed, err = foldObject(s, sess, ev, s.outputs, sess.conn.Sink, fromSink)
	case native.FacilitySource:
		changed, err = foldObject(s, sess, ev, s.inputs, sess.conn.Source, fromSource)
	case native.FacilitySinkInput:
		changed, err = s.foldStream(ctx, sess, ev, StreamPlayback, sess.conn.SinkInput)
	case native.FacilitySourceOutput:
		changed, err = s.foldStream(ctx, sess, ev, StreamRecord, sess.conn.SourceOutput)
	case native.FacilityServer:
		if ev.Operation == native.OpChange {
			changed, err = s.refreshDefaults(ctx, sess)
		}
	}
	if err != nil && !errors.Is(err, native.ErrNoEntity) {
		select {
		case <-sess.conn.Done():
		default:
			log.Printf("pulse: refresh after %v: %v", ev, err)
		}
	}
	if changed {
		s.notify()
	}
}

func foldObject[T any](s *Service, sess *session, ev native.SubscribeEvent, m map[uint32]T,
	query func(context.Context, uint32) (native.DeviceInfo, error), convert func(native.DeviceInfo) T,
) (bool, error) {
	if ev.Operation == native.OpRemove {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sess != sess {
			return false, nil
		}
		_, had := m[ev.Index]
		delete(m, ev.Index)
		return had, nil
	}
	info, err := query(context.Background(), ev.Index)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != sess {
		return false, nil
	}
	m[ev.Index] = convert(info)
	return true, nil
}

func (s *Service) foldStream(ctx context.Context, sess *session, ev native.SubscribeEvent, t StreamType,
	query func(context.Context, uint32) (native.StreamInfo, error),
) (bool, error) {
	key := StreamKey{Index: ev.Index, Type: t}
	if ev.Operation == native.OpRemove {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.sess != sess {
			return false, nil
		}
		_, had := s.streams[key]
		delete(s.streams, key)
		return had, nil
	}
	info, err := query(ctx, ev.Index)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != sess {
		return false, nil
	}
	s.streams[key] = fromStream(info, t)
	return true, nil
}

func (s *Service) refreshDefaults(ctx context.Context, sess *session) (bool, error) {
	info, err := sess.conn.ServerInfo(ctx)
	if err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != sess {
		return false, nil
	}
	changed := s.defaultSink != info.DefaultSink || s.defaultSource != info.DefaultSource
	s.defaultSink, s.defaultSource = info.DefaultSink, info.DefaultSource
	return changed, nil
}

// Subscribe ticks after every applied change, a lost or regained
// connection included; ticks coalesce, so a slow reader sees the
// latest state on its next read rather than a backlog. The channel
// closes when ctx ends or the service closes; stop unsubscribes early.
func (s *Service) Subscribe(ctx context.Context) (<-chan struct{}, func(), error) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.life.Err() != nil {
		s.mu.Unlock()
		return nil, nil, fmt.Errorf("pulse: subscribe: %w", ErrClosed)
	}
	id := s.nextSub
	s.nextSub++
	s.subs[id] = ch
	s.mu.Unlock()
	quit := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(quit)
			s.mu.Lock()
			if c, ok := s.subs[id]; ok {
				delete(s.subs, id)
				close(c)
			}
			s.mu.Unlock()
		})
	}
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-quit:
		case <-s.stopped:
		}
	}()
	return ch, stop, nil
}

func (s *Service) notify() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Service) closeSubscribers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ch := range s.subs {
		delete(s.subs, id)
		close(ch)
	}
}

func sortedValues[K comparable, V any](m map[K]V, less func(a, b V) int, clone func(V) V) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, clone(v))
	}
	slices.SortFunc(out, less)
	return out
}

func byDeviceIndex[T interface{ key() DeviceKey }](a, b T) int {
	return cmp.Compare(a.key().Index, b.key().Index)
}

func (d OutputDevice) key() DeviceKey { return d.Key }
func (d InputDevice) key() DeviceKey  { return d.Key }

func (d OutputDevice) clone() OutputDevice {
	d.Device = d.Device.clone()
	return d
}

func (d InputDevice) clone() InputDevice {
	d.Device = d.Device.clone()
	return d
}

// OutputDevices lists every sink by index.
func (s *Service) OutputDevices() []OutputDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.outputs, byDeviceIndex[OutputDevice], OutputDevice.clone)
}

// InputDevices lists every source by index, monitors included.
func (s *Service) InputDevices() []InputDevice {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sortedValues(s.inputs, byDeviceIndex[InputDevice], InputDevice.clone)
}

// PlaybackStreams lists the applications playing audio.
func (s *Service) PlaybackStreams() []AudioStream { return s.streamsOf(StreamPlayback) }

// RecordingStreams lists the applications recording audio.
func (s *Service) RecordingStreams() []AudioStream { return s.streamsOf(StreamRecord) }

func (s *Service) streamsOf(t StreamType) []AudioStream {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []AudioStream
	for k, st := range s.streams {
		if k.Type == t {
			out = append(out, st.clone())
		}
	}
	slices.SortFunc(out, func(a, b AudioStream) int { return cmp.Compare(a.Key.Index, b.Key.Index) })
	return out
}

// DefaultOutput is the default sink. The default resolves by name on
// every read, so a default naming a sink not discovered yet appears
// the moment it is, rather than the previous device lingering.
func (s *Service) DefaultOutput() (OutputDevice, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return findByName(s.outputs, s.defaultSink, func(d OutputDevice) string { return d.Name })
}

// DefaultInput is the default source.
func (s *Service) DefaultInput() (InputDevice, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return findByName(s.inputs, s.defaultSource, func(d InputDevice) string { return d.Name })
}

func findByName[T interface{ clone() T }](m map[uint32]T, name string, nameOf func(T) string) (T, bool) {
	var zero T
	if name == "" {
		return zero, false
	}
	for _, d := range m {
		if nameOf(d) == name {
			return d.clone(), true
		}
	}
	return zero, false
}

// OutputDevice snapshots one sink.
func (s *Service) OutputDevice(index uint32) (OutputDevice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.outputs[index]
	if !ok {
		return OutputDevice{}, &DeviceNotFoundError{Key: DeviceKey{Index: index, Type: DeviceOutput}}
	}
	return d.clone(), nil
}

// InputDevice snapshots one source.
func (s *Service) InputDevice(index uint32) (InputDevice, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.inputs[index]
	if !ok {
		return InputDevice{}, &DeviceNotFoundError{Key: DeviceKey{Index: index, Type: DeviceInput}}
	}
	return d.clone(), nil
}

// AudioStream snapshots one stream.
func (s *Service) AudioStream(key StreamKey) (AudioStream, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st, ok := s.streams[key]
	if !ok {
		return AudioStream{}, &StreamNotFoundError{Key: key}
	}
	return st.clone(), nil
}

// device looks a key up on the current connection, returning its name.
func (s *Service) device(key DeviceKey) (string, *native.Conn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, err := s.currentLocked()
	if err != nil {
		return "", nil, err
	}
	switch key.Type {
	case DeviceOutput:
		if d, ok := s.outputs[key.Index]; ok {
			return d.Name, conn, nil
		}
	case DeviceInput:
		if d, ok := s.inputs[key.Index]; ok {
			return d.Name, conn, nil
		}
	}
	return "", nil, &DeviceNotFoundError{Key: key}
}

func (s *Service) stream(key StreamKey) (*native.Conn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	conn, err := s.currentLocked()
	if err != nil {
		return nil, err
	}
	if _, ok := s.streams[key]; !ok {
		return nil, &StreamNotFoundError{Key: key}
	}
	return conn, nil
}

// SetDeviceVolume sets a device's per-channel volume.
func (s *Service) SetDeviceVolume(ctx context.Context, key DeviceKey, v Volume) error {
	if v.Channels() == 0 {
		return ErrEmptyVolume
	}
	_, conn, err := s.device(key)
	if err != nil {
		return err
	}
	if key.Type == DeviceOutput {
		return conn.SetSinkVolume(ctx, key.Index, v.toPulse())
	}
	return conn.SetSourceVolume(ctx, key.Index, v.toPulse())
}

// SetDeviceMute mutes or unmutes a device.
func (s *Service) SetDeviceMute(ctx context.Context, key DeviceKey, muted bool) error {
	_, conn, err := s.device(key)
	if err != nil {
		return err
	}
	if key.Type == DeviceOutput {
		return conn.SetSinkMute(ctx, key.Index, muted)
	}
	return conn.SetSourceMute(ctx, key.Index, muted)
}

// SetDevicePort switches a device's active port (headphones vs
// speakers).
func (s *Service) SetDevicePort(ctx context.Context, key DeviceKey, port string) error {
	_, conn, err := s.device(key)
	if err != nil {
		return err
	}
	if key.Type == DeviceOutput {
		return conn.SetSinkPort(ctx, key.Index, port)
	}
	return conn.SetSourcePort(ctx, key.Index, port)
}

// SetDefault makes a device the default of its kind.
func (s *Service) SetDefault(ctx context.Context, key DeviceKey) error {
	name, conn, err := s.device(key)
	if err != nil {
		return err
	}
	if key.Type == DeviceOutput {
		return conn.SetDefaultSink(ctx, name)
	}
	return conn.SetDefaultSource(ctx, name)
}

// SetStreamVolume sets a stream's per-channel volume.
func (s *Service) SetStreamVolume(ctx context.Context, key StreamKey, v Volume) error {
	if v.Channels() == 0 {
		return ErrEmptyVolume
	}
	conn, err := s.stream(key)
	if err != nil {
		return err
	}
	if key.Type == StreamPlayback {
		return conn.SetSinkInputVolume(ctx, key.Index, v.toPulse())
	}
	return conn.SetSourceOutputVolume(ctx, key.Index, v.toPulse())
}

// SetStreamMute mutes or unmutes a stream.
func (s *Service) SetStreamMute(ctx context.Context, key StreamKey, muted bool) error {
	conn, err := s.stream(key)
	if err != nil {
		return err
	}
	if key.Type == StreamPlayback {
		return conn.SetSinkInputMute(ctx, key.Index, muted)
	}
	return conn.SetSourceOutputMute(ctx, key.Index, muted)
}

// MoveStream routes a stream to another device: a playback stream to
// a sink, a recording stream to a source.
func (s *Service) MoveStream(ctx context.Context, key StreamKey, device DeviceKey) error {
	if device.Type != key.Type.deviceType() {
		return fmt.Errorf("pulse: a %s stream cannot move to an %s device", key.Type, device.Type)
	}
	conn, err := s.stream(key)
	if err != nil {
		return err
	}
	if _, _, err := s.device(device); err != nil {
		return err
	}
	if key.Type == StreamPlayback {
		return conn.MoveSinkInput(ctx, key.Index, device.Index)
	}
	return conn.MoveSourceOutput(ctx, key.Index, device.Index)
}
