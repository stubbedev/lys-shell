package bar

import (
	"errors"
	"slices"
	"testing"

	"github.com/stubbedev/lys-ui/app"

	"github.com/stubbedev/lys-shell/service/shellipc"
	"github.com/stubbedev/lys-shell/shell/launcher"
)

type fakeShortcuts struct {
	registered []string
	presses    map[string]func(pressed bool, unixSec uint64)
	refuse     string
}

func (f *fakeShortcuts) register(id, appID, _, trigger string, on func(bool, uint64), done func(*app.GlobalShortcut, error)) {
	f.registered = append(f.registered, appID+":"+id+"@"+trigger)
	if id == f.refuse {
		done(nil, errors.New("refused"))
		return
	}
	f.presses[id] = on
	done(&app.GlobalShortcut{}, nil)
}

func TestAPressedShortcutOpensItsPanelInProcess(t *testing.T) {
	f := &fakeShortcuts{presses: map[string]func(bool, uint64){}}
	opened, toggled := 0, 0
	stop := registerShellShortcuts(f.register, shellShortcuts(func() { opened++ }, func() { toggled++ }))
	defer stop()
	want := []string{shellipc.AppID + ":launcher@SUPER+Space", shellipc.AppID + ":dashboard@SUPER+A"}
	if !slices.Equal(f.registered, want) {
		t.Fatalf("registered %v, want %v", f.registered, want)
	}
	f.presses["launcher"](true, 1)
	if opened != 1 || toggled != 0 {
		t.Errorf("a launcher press opened %d launchers and %d dashboards, want 1 and 0", opened, toggled)
	}
	f.presses["launcher"](false, 1)
	if opened != 1 {
		t.Errorf("a release opened the launcher again (%d)", opened)
	}
	f.presses["dashboard"](true, 2)
	if toggled != 1 {
		t.Errorf("a dashboard press toggled %d times, want 1", toggled)
	}
}

func TestARefusedShortcutLeavesTheOthersBound(t *testing.T) {
	f := &fakeShortcuts{presses: map[string]func(bool, uint64){}, refuse: "launcher"}
	toggled := 0
	stop := registerShellShortcuts(f.register, shellShortcuts(func() { t.Error("a refused shortcut fired") }, func() { toggled++ }))
	defer stop()
	if _, ok := f.presses["launcher"]; ok {
		t.Error("the refused shortcut is live")
	}
	f.presses["dashboard"](true, 1)
	if toggled != 1 {
		t.Errorf("the dashboard toggled %d times, want 1", toggled)
	}
}

func TestTheShortcutSessionIsTheSearchPanelApartFromCLISessions(t *testing.T) {
	a, b := searchSession(), searchSession()
	if a.Options.Mode == nil || *a.Options.Mode != launcher.SearchMode || a.Options.Dmenu || a.Replace {
		t.Errorf("session options = %+v, want the search panel as a bare lys-shell launcher asks", a.Options)
	}
	if a.ID>>63 != 1 || b.ID == a.ID {
		t.Errorf("session ids %d %d, want distinct ids outside the socket's counter", a.ID, b.ID)
	}
}
