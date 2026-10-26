// Package pipewire is the PipeWire video producer lys-shell's ScreenCast
// portal streams through: a node exported over PipeWire's native
// protocol (a client-node, as libpipewire's pw_stream exports one), fed
// one captured frame per cycle the graph drives, stamped with the
// header, transform and damage metas. The protocol, the shared memory
// and the realtime cycle are spoken here in Go; no libpipewire.
package pipewire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Rect is a damaged region in buffer pixels.
type Rect struct{ X, Y, Width, Height int }

// Source fills one frame: Fill copies stride-byte rows into dst and
// reports what changed (nil or empty is the whole frame); ok false
// skips this cycle (nothing new, or the capture failed).
type Source interface {
	Fill(dst []byte) (damage []Rect, ok bool)
}

// Resizable is a Source whose frames change size (a window being
// resized): Size is the layout of the frames it has now. When that
// differs from the stream's, the producer offers the new size and
// holds frames back until the consumer has renegotiated.
type Resizable interface {
	Source
	Size() (width, height, stride uint32)
}

// DmabufSource is a Source that also fills GPU buffers: the producer
// offers its modifier's format first and, when the consumer takes it,
// binds one buffer from AllocDmabuf to each PipeWire buffer for the
// stream's life and fills it in place (zero copy). A consumer that
// takes the plain format gets producer-allocated shared memory, filled
// through Fill.
type DmabufSource interface {
	Source
	Modifier() uint64
	// DmabufVideoFormat is the GPU buffers' format, which need not be
	// the shared-memory one (a GPU compositor hands out 24-bit shm and
	// 32-bit dmabufs).
	DmabufVideoFormat() VideoFormat
	AllocDmabuf() (Dmabuf, error)
	FillDmabuf(Dmabuf) (damage []Rect, ok bool)
}

// Dmabuf is one GPU buffer's single plane.
type Dmabuf interface {
	Fd() int
	Offset() uint32
	Stride() uint32
	Release()
}

// Config is one producer's fixed stream geometry.
type Config struct {
	// Name is the node name the consumer sees.
	Name                  string
	Width, Height, Stride uint32
	Format                VideoFormat
	// FPS is the advertised and paced frame rate.
	FPS uint32
	// Transform is the constant spa_meta_videotransform value (the
	// output's wl_output transform; 0 for a window).
	Transform uint32
	Source    Source
}

// Producer is one running PipeWire video output stream. Its Source is
// called from one goroutine at a time.
type Producer struct {
	k *core
	n *node

	done      chan struct{}
	endOnce   sync.Once
	errMu     sync.Mutex
	err       error
	closeOnce sync.Once
	closing   chan struct{}

	cfg Config
	// dmabuf is the source's GPU side when it has one; useDmabuf is set
	// once the consumer negotiated the modifier format.
	dmabuf    DmabufSource
	useDmabuf bool
	// resizable is the source's size when it can change; renegotiating
	// holds frames back from a size change until the consumer took the
	// new format.
	resizable     Resizable
	renegotiating bool
	format        []byte

	start       time.Time
	minInterval time.Duration
	last        time.Time
	seq         uint64
	damage      []Rect
}

// connectTimeout bounds each wait on the server while starting.
const connectTimeout = 5 * time.Second

