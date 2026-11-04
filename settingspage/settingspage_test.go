package settingspage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAListSurvivesTheRoundTrip(t *testing.T) {
	want := []Page{{ID: "bar-general", Title: "Bar", Icon: "ld-panel-top-symbolic"}, {ID: "wm-input", Title: "Saisie"}}
	data, err := Encode(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%+v, want %+v", got, want)
	}
}

func TestAnEmptyListIsNotAMissingOne(t *testing.T) {
	if got, err := Decode([]byte("[]\n")); err != nil || len(got) != 0 {
		t.Fatalf("[]: %+v, %v", got, err)
	}
	if _, err := Decode([]byte("null\n")); err == nil {
		t.Fatal("null is not a list")
	}
}

func TestDecodeRefusesWhatCouldNotOpen(t *testing.T) {
	for name, data := range map[string]string{
		"not json":        "lys-settings 0.1.0\n",
		"empty output":    "",
		"no id":           `[{"id":"","title":"Bar","icon":""}]`,
		"no title":        `[{"id":"bar","title":"","icon":""}]`,
		"twice":           `[{"id":"bar","title":"A","icon":""},{"id":"bar","title":"B","icon":""}]`,
		"unknown field":   `[{"id":"bar","title":"A","icon":"","group":"x"}]`,
		"object not list": `{"id":"bar"}`,
	} {
		if got, err := Decode([]byte(data)); err == nil {
			t.Errorf("%s: decoded %+v", name, got)
		}
	}
}

func TestEncodeRefusesAPageThatCouldNotOpen(t *testing.T) {
	if _, err := Encode([]Page{{Title: "no id"}}); err == nil || !strings.Contains(err.Error(), "no ID") {
		t.Fatalf("%v", err)
	}
}

func TestArgvOpensThePage(t *testing.T) {
	if got := (Page{ID: "wm-input"}).Argv(); !slices.Equal(got, []string{"lys-settings", "--page", "wm-input"}) {
		t.Fatalf("%q", got)
	}
}

func fakeApp(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Program), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestListReadsWhatTheAppPrints(t *testing.T) {
	fakeApp(t, `[ "$1" = --list-pages ] || exit 9; echo '[{"id":"wm-input","title":"Input","icon":"i"}]'`)
	got, err := List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []Page{{ID: "wm-input", Title: "Input", Icon: "i"}}) {
		t.Fatalf("%+v", got)
	}
}

func TestListSaysTheAppIsNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := List(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("%v", err)
	}
}

func TestListReportsAnAppThatFailsOrPrintsNonsense(t *testing.T) {
	fakeApp(t, `echo boom >&2; exit 1`)
	if _, err := List(context.Background()); err == nil || errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failing app: %v", err)
	}
	fakeApp(t, `echo lys-settings 0.1.0`)
	if _, err := List(context.Background()); err == nil {
		t.Fatal("nonsense was accepted")
	}
}

func TestListStopsWithItsContext(t *testing.T) {
	fakeApp(t, `exec sleep 300`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := List(ctx); err == nil {
		t.Fatal("a cancelled list succeeded")
	}
}

func TestALongLivedListerStartsTheAppOnce(t *testing.T) {
	runs := filepath.Join(t.TempDir(), "runs")
	fakeApp(t, `echo run >> "`+runs+`"; echo '[{"id":"a","title":"A","icon":""}]'`)
	list := NewLister()
	for range 5 {
		got, err := list(context.Background())
		if err != nil || len(got) != 1 {
			t.Fatalf("%+v, %v", got, err)
		}
	}
	data, err := os.ReadFile(runs)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "run"); n != 1 {
		t.Fatalf("started %d times", n)
	}
}

func TestAListerAsksAgainAfterAFailure(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	list := NewLister()
	if _, err := list(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("%v", err)
	}
	fakeApp(t, `echo '[{"id":"a","title":"A","icon":""}]'`)
	if got, err := list(context.Background()); err != nil || len(got) != 1 {
		t.Fatalf("installed later: %+v, %v", got, err)
	}
}
