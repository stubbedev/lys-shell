package portal

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/pipewire/pwtest"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/service/lyswm"
	"github.com/stubbedev/lys-shell/service/lyswm/lyswmtest"
)

// lysWMGeometry is the outputs' logical geometry as lys-wm reports it.
func lysWMGeometry() ([]outputGeometry, error) {
	outputs, err := lyswm.Outputs()
	geometry := make([]outputGeometry, 0, len(outputs))
	for _, o := range outputs {
		geometry = append(geometry, outputGeometry{name: o.Name, x: int32(o.Rect.X), y: int32(o.Rect.Y), width: int32(o.Rect.Width), height: int32(o.Rect.Height)})
	}
	return geometry, err
}

func lysWMOutput(t *testing.T, name string) (lyswm.Output, bool) {
	t.Helper()
	outputs, err := lyswm.Outputs()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(outputs, func(o lyswm.Output) bool { return o.Name == name })
	if i < 0 {
		return lyswm.Output{}, false
	}
	return outputs[i], true
}

// On a real lys-wm, a virtual source adds an output lys-wm lists, the
// stream is that output's size, and closing the session removes it.
func TestScreenCastVirtualOnLysWM(t *testing.T) {
	r := newRig(t, func(b *Backend) {
		b.screenCast = newScreenCast(b.conn, b.sessions, b.castReminder(), screenStreams{openDevice: openVulkanAllocator}.start, lysWMGeometry)
	})
	lyswmtest.Headless(t)
	pwtest.Start(t)
	serveDialogs(t, r, true)
	if v, err := r.obj.GetProperty(ScreenCastIface + ".AvailableSourceTypes"); err != nil || v.Value() != uint32(7) {
		t.Fatalf("on lys-wm AvailableSourceTypes = %v, %v", v, err)
	}
	r.castSession(t, Vardict{"types": dbus.MakeVariant(sourceVirtual)})
	code, res := r.castStart(t)
	if code != ResponseSuccess {
		t.Fatalf("Start = %d", code)
	}
	out, ok := lysWMOutput(t, "VIRT-1")
	if !ok || out.Mode != (lyswm.Mode{Width: 1920, Height: 1080, RefreshMHz: virtualRefreshMHz}) || out.Scale != virtualScale {
		t.Fatalf("lys-wm lists the virtual output as %+v (%v)", out, ok)
	}
	streams := castStreams(t, res)
	if len(streams) != 1 || streams[0].Props["source_type"].Value() != sourceVirtual || pair(streams[0].Props["size"]) != [2]int32{1920, 1080} {
		t.Fatalf("streams = %+v", streams)
	}
	s, live := r.backend.sessions.get(sessA)
	if !live {
		t.Fatal("the session is gone")
	}
	st, ok := r.backend.screenCast.stream(s, streams[0].Node)
	if !ok {
		t.Fatal("the session does not run its stream")
	}
	if w, h := st.stream.Size(); w != int32(out.Mode.Width) || h != int32(out.Mode.Height) {
		t.Fatalf("the stream is %dx%d, its output %dx%d", w, h, out.Mode.Width, out.Mode.Height)
	}
	if err := r.client.Object(BusName, sessA).Call(SessionIface+".Close", 0).Err; err != nil {
		t.Fatal(err)
	}
	if _, ok := lysWMOutput(t, "VIRT-1"); ok {
		t.Fatal("the virtual output outlived its session")
	}
}
