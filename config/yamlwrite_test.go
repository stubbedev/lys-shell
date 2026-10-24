package config

import (
	"strings"
	"testing"
)

func TestYAMLDocumentKeepsFieldOrderAndTypes(t *testing.T) {
	root := newTable()
	root.set("imports", []any{})
	root.set("name", "x")
	bar := newTable()
	bar.set("scale", float32(1))
	bar.set("padding", float32(0.35))
	bar.set("list", []any{"a", "b"})
	bar.set("hex", "#123456")
	bar.set("word", "true")
	bar.set("count", int64(3))
	root.set("bar", bar)
	modules := newTable()
	clock := newTable()
	clock.set("format", "%H")
	modules.set("clock", clock)
	root.set("modules", modules)
	layouts := []any{newTable(), newTable()}
	layouts[0].(*table).set("monitor", "*")
	layouts[1].(*table).set("monitor", "DP-1")
	root.set("layout", layouts)
	got, err := yamlDocument(root)
	if err != nil {
		t.Fatal(err)
	}
	want := `imports: []
name: x
bar:
  scale: 1.0
  padding: 0.35
  list:
    - a
    - b
  hex: '#123456'
  word: "true"
  count: 3
modules:
  clock:
    format: '%H'
layout:
  - monitor: '*'
  - monitor: DP-1
`
	if got != want {
		t.Errorf("document =\n%s\nwant\n%s", got, want)
	}
	back, err := ParseYAML(got)
	if err != nil {
		t.Fatalf("the document does not parse back: %v", err)
	}
	b := back["bar"].(map[string]any)
	if b["scale"] != 1.0 || b["word"] != "true" || b["hex"] != "#123456" || b["count"] != int64(3) {
		t.Errorf("round trip changed a value or its type: %#v", b)
	}
	if _, err := yamlDocument([]any{"x"}); err == nil {
		t.Error("an array is not a document")
	}
	if got, err := yamlDocument(newTable()); err != nil || got != "" {
		t.Errorf("an empty table = %q, %v; want an empty document", got, err)
	}
}

func TestYAMLFloatsAndInline(t *testing.T) {
	for v, want := range map[float64]string{1: "1.0", 0.5: "0.5", 0: "0.0", -2: "-2.0", 1e21: "1000000000000000000000.0"} {
		if got := yamlFloat(v, 64); got != want {
			t.Errorf("%v -> %s, want %s", v, got, want)
		}
	}
	if got := yamlFloat(float64(float32(0.35)), 32); got != "0.35" {
		t.Errorf("f32 0.35 -> %s", got)
	}
	inner := newTable()
	inner.set("a", int64(1))
	inner.set("b", []any{"x", "y"})
	if got := YAMLInline(inner); got != `{a: 1, b: [x, y]}` {
		t.Errorf("inline table = %s", got)
	}
	if got := YAMLInline(newTable()); got != "{}" {
		t.Errorf("empty inline table = %s", got)
	}
	if got := YAMLInline("bottom"); got != "bottom" {
		t.Errorf("inline string = %s", got)
	}
	if got := YAMLInline("5"); got != `"5"` {
		t.Errorf("a numeric string = %s, want it quoted so it stays a string", got)
	}
}

func TestDefaultYAMLStartsWithImportsAndCarriesEverySection(t *testing.T) {
	out, err := DefaultYAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "imports: []\ngeneral:\n") {
		t.Errorf("default begins %q", out[:min(len(out), 60)])
	}
	for _, key := range []string{"\nbar:\n", "\n  layout:\n", "\ndropdowns:\n", "\nmodules:\n", "\n  battery:\n", "\nosd:\n", "\nanimations:\n"} {
		if !strings.Contains(out, key) {
			t.Errorf("default lacks %q", key)
		}
	}
	if !strings.Contains(out, "\n  padding: 0.35\n") {
		t.Error("f32 defaults print in their own width in `config default`")
	}
	if strings.Contains(out, "extends") {
		t.Error("None fields are omitted from the document")
	}
	if _, err := ParseYAML(out); err != nil {
		t.Errorf("the default document does not parse back: %v", err)
	}
}

func TestParseCLIValue(t *testing.T) {
	for raw, want := range map[string]any{
		"true": true, "5": int64(5), "1.5": 1.5, `"q"`: "q", "'5'": "5",
		"plain words": "plain words", "%H:%M": "%H:%M",
		"Now: playing": "Now: playing", "": "", "~": "~", "null": "null", "[unclosed": "[unclosed",
	} {
		if got := ParseCLIValue(raw); got != want {
			t.Errorf("%q -> %#v, want %#v", raw, got, want)
		}
	}
	if list, ok := ParseCLIValue(`[a, "b"]`).([]any); !ok || len(list) != 2 || list[1] != "b" {
		t.Error("a flow sequence parses as an array")
	}
	if m, ok := ParseCLIValue(`{module: clock}`).(map[string]any); !ok || m["module"] != "clock" {
		t.Error("a flow mapping parses as a table")
	}
}

// ParseYAML reads back what YAMLDocument writes, in the tree shapes
// SetByPath takes, and refuses what is no YAML mapping.
func TestParseYAMLRoundTrips(t *testing.T) {
	doc := map[string]any{"custom": []any{
		map[string]any{"id": "cpu", "command": "echo 1", "interval-ms": int64(5000)},
		map[string]any{"id": "mem", "ratio": 0.5},
	}}
	text, err := YAMLDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "custom:\n  - ") {
		t.Errorf("document %q lacks the list", text)
	}
	got, err := ParseYAML(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	list, ok := got["custom"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("custom = %#v, want a two-item []any", got["custom"])
	}
	if m := list[0].(map[string]any); m["id"] != "cpu" || m["interval-ms"] != int64(5000) {
		t.Errorf("first module %#v", m)
	}
	for _, bad := range []string{"custom: [unterminated", "- a list\n", "a scalar\n"} {
		if _, err := ParseYAML(bad); err == nil {
			t.Errorf("%q parsed as a document", bad)
		}
	}
	svc := Load(t.TempDir(), DiscardDiagnostics)
	defer svc.Close()
	parsed, _ := ParseYAML("custom:\n  - id: x\n    command: \"true\"\n")
	if err := svc.SetByPath("modules.custom", parsed["custom"]); err != nil {
		t.Errorf("SetByPath refused the parsed modules: %v", err)
	}
	if len(svc.Config().Custom) != 1 || svc.Config().Custom[0].Id != "x" {
		t.Errorf("modules %+v", svc.Config().Custom)
	}
}
