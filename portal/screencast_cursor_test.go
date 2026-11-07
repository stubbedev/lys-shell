package portal

import (
	"errors"
	"image/color"
	"testing"
	"time"

	"github.com/stubbedev/lys-lib/capture"
	"github.com/stubbedev/lys-lib/pipewire/pwtest"
	"github.com/stubbedev/lys-lib/vinput"
	"github.com/stubbedev/lys-lib/wltest"

	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// cursorRig is a headless lys-wm with a still output under a pointer
// the test moves and whose image it sets.
type cursorRig struct {
	t       *testing.T
	painter *wltest.Painter
	pointer *vinput.Device
	target  sharepicker.Target
	w, h    uint32
}

func newCursorRig(t *testing.T) *cursorRig {
	t.Helper()
	lyswmtest.Headless(t)
	pwtest.Start(t)
	p := wltest.NewPainter(t)
	p.Layer(wltest.AnchorTop|wltest.AnchorBottom|wltest.AnchorLeft|wltest.AnchorRight, 0, 0, color.RGBA{R: 0x20, G: 0x40, B: 0x60, A: 0xff})
	p.Run()
	pointer, err := vinput.Dial("", vinput.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pointer.Close)
	c, err := capture.Connect()
	if err != nil {
		t.Fatal(err)
	}
	outs := c.Outputs()
	_ = c.Close()
	if len(outs) == 0 {
		t.Fatal("lys-wm lists no output")
	}
	o := outs[0]
	r := &cursorRig{
		t: t, painter: p, pointer: pointer, target: sharepicker.Target{Kind: sharepicker.TargetOutput, Name: o.Name},
		w: uint32(o.Width), h: uint32(o.Height),
	}
	r.move(r.w/2, r.h/2)
	p.Cursor(16, color.RGBA{R: 0xff, A: 0xff})
	return r
}

func (r *cursorRig) move(x, y uint32) {
	r.t.Helper()
	if err := errors.Join(r.pointer.MotionAbsolute(x, y, r.w, r.h), r.pointer.Roundtrip()); err != nil {
		r.t.Fatal(err)
	}
}

// at is where move i of a run puts the pointer: a new place each time.
func at(i int) (x, y uint32) {
	return uint32(20 + i*3), uint32(30 + i*2)
}

// castView is a stream of the rig's output as its consumer sees it: each
// buffer the producer queued, once, and the frames the capture took.
type castView struct {
	t        *testing.T
	consumer *pwtest.Consumer
	seq      uint64
	seen     bool
	ext      *extSource
	frames   uint64
}

// stream starts the output's stream in mode with its consumer linked;
// the output streams from the shared-memory ext-image-copy session, as
// lys-wm's software renderer offers no dmabuf.
func (r *cursorRig) stream(mode cursorMode) *castView {
	r.t.Helper()
	s := r.start(r.target, mode)
	ext, ok := s.(*pipewireStream).source.(*extSource)
	if !ok {
		r.t.Fatalf("lys-wm's output streams from %T, not the ext-image-copy session", s.(*pipewireStream).source)
	}
	return &castView{t: r.t, consumer: pwtest.Consume(r.t, screencastNode), ext: ext}
}

// take is the next buffer the consumer dequeued, and whether the producer
// queued it anew rather than the consumer dequeuing it again.
func (v *castView) take() (pwtest.Buffer, bool) {
	v.t.Helper()
	b := v.consumer.Buffer(v.t)
	if v.seen && b.Seq == v.seq {
		return b, false
	}
	v.seq, v.seen = b.Seq, true
	return b, true
}

// start streams target in mode, closed with the test.
func (r *cursorRig) start(target sharepicker.Target, mode cursorMode) screenStream {
	r.t.Helper()
	s, err := screenStreams{openDevice: openVulkanAllocator}.start(target, mode, defaultFPS)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(s.Close)
	return s
}

// next is the next buffer the producer queued.
func (v *castView) next() pwtest.Buffer {
	v.t.Helper()
	for range 2000 {
		if b, fresh := v.take(); fresh {
			return b
		}
	}
	v.t.Fatal("two thousand buffers dequeued again and none queued anew")
	return pwtest.Buffer{}
}

// until reads buffers up to the first ok takes, passing each to seen;
// past a thousand it fails.
func (v *castView) until(what string, ok func(pwtest.Buffer) bool, seen func(pwtest.Buffer)) pwtest.Buffer {
	v.t.Helper()
	for range 200 {
		b := v.next()
		seen(b)
		if ok(b) {
			return b
		}
	}
	v.t.Fatalf("two hundred buffers and none with %s", what)
	return pwtest.Buffer{}
}

// settle waits until lys-wm stops changing the output (a surface still
// animating open): until two frames come with none captured between
// them, the second the first sent again, as a still output's stream does
// once stillRefresh passed. It passes each buffer to seen.
func (v *castView) settle(seen func(pwtest.Buffer)) {
	v.t.Helper()
	frames := 0
	for range 1000 {
		b := v.next()
		seen(b)
		if b.Size == 0 {
			continue
		}
		if frames++; v.captured() == 0 && frames > 1 {
			return
		}
	}
	v.t.Fatal("lys-wm kept changing the output through a thousand buffers")
}

// captured is how many frames the capture took since the last call.
func (v *castView) captured() uint64 {
	v.t.Helper()
	var f capture.StreamFrame
	if !v.ext.stream.Latest(&f) {
		v.t.Fatal("the stream published no frame")
	}
	n := f.Seq + 1 - v.frames
	v.frames = f.Seq + 1
	return n
}

func bitmapOf(side int32) func(pwtest.Buffer) bool {
	return func(b pwtest.Buffer) bool {
		return b.Cursor != nil && b.Cursor.Bitmap != nil && b.Cursor.Bitmap.Width == uint32(side)
	}
}

// pointerAt accepts a cursor meta placing the pointer where the test moved
// it, within the pixel lys-wm may round an absolute motion to.
func pointerAt(x, y uint32) func(pwtest.Buffer) bool {
	near := func(got int32, want uint32) bool { return abs(int64(got)-int64(want)) <= 1 }
	return func(b pwtest.Buffer) bool {
		return b.Cursor != nil && b.Cursor.ID != 0 && near(b.Cursor.X, x) && near(b.Cursor.Y, y)
	}
}

func abs(v int64) int64 { return max(v, -v) }

// The metadata cursor mode on a real lys-wm: 100 moves of the pointer
// over a still output make 100 cursor meta updates, each in a buffer
// without a frame, and capture no frame; a bitmap goes out once per new
// image and never for a move.
func TestTheMetadataCursorModeSendsMovesWithoutCapturingFrames(t *testing.T) {
	r := newCursorRig(t)
	v := r.stream(cursorMetadata)
	updates, bitmaps := 0, 0
	var lastX, lastY int32 = -1, -1
	tally := func(b pwtest.Buffer) {
		if b.Cursor == nil {
			t.Fatalf("a buffer without the cursor meta: %+v", b)
		}
		if b.Cursor.ID != 0 && (b.Cursor.X != lastX || b.Cursor.Y != lastY) {
			updates++
			lastX, lastY = b.Cursor.X, b.Cursor.Y
		}
		if bm := b.Cursor.Bitmap; bm != nil && bm.Width > 0 {
			bitmaps++
		}
	}
	v.until("the first frame", func(b pwtest.Buffer) bool { return b.Size > 0 }, tally)
	if bitmaps != 1 {
		v.until("the 16x16 image", bitmapOf(16), tally)
	}
	v.settle(tally)
	updates, bitmaps, metaOnly := 0, 0, 0
	const moves = 100
	for i := range moves {
		x, y := at(i)
		r.move(x, y)
		b := v.until("the pointer moved", pointerAt(x, y), tally)
		if b.Size == 0 && !b.Corrupted {
			t.Fatalf("move %d came in an empty buffer not marked corrupted", i)
		}
		if b.Size == 0 {
			metaOnly++
		}
	}
	if updates != moves || bitmaps != 0 {
		t.Fatalf("%d moves made %d cursor meta updates and %d bitmaps", moves, updates, bitmaps)
	}
	if n := v.captured(); n != 0 {
		t.Fatalf("%d moves over a still output captured %d frames", moves, n)
	}
	if metaOnly == 0 {
		t.Fatalf("none of %d moves came in a buffer without a frame", moves)
	}
	sides := []int32{24, 32, 20}
	for _, side := range sides {
		r.painter.Cursor(side, color.RGBA{G: 0xff, A: 0xff})
		b := v.until("the new image", bitmapOf(side), tally)
		if bm := b.Cursor.Bitmap; bm.Height != uint32(side) || bm.Stride != side*4 || b.Cursor.HotspotX != side/2 {
			t.Fatalf("the %dx%d image went out as %+v, hotspot %d,%d", side, side, *bm, b.Cursor.HotspotX, b.Cursor.HotspotY)
		}
	}
	for i := range 5 {
		x, y := at(moves + i)
		r.move(x, y)
		v.until("the pointer moved", pointerAt(x, y), tally)
	}
	if bitmaps != len(sides) {
		t.Fatalf("%d new images wrote %d bitmaps", len(sides), bitmaps)
	}
}

// The embedded mode paints the pointer: each move is one captured frame,
// and no buffer carries a cursor meta.
func TestTheEmbeddedCursorModeCapturesAFramePerMove(t *testing.T) {
	r := newCursorRig(t)
	v := r.stream(cursorEmbedded)
	noCursor := func(b pwtest.Buffer) {
		if b.Cursor != nil {
			t.Fatalf("a buffer carries a cursor meta: %+v", *b.Cursor)
		}
	}
	v.until("the first frame", func(b pwtest.Buffer) bool { return b.Size > 0 }, noCursor)
	v.settle(noCursor)
	const moves = 100
	frames := uint64(0)
	for i := range moves {
		x, y := at(i)
		r.move(x, y)
		v.until("a frame of the moved pointer", func(b pwtest.Buffer) bool { return b.Size > 0 }, noCursor)
		deadline := time.Now().Add(5 * time.Second)
		for frames <= uint64(i) {
			if time.Now().After(deadline) {
				t.Fatalf("move %d captured no frame", i)
			}
			frames += v.captured()
		}
	}
	if frames != moves {
		t.Fatalf("%d moves captured %d frames", moves, frames)
	}
}

// The hidden mode leaves the pointer out: no buffer carries a cursor
// meta, and a moving pointer or a new image captures nothing.
func TestTheHiddenCursorModeCarriesNoCursorMeta(t *testing.T) {
	r := newCursorRig(t)
	v := r.stream(cursorHidden)
	noCursor := func(b pwtest.Buffer) {
		if b.Cursor != nil {
			t.Fatalf("a buffer carries a cursor meta: %+v", *b.Cursor)
		}
	}
	v.until("the first frame", func(b pwtest.Buffer) bool { return b.Size > 0 }, noCursor)
	v.settle(noCursor)
	for i := range 20 {
		r.move(at(i))
	}
	r.painter.Cursor(24, color.RGBA{B: 0xff, A: 0xff})
	v.until("the still frame sent again", func(b pwtest.Buffer) bool { return b.Size > 0 }, noCursor)
	if n := v.captured(); n != 0 {
		t.Fatalf("a pointer over a still output captured %d frames without a cursor", n)
	}
}

// A region's metadata cursor is placed from the region's corner, and a
// pointer off the region leaves it: an empty bitmap, then the image again
// once the pointer is back.
func TestTheMetadataCursorModePlacesThePointerInARegion(t *testing.T) {
	r := newCursorRig(t)
	region := sharepicker.Target{Kind: sharepicker.TargetRegion, Name: r.target.Name, X: 100, Y: 50, Width: 400, Height: 300}
	r.start(region, cursorMetadata)
	v := &castView{t: t, consumer: pwtest.Consume(t, screencastNode)}
	none := func(pwtest.Buffer) {}
	r.move(300, 200)
	v.until("the pointer in the region", pointerAt(200, 150), none)
	r.move(20, 20)
	v.until("the pointer off the region", func(b pwtest.Buffer) bool {
		return b.Cursor != nil && b.Cursor.ID != 0 && b.Cursor.Bitmap != nil && b.Cursor.Bitmap.Width == 0
	}, none)
	r.move(150, 80)
	b := v.until("the pointer back in the region", pointerAt(50, 30), none)
	if b.Cursor.Bitmap == nil || b.Cursor.Bitmap.Width != 16 {
		t.Fatalf("the pointer came back into the region without its image: %+v", *b.Cursor)
	}
}