// Start exports the stream's node and returns once the server has
// registered it, so a consumer can link to NodeID.
func Start(cfg Config) (*Producer, error) {
	if cfg.FPS == 0 {
		cfg.FPS = 1
	}
	if cfg.Name == "" {
		cfg.Name = "lys-shell-screencast"
	}
	p := &Producer{
		cfg: cfg, done: make(chan struct{}), closing: make(chan struct{}), start: time.Now(),
		minInterval: time.Second * 10 / time.Duration(cfg.FPS*12),
		damage:      make([]Rect, 0, maxDamageRegions),
	}
	p.last = p.start.Add(-p.minInterval)
	p.dmabuf, _ = cfg.Source.(DmabufSource)
	p.resizable, _ = cfg.Source.(Resizable)
	k, err := connectCore(connectTimeout, p.stopped)
	if err != nil {
		return nil, err
	}
	var portFlags uint64
	if p.dmabuf != nil {
		portFlags = portFlagCanAlloc
	}
	p.k = k
	p.n = newNode(k, p, directionOutput, []dictItem{
		{"media.type", "Video"},
		{"media.category", "Capture"},
		{"media.role", "Screen"},
		{"media.class", "Stream/Output/Video"},
		{"media.name", cfg.Name},
		{"node.name", cfg.Name},
		{"node.async", "true"},
		{"node.want-driver", "true"},
		{"stream.is-live", "true"},
	}, portFlags)
	if err := p.n.export(connectTimeout); err != nil {
		p.Close()
		return nil, fmt.Errorf("pipewire: export the stream node: %w", err)
	}
	return p, nil
}

// NodeID is the node the consumer connects to.
func (p *Producer) NodeID() uint32 { return p.n.global }

// Done is closed once the stream has ended: closed, or cut off by the
// server (Err says why).
func (p *Producer) Done() <-chan struct{} { return p.done }

// Err is why the stream ended by itself; nil while it runs and after
// Close.
func (p *Producer) Err() error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return p.err
}

// stopped is the event loop's end: the server went away or broke the
// protocol, or Close stopped it.
func (p *Producer) stopped(err error) {
	select {
	case <-p.closing:
	default:
		p.errMu.Lock()
		p.err = err
		p.errMu.Unlock()
	}
	p.endOnce.Do(func() { close(p.done) })
}

// Close stops the stream and releases everything it holds.
func (p *Producer) Close() {
	p.closeOnce.Do(func() {
		close(p.closing)
		if err := p.n.deactivate(); err != nil {
			p.k.abort(err)
		}
		p.k.close()
		p.n.release()
	})
}

// portParams is the formats the stream offers and, once one is
// negotiated, its buffer layout and metas. A DmabufSource's modifier
// format comes first.
func (p *Producer) portParams() []portParam {
	var enum [][]byte
	if p.dmabuf != nil {
		mod := p.dmabuf.Modifier()
		enum = append(enum, formatPod(p.cfg.Width, p.cfg.Height, p.cfg.FPS, p.dmabuf.DmabufVideoFormat(), &mod))
	}
	enum = append(enum, formatPod(p.cfg.Width, p.cfg.Height, p.cfg.FPS, p.cfg.Format, nil))
	params := []portParam{
		{id: paramEnumFormat, flags: paramInfoRead, pods: enum},
		{id: paramMeta, flags: paramInfoRead, pods: [][]byte{busyMetaPod()}},
		{id: paramFormat, flags: paramInfoWrite},
		{id: paramBuffers},
	}
	if p.format == nil {
		return params
	}
	layout := bufferPodsOf(p.cfg.Stride, p.cfg.Height, p.dataTypes())
	params[1].pods = append(params[1].pods, layout[1:]...)
	params[2] = portParam{id: paramFormat, flags: paramInfoRead | paramInfoWrite, pods: [][]byte{p.format}}
	params[3] = portParam{id: paramBuffers, flags: paramInfoRead, pods: layout[:1]}
	return params
}

// dataTypes is the spa_data types the buffers' data comes in: a
// DmabufSource allocates every buffer itself, a GPU buffer or a memfd
// per the negotiated format; otherwise the server's shared memory.
func (p *Producer) dataTypes() uint32 {
	switch {
	case p.dmabuf == nil:
		return 1<<dataMemFd | 1<<dataMemPtr
	case p.useDmabuf:
		return 1 << dataDmaBuf
	}
	return 1 << dataMemFd
}

