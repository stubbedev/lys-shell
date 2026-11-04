package search

import (
	"bytes"
	"context"
	"errors"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stubbedev/lys-shell/settingspage"
)

func settingsEntries(t *testing.T, pages func(context.Context) ([]settingspage.Page, error)) []Entry {
	t.Helper()
	var got []Entry
	NewSettings(Env{}, pages).Load(context.Background(), func(e []Entry) { got = e })
	return got
}

func TestSettingsOffersThePagesAndOpensTheOneChosen(t *testing.T) {
	var spawned [][]string
	env := Env{Spawn: func(argv []string) { spawned = append(spawned, argv) }}
	p := NewSettings(env, func(context.Context) ([]settingspage.Page, error) {
		return []settingspage.Page{{ID: "wm-input", Title: "Input", Icon: "ld-keyboard-symbolic"}}, nil
	})
	var got []Entry
	p.Load(context.Background(), func(e []Entry) { got = e })
	if len(got) != 1 || got[0].Title != "Input" || got[0].ID != "settings:wm-input" {
		t.Fatalf("%+v", got)
	}
	actions := p.Actions(got[0])
	if len(actions) != 1 {
		t.Fatalf("%d actions", len(actions))
	}
	actions[0].Run(context.Background())
	if len(spawned) != 1 || !slices.Equal(spawned[0], []string{"lys-settings", "--page", "wm-input"}) {
		t.Fatalf("%q", spawned)
	}
}

func TestSettingsWithoutTheAppOffersNothingAndSaysSoOnce(t *testing.T) {
	var logged bytes.Buffer
	before := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(before) })
	notInstalled = sync.Once{}
	for range 3 {
		got := settingsEntries(t, func(context.Context) ([]settingspage.Page, error) {
			return nil, settingspage.ErrNotInstalled
		})
		if got != nil {
			t.Fatalf("%+v", got)
		}
	}
	if n := strings.Count(logged.String(), "not installed"); n != 1 {
		t.Fatalf("said %d times: %s", n, logged.String())
	}
}

func TestSettingsReportsAnAppThatFailsEveryTime(t *testing.T) {
	var logged bytes.Buffer
	before := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(before) })
	for range 2 {
		got := settingsEntries(t, func(context.Context) ([]settingspage.Page, error) {
			return nil, errors.New("the app broke")
		})
		if got != nil {
			t.Fatalf("%+v", got)
		}
	}
	if n := strings.Count(logged.String(), "the app broke"); n != 2 {
		t.Fatalf("said %d times: %s", n, logged.String())
	}
}
