package brightness

import (
	"slices"
	"testing"

	"github.com/stubbedev/lys-lib/logind"
	"github.com/stubbedev/lys-lib/logind/logindtest"
)

func TestLogindBacklightResolvesTheDisplaySessionOnce(t *testing.T) {
	fake := logindtest.Start(t, logindtest.Config{Display: "2"})
	b := &logindBacklight{manager: logind.NewManager(fake.Bus.Conn(t))}
	for _, value := range []uint32{300, 600} {
		if err := b.set(t.Context(), "intel_backlight", value); err != nil {
			t.Fatalf("set %d: %v", value, err)
		}
	}
	if got := fake.CallsTo("Manager.GetSession"); len(got) != 1 || !slices.Equal(got[0].Args, []any{"auto"}) {
		t.Errorf("GetSession calls %v, want one for auto", got)
	}
	calls := fake.CallsTo("Session.SetBrightness")
	if len(calls) != 2 {
		t.Fatalf("SetBrightness calls %v, want two", calls)
	}
	for i, value := range []uint32{300, 600} {
		want := []any{"backlight", "intel_backlight", value}
		if calls[i].Path != logind.SessionPath("2") || !slices.Equal(calls[i].Args, want) {
			t.Errorf("SetBrightness call %d: %v, want %v on %s", i, calls[i], want, logind.SessionPath("2"))
		}
	}
}

func TestLogindBacklightWithoutASessionFails(t *testing.T) {
	fake := logindtest.Start(t, logindtest.Config{})
	b := &logindBacklight{manager: logind.NewManager(fake.Bus.Conn(t))}
	if err := b.set(t.Context(), "intel_backlight", 300); err == nil {
		t.Fatal("set with no session succeeded")
	}
	if got := fake.CallsTo("Session.SetBrightness"); len(got) != 0 {
		t.Errorf("SetBrightness calls %v with no session", got)
	}
}