// setFormat takes the format the consumer and the server settled on,
// which must be one the stream offers.
func (p *Producer) setFormat(f []byte) error {
	if f == nil {
		p.format, p.useDmabuf = nil, false
		return nil
	}
	if typ, v, ok := propValue(f, formatMediaType); !ok || typ != typeID || len(v) < 4 || binary.LittleEndian.Uint32(v) != mediaTypeVideo {
		return errors.New("not a video format")
	}
	if typ, v, ok := propValue(f, formatMediaSubtype); !ok || typ != typeID || len(v) < 4 || binary.LittleEndian.Uint32(v) != mediaSubtypeRaw {
		return errors.New("not raw video")
	}
	typ, v, ok := propValue(f, formatVideoFormat)
	if !ok || typ != typeID || len(v) < 4 {
		return errors.New("no video format")
	}
	video := VideoFormat(binary.LittleEndian.Uint32(v))
	mtyp, mv, dmabuf := propValue(f, formatVideoModifier)
	want := p.cfg.Format
	if dmabuf {
		if p.dmabuf == nil {
			return errors.New("a dmabuf format, and the source has no GPU buffers")
		}
		if mtyp != typeLong || len(mv) < 8 || binary.LittleEndian.Uint64(mv) != p.dmabuf.Modifier() {
			return fmt.Errorf("modifier is not the offered %#x", p.dmabuf.Modifier())
		}
		want = p.dmabuf.DmabufVideoFormat()
	}
	if video != want {
		return fmt.Errorf("video format %d is not the offered %d", video, want)
	}
	w, h, ok := formatSize(f)
	if !ok || w != p.cfg.Width || h != p.cfg.Height {
		return fmt.Errorf("size %dx%d is not the offered %dx%d", w, h, p.cfg.Width, p.cfg.Height)
	}
	p.format, p.useDmabuf, p.renegotiating = append([]byte(nil), f...), dmabuf, false
	return nil
}

// allocData gives a buffer its memory: a GPU buffer when the consumer
// took the modifier format, else a memfd.
func (p *Producer) allocData(b *buffer) error {
	if p.useDmabuf {
		d, err := p.dmabuf.AllocDmabuf()
		if err != nil {
			return err
		}
		b.dmabuf, b.fd = d, d.Fd()
		b.dataType, b.dataFlags = dataDmaBuf, dataFlagReadable
		b.maxsize = d.Stride() * p.cfg.Height
		b.chunk.offset, b.chunk.stride, b.chunk.size = d.Offset(), int32(d.Stride()), 0
		return nil
	}
	size := p.cfg.Stride * p.cfg.Height
	fd, mem, err := memfd(int(size))
	if err != nil {
		return err
	}
	b.fd, b.data, b.maxsize = fd, mem, size
	b.dataType, b.dataFlags = dataMemFd, dataFlagReadable|dataFlagMappable
	return nil
}

func (p *Producer) releaseData(b *buffer) {
	if b.dmabuf != nil {
		b.dmabuf.Release()
	} else {
		_ = unix.Munmap(b.data)
		_ = unix.Close(b.fd)
	}
	b.dmabuf, b.data, b.fd = nil, nil, -1
}

// process is one cycle of the stream: unless it is too early or the
// source's size is being renegotiated, fill the next free buffer and
// hand it to the links.
func (p *Producer) process(n *node) int32 {
	slot, ok := n.cycleSlot()
	if !ok {
		return statusNeedData
	}
	now := time.Now()
	if now.Sub(p.last) < p.minInterval || !p.sizeSettled(n) {
		n.idle(slot)
		return statusNeedData
	}
	b := n.dequeue()
	if b == nil {
		n.idle(slot)
		return statusNeedData
	}
	if !p.fill(b, now) {
		b.release()
		n.idle(slot)
		return statusNeedData
	}
	p.last = now
	n.queue(slot, b)
	return statusHaveData
}

