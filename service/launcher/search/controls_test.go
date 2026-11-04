package search

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
)

func controlAction(t *testing.T, ctl Control) Action {
	t.Helper()
	actions := (&Controls{}).Actions(Entry{Data: ctl})
	if len(actions) != 1 {
		t.Fatalf("actions = %d", len(actions))
	}
	return actions[0]
}

func TestAControlThatRunsNowClosesTheSession(t *testing.T) {
	ran := false
	var deadline bool
	o := controlAction(t, Control{ID: "x", Title: "X", Effect: RunNow(func(ctx context.Context) error {
		ran = true
		_, deadline = ctx.Deadline()
		return nil
	})}).Run(context.Background())
	if _, ok := o.(Close); !ok || !ran {
		t.Errorf("outcome %T, ran %v", o, ran)
	}
	if !deadline {
		t.Error("a service call must be bounded")
	}
}

func TestAControlThatFailsSaysSoAndStays(t *testing.T) {
	o := controlAction(t, Control{ID: "x", Title: "Turn Off Wi-Fi", Effect: RunNow(func(context.Context) error {
		return errors.New("no permission")
	})}).Run(context.Background())
	stay, ok := o.(Stay)
	if !ok || !strings.Contains(stay.Message, "Turn Off Wi-Fi") || !strings.Contains(stay.Message, "no permission") {
		t.Errorf("outcome %+v", o)
	}
}

func TestAControlForAfterTheCloseDoesNotRunBeforeIt(t *testing.T) {
	ran := false
	o := controlAction(t, Control{ID: "x", Effect: RunOnceClosed(func() { ran = true })}).Run(context.Background())
	then, ok := o.(CloseThen)
	if !ok {
		t.Fatalf("outcome %T", o)
	}
	if ran {
		t.Error("it ran with the panel still up")
	}
	then.Run()
	if !ran {
		t.Error("the outcome does not carry the effect")
	}
}

func controlRoot(t *testing.T, d Deps, providers ...config.LauncherSearchProvider) (*Engine, *sink) {
	t.Helper()
	cfg := config.DefaultsLauncher().Search
	cfg.Providers = providers
	s := &sink{}
	e := Start(NewRoot(cfg, config.DefaultsPower(), d, t.TempDir()), nil, s, func(fn func()) { fn() })
	t.Cleanup(func() {
		e.Stop()
		select {
		case <-e.Done():
		case <-time.After(liveness):
			t.Error("the engine did not stop")
		}
	})
	return e, s
}

func someControls(titles ...string) func(context.Context) []Control {
	return func(context.Context) []Control {
		out := make([]Control, len(titles))
		for i, title := range titles {
			out[i] = Control{ID: title, Title: title, Effect: RunNow(func(context.Context) error { return nil })}
		}
		return out
	}
}

func TestControlGroupsFollowTheConfig(t *testing.T) {
	d := Deps{Toggles: someControls("Toggle Thing"), Media: someControls("Media Thing"), Capture: someControls("Capture Thing")}
	e, s := controlRoot(t, d, config.SearchToggles, config.SearchCapture)
	e.SetQuery("thing", 1)
	until(t, s, "#Commands", "Toggle Thing", "Capture Thing")
}

func TestAGroupWithoutItsServiceOffersNothing(t *testing.T) {
	cfg := config.DefaultsLauncher().Search
	cfg.Providers = []config.LauncherSearchProvider{config.SearchToggles, config.SearchMedia, config.SearchCapture}
	if root := NewRoot(cfg, config.DefaultsPower(), Deps{}, t.TempDir()); len(root.Providers) != 0 {
		t.Errorf("providers = %+v", root.Providers)
	}
}

func TestControlsWaitForASecondCharacter(t *testing.T) {
	e, s := controlRoot(t, Deps{Toggles: someControls("Turn On Wi-Fi")}, config.SearchToggles)
	e.SetQuery("wi", 1)
	until(t, s, "#Commands", "Turn On Wi-Fi")
	e.SetQuery("w", 2)
	until(t, s)
}

func TestTheCommandsSectionKeepsItsCapAcrossProviders(t *testing.T) {
	d := Deps{Toggles: someControls("Thing A", "Thing B", "Thing C", "Thing D"), Capture: someControls("Thing E", "Thing F", "Thing G", "Thing H")}
	e, s := controlRoot(t, d, config.SearchToggles, config.SearchCapture)
	e.SetQuery("thing", 1)
	until(t, s, "#Commands", "Thing A", "Thing B", "Thing C", "Thing D", "Thing E")
}
