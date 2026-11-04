package settingspage

import (
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