// sizeSettled is whether frames of the stream's size may go out: a
// resized source has the new size offered, and frames wait until the
// consumer took it.
func (p *Producer) sizeSettled(n *node) bool {
	if p.resizable == nil {
		return true
	}
	if w, h, stride := p.resizable.Size(); w != p.cfg.Width || h != p.cfg.Height || stride != p.cfg.Stride {
		p.cfg.Width, p.cfg.Height, p.cfg.Stride = w, h, stride
		p.renegotiating = true
		n.markPortChanged()
	}
	return !p.renegotiating
}

// fill writes one frame into b and stamps its metas; false leaves b
// unsent (nothing new, or the capture failed).
func (p *Producer) fill(b *buffer, now time.Time) bool {
	if b.chunk == nil {
		return false
	}
	var damage []Rect
	var ok bool
	if b.dmabuf != nil {
		damage, ok = p.dmabuf.FillDmabuf(b.dmabuf)
		b.chunk.offset, b.chunk.stride = b.dmabuf.Offset(), int32(b.dmabuf.Stride())
		b.chunk.size = 0
		if ok {
			b.chunk.size = b.dmabuf.Stride() * p.cfg.Height
		}
	} else {
		if b.data == nil {
			return false
		}
		damage, ok = p.cfg.Source.Fill(b.data)
		b.chunk.offset, b.chunk.stride = 0, int32(p.cfg.Stride)
		b.chunk.size = 0
		if ok {
			b.chunk.size = min(p.cfg.Stride*p.cfg.Height, uint32(len(b.data)))
		}
	}
	if !ok {
		return false
	}
	p.stamp(b, now, damage)
	return true
}

// stamp writes the metas the consumer negotiated.
func (p *Producer) stamp(b *buffer, now time.Time, damage []Rect) {
	if b.header != nil {
		*b.header = cMetaHeader{pts: now.Sub(p.start).Nanoseconds(), seq: p.seq}
		p.seq++
	}
	if b.transform != nil {
		*b.transform = p.cfg.Transform
	}
	if b.damage != nil {
		p.damage = ClampDamage(p.damage, damage, int(p.cfg.Width), int(p.cfg.Height), maxDamageRegions)
		writeDamage(b.damage, p.damage)
	}
}

// ClampDamage clips rects to the frame into out; none, none left, or
// more than max collapse to the whole frame. It appends at most max
// rects to out[:0], so an out of that capacity never grows.
func ClampDamage(out, rects []Rect, width, height, max int) []Rect {
	whole := Rect{0, 0, width, height}
	out = out[:0]
	if len(rects) == 0 || max == 0 {
		return append(out, whole)
	}
	for _, r := range rects {
		if r.X < 0 || r.Y < 0 || r.X >= width || r.Y >= height {
			continue
		}
		w, h := min(r.Width, width-r.X), min(r.Height, height-r.Y)
		if w <= 0 || h <= 0 {
			continue
		}
		if len(out) == max {
			return append(out[:0], whole)
		}
		out = append(out, Rect{r.X, r.Y, w, h})
	}
	if len(out) == 0 {
		return append(out, whole)
	}
	return out
}

// writeDamage fills the meta's region slots and terminates the list
// with a zero-size region, leaving room for it.
func writeDamage(slots []cMetaRegion, rects []Rect) {
	if len(slots) == 0 {
		return
	}
	i := 0
	for ; i < len(rects) && i+1 < len(slots); i++ {
		r := rects[i]
		slots[i] = cMetaRegion{int32(r.X), int32(r.Y), uint32(r.Width), uint32(r.Height)}
	}
	slots[i] = cMetaRegion{}
}

// memfd is a size-byte sealed-size shared memory file, mapped.
func memfd(size int) (int, []byte, error) {
	fd, err := unix.MemfdCreate("lys-shell-screencast", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return -1, nil, fmt.Errorf("memfd: %w", err)
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		_ = unix.Close(fd)
		return -1, nil, fmt.Errorf("memfd: size %d: %w", size, err)
	}
	mem, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Close(fd)
		return -1, nil, fmt.Errorf("memfd: map %d bytes: %w", size, err)
	}
	return fd, mem, nil
}
