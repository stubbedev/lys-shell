package portal

import (
	"testing"

	"github.com/stubbedev/lys-desktop/service/lyswm"
	"github.com/stubbedev/lys-desktop/service/lyswm/lyswmtest"
	"github.com/stubbedev/lys-lib/pipewire/pwtest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/shell/sharepicker"
)

// framesUntil is how many buffers a consumer reads waiting for a frame
// of a new size before the switch counts as not followed.
const framesUntil = 300

// On a real lys-wm, the picker offers the cast target, and its one
// PipeWire stream follows casttarget from one output to another of
// another size and back, without ending or being replaced.
func TestScreenCastDynamicOnLysWM(t *testing.T) {
	r := newRig(t, func(b *Backend) {
		b.screenCast = newScreenCast(b.conn, b.sessions, b.castReminder(), screenStreams{openDevice: openVulkanAllocator}.start, lysWMGeometry)
	})
	lyswmtest.Headless(t)
	pwtest.Start(t)
	outputs, err := lyswm.Outputs()
	if err != nil || len(outputs) != 1 {
		t.Fatalf("lys-wm's outputs %+v, %v", outputs, err)
	}
	first := outputs[0]
	second, err := createLysWMOutput(virtualSize{640, 360})
	if err != nil {
		t.Fatal(err)
	}
	if err := lyswm.Dispatch("casttarget output " + first.Name); err != nil {
		t.Fatal(err)
	}
	p := serveSharePicker(t, r, "/dynamic")
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceMonitor | sourceWindow)})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	if asked := p.asked.Load(); len(asked) != 1 || asked[0].(sharepicker.Request).Sources&sharepicker.SourceDynamic == 0 {
		t.Fatalf("picker asked %v", asked)
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || streams[0].Props["source_type"].Value() != sourceMonitor {
		t.Fatalf("streams = %+v", streams)
	}
	node := streams[0].Node
	s, live := r.backend.sessions.get(sessA)
	if !live {
		t.Fatal("the session is gone")
	}
	st, ok := r.backend.screenCast.stream(s, node)
	if !ok {
		t.Fatal("the session does not run its stream")
	}
	consumer := pwtest.Consume(t, screencastNode)
	follows := func(target string, width, height int) {
		t.Helper()
		if err := lyswm.Dispatch("casttarget output " + target); err != nil {
			t.Fatal(err)
		}
		want := uint32(width * height * 4)
		var got uint32
		for range framesUntil {
			if got = consumer.Frame(t); got == want {
				break
			}
		}
		if got != want {
			t.Fatalf("after casttarget %s the frames are %d bytes, want %dx%d's %d", target, got, width, height, want)
		}
		select {
		case <-st.stream.Ended():
			t.Fatalf("the stream ended when the target moved to %s", target)
		default:
		}
		if now, ok := r.backend.screenCast.stream(s, node); !ok || now.stream != st.stream {
			t.Fatalf("the stream on node %d was replaced when the target moved to %s", node, target)
		}
	}
	follows(first.Name, first.Mode.Width, first.Mode.Height)
	follows(second, 640, 360)
	follows(first.Name, first.Mode.Width, first.Mode.Height)
}
