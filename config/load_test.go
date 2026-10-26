package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFileMissingCreatesTheStub(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lys-shell", "config.yaml")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if cfg.Bar.Location != LocationTop || !cfg.Bar.Exclusive {
		t.Errorf("missing file: want defaults, got %+v", cfg.Bar)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != defaultConfigStub {
		t.Errorf("stub = %q, %v", data, err)
	}
	if !strings.HasPrefix(string(data), "# yaml-language-server: $schema=./schema.json\n") {
		t.Errorf("stub %q lacks the schema modeline", data)
	}
	if again, err := LoadFile(path); err != nil || again.Bar.Location != LocationTop {
		t.Errorf("the stub does not load back: %v", err)
	}
}

// A config.toml from before the YAML switch is an error naming the file
// to convert, and no empty config.yaml is written over the user's
// settings; with a config.yaml present it is ignored.
func TestLegacyTOMLConfigIsAnErrorNotAStub(t *testing.T) {
	dir := t.TempDir()
	legacy := writeFile(t, dir, "config.toml", "[bar]\nlocation = \"bottom\"\n")
	path := filepath.Join(dir, "config.yaml")
	cfg, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), legacy+" is TOML, which lys-shell no longer reads: convert it to "+path) {
		t.Fatalf("legacy config.toml: %v", err)
	}
	if cfg.Bar.Location != LocationTop {
		t.Errorf("location = %q, want the defaults while the file is unconverted", cfg.Bar.Location)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a stub config.yaml was written beside the TOML file: %v", err)
	}
	writeFile(t, dir, "config.yaml", "bar:\n  location: left\n")
	if cfg, err := LoadFile(path); err != nil || cfg.Bar.Location != LocationLeft {
		t.Errorf("with config.yaml present: %q, %v", cfg.Bar.Location, err)
	}
}

// Every config file is YAML whatever its name: TOML syntax does not
// parse.
func TestTOMLSyntaxDoesNotParse(t *testing.T) {
	_, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "[bar]\nlocation = \"bottom\"\n"))
	if err == nil {
		t.Fatal("a TOML document loaded as YAML")
	}
}

func TestYAMLLoadsSectionsAndModules(t *testing.T) {
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "bar:\n  location: bottom\n  exclusive: false\nmodules:\n  clock:\n    format: \"%H\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bar.Location != LocationBottom || cfg.Bar.Exclusive || cfg.Clock.Format != "%H" {
		t.Errorf("loaded %+v, clock %q", cfg.Bar, cfg.Clock.Format)
	}
}

// A document of nothing but comments is an empty config; a null value,
// a duplicate key, or a syntax error fails the file.
func TestYAMLNullsDuplicatesAndSyntaxErrorsFailTheFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadFile(writeFile(t, dir, "comments.yaml", "# nothing yet\n")); err != nil {
		t.Errorf("a comment-only file: %v", err)
	}
	for name, content := range map[string]string{
		"null.yaml":      "bar:\n  location: ~\n",
		"duplicate.yaml": "bar:\n  location: left\nbar:\n  location: right\n",
		"broken.yaml":    "a: : :\n  - broken",
		"scalar.yaml":    "just a string\n",
	} {
		cfg, err := LoadFile(writeFile(t, dir, name, content))
		if err == nil {
			t.Errorf("%s: want a file error", name)
		}
		if cfg.Bar.Location != LocationTop {
			t.Errorf("%s: want the defaults back", name)
		}
	}
	_, err := LoadFile(writeFile(t, dir, "null-line.yaml", "bar:\n  location: ~\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2: a null is not a config value") {
		t.Errorf("null error = %v, want it to name the line", err)
	}
}

func TestBadValueKeepsItsDefaultAndTheRestApplies(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "bar:\n  location: sideways\n  exclusive: false\n")
	cfg, err := LoadFile(path)
	if err == nil {
		t.Fatal("invalid location: want a diagnostic error")
	}
	var d Diagnostic
	if !errors.As(err, &d) || d.Field("Field") != "bar.location" ||
		!strings.Contains(d.Field("Error"), "unknown variant `sideways`, expected one of `top`, `bottom`, `left`, `right`") {
		t.Errorf("diagnostic = %+v", err)
	}
	if cfg.Bar.Location != LocationTop {
		t.Errorf("location = %q, want the default", cfg.Bar.Location)
	}
	if cfg.Bar.Exclusive {
		t.Error("exclusive = true: the valid sibling must still apply")
	}
}

func TestTypeMismatchUsesSerdeWording(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "bar:\n  border-width: 300\n  scale: big\n")
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("want errors")
	}
	msg := err.Error()
	for _, want := range []string{"invalid value: integer `300`, expected u8", `invalid type: string "big", expected f32`} {
		if !strings.Contains(msg, want) {
			t.Errorf("errors %q lack %q", msg, want)
		}
	}
}

// A dropdown height is a pixel length: "400px" loads, while a
// multiplier, which has no base height to scale, or a negative length
// is an error naming the key and the form it takes, and the dropdown's
// size is left unset rather than read as something else.
func TestDropdownHeightTakesPixelsOnly(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "dropdowns:\n  weather:\n    height: 400px\n")
	cfg, err := LoadFile(path)
	if err != nil || cfg.Dropdowns.Weather.Height == nil || *cfg.Dropdowns.Weather.Height != 400 {
		t.Fatalf("height 400px: %v, %v", cfg.Dropdowns.Weather.Height, err)
	}
	for raw, wantErr := range map[string]string{
		"1.5":    "invalid type: floating point `1.5`, expected a pixel length like \"400px\"",
		"2":      "invalid type: integer `2`, expected a pixel length like \"400px\"",
		`"1.5"`:  "invalid value: string \"1.5\", expected a pixel length like \"400px\" up to 10000px",
		`"-4px"`: "invalid value: string \"-4px\", expected a pixel length like \"400px\" up to 10000px",
	} {
		path := writeFile(t, t.TempDir(), "config.yaml", "dropdowns:\n  weather:\n    width: 1.5\n    height: "+raw+"\n")
		cfg, err := LoadFile(path)
		var d Diagnostic
		if !errors.As(err, &d) || d.Field("Field") != "dropdowns.weather.height" || d.Field("Error") != wantErr {
			t.Errorf("height %s: error %v, want %q at dropdowns.weather.height", raw, err, wantErr)
		}
		if got := cfg.Dropdowns.Weather; got != (DropdownSize{}) {
			t.Errorf("height %s: loaded %+v, want the whole size rejected, not read as something else", raw, got)
		}
	}
}

func TestUnknownKeysAndNonTableSectionsAreIgnored(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "osd: 5\nbar:\n  not-in-the-schema: 3\nmodules:\n  clock:\n    also-unknown: 1\n")
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("unknown keys: %v", err)
	}
}

func TestDeprecatedAliasApplies(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "modules:\n  notification:\n    icon-name: legacy\n")
	cfg, err := LoadFile(path)
	if err != nil || cfg.Notification.IconName != "legacy" {
		t.Fatalf("modules.notification alias: %q, %v", cfg.Notification.IconName, err)
	}
	path = writeFile(t, t.TempDir(), "config.yaml", "modules:\n  notifications:\n    icon-name: plural\n")
	cfg, _ = LoadFile(path)
	if cfg.Notification.IconName != "plural" {
		t.Errorf("canonical key: %q", cfg.Notification.IconName)
	}
}

func TestImportsMergeInOrderAndTheFileWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.yml", "modules:\n  clock:\n    format: one\nbar:\n  location: left\n")
	writeFile(t, dir, "two.yaml", "modules:\n  clock:\n    format: two\n")
	main := writeFile(t, dir, "config.yaml", "imports: [one.yml, two]\nbar:\n  exclusive: false\n")
	cfg, err := LoadFile(main)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Clock.Format != "two" || cfg.Bar.Location != LocationLeft || cfg.Bar.Exclusive {
		t.Errorf("merged: format %q location %q exclusive %v", cfg.Clock.Format, cfg.Bar.Location, cfg.Bar.Exclusive)
	}
}

func TestImportCycleMissingAndNonYAMLImportsFail(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", "imports: [b.yaml]\n")
	writeFile(t, dir, "b.yaml", "imports: [a.yaml]\n")
	main := writeFile(t, dir, "config.yaml", "imports: [a.yaml]\n")
	_, err := LoadFile(main)
	if err == nil || !strings.Contains(err.Error(), "circular import detected: config.yaml -> a.yaml -> b.yaml -> a.yaml") {
		t.Errorf("cycle: %v", err)
	}
	missing := writeFile(t, dir, "m.yaml", "imports: [ghost.yaml]\n")
	_, err = LoadFile(missing)
	if err == nil || !strings.Contains(err.Error(), "cannot import '"+filepath.Join(dir, "ghost.yaml")+"'") {
		t.Errorf("missing import: %v", err)
	}
	writeFile(t, dir, "old.toml", "[bar]\nlocation = \"left\"\n")
	toml := writeFile(t, dir, "t.yaml", "imports: [old.toml]\n")
	_, err = LoadFile(toml)
	if err == nil || !strings.Contains(err.Error(), "cannot import '"+filepath.Join(dir, "old.toml")+"': imports must be YAML") {
		t.Errorf("a TOML import: %v", err)
	}
}

func TestResolveImportPrefersYAMLForBareNames(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "config.yaml")
	if got := resolveImport(base, "theme"); got != filepath.Join(dir, "theme.yaml") {
		t.Errorf("no sibling: %q, want the .yaml fallback", got)
	}
	writeFile(t, dir, "theme.toml", "a = 1\n")
	if got := resolveImport(base, "theme"); got != filepath.Join(dir, "theme.yaml") {
		t.Errorf("a toml sibling: %q, want the .yaml fallback", got)
	}
	writeFile(t, dir, "theme.yml", "a: 1\n")
	if got := resolveImport(base, "theme"); got != filepath.Join(dir, "theme.yml") {
		t.Errorf("yml sibling: %q", got)
	}
	writeFile(t, dir, "theme.yaml", "a: 1\n")
	if got := resolveImport(base, "theme"); got != filepath.Join(dir, "theme.yaml") {
		t.Errorf("yaml sibling: %q, want .yaml before .yml", got)
	}
	if got := resolveImport(base, "/abs/x.yaml"); got != "/abs/x.yaml" {
		t.Errorf("absolute import: %q", got)
	}
}

func TestDiscoverMainIsYAMLOnly(t *testing.T) {
	dir := t.TempDir()
	if got := DiscoverMain(dir); got != filepath.Join(dir, "config.yaml") {
		t.Errorf("empty dir: %q", got)
	}
	writeFile(t, dir, "config.toml", "")
	if got := DiscoverMain(dir); got != filepath.Join(dir, "config.yaml") {
		t.Errorf("a config.toml was discovered: %q", got)
	}
	writeFile(t, dir, "config.yml", "")
	if got := DiscoverMain(dir); got != filepath.Join(dir, "config.yml") {
		t.Errorf("yml: %q", got)
	}
	writeFile(t, dir, "config.yaml", "")
	if got := DiscoverMain(dir); got != filepath.Join(dir, "config.yaml") {
		t.Errorf("yaml first: %q", got)
	}
}

func TestDirFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, err := Dir(); err != nil || got != "/xdg/lys-shell" {
		t.Errorf("xdg: %q %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/u")
	if got, err := Dir(); err != nil || got != "/home/u/.config/lys-shell" {
		t.Errorf("home: %q %v", got, err)
	}
	t.Setenv("HOME", "")
	if _, err := Dir(); err == nil {
		t.Error("neither set: want an error")
	}
}

func TestClampedNewtypesWarnAndClamp(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml",
		"bar:\n  background-opacity: 150\n  scale: 9\nmodules:\n  cava:\n    bars: 0\n")
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("clamping is not an error: %v", err)
	}
	if cfg.Bar.BackgroundOpacity != 100 || cfg.Bar.Scale != 3 || cfg.Cava.Bars != 1 {
		t.Errorf("clamped: opacity %d scale %v bars %d", cfg.Bar.BackgroundOpacity, cfg.Bar.Scale, cfg.Cava.Bars)
	}
}

// A size is a multiplier or a pixel length: both load, and a string
// that is neither is an error naming the key, the field keeping its
// default instead of falling back to a 1.0 multiplier. Inside a value
// struct the error names the struct's field.
func TestSizeTakesAMultiplierOrPixels(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", "bar:\n  padding: 24px\n  module-gap: 1.5\n")
	cfg, err := LoadFile(path)
	if err != nil || cfg.Bar.Padding != Px(24) || cfg.Bar.ModuleGap != Scale(1.5) {
		t.Fatalf("padding %+v module-gap %+v: %v, want 24px and 1.5", cfg.Bar.Padding, cfg.Bar.ModuleGap, err)
	}
	const want = "invalid value: string \"nope\", expected a scale multiplier like 1.5 or a pixel length like \"24px\""
	defaults := Defaults()
	for doc, field := range map[string]string{
		"bar:\n  padding: nope\n  scale: 2\n":       "bar.padding",
		"dropdowns:\n  weather:\n    width: nope\n": "dropdowns.weather.width",
	} {
		cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", doc))
		var d Diagnostic
		if !errors.As(err, &d) || d.Field("Field") != field || d.Field("Error") != want {
			t.Errorf("%s: error %v, want %q at %s", field, err, want, field)
		}
		if cfg.Bar.Padding != defaults.Bar.Padding || cfg.Dropdowns.Weather != defaults.Dropdowns.Weather {
			t.Errorf("%s: padding %+v weather %+v, want the defaults kept", field, cfg.Bar.Padding, cfg.Dropdowns.Weather)
		}
	}
	cfg, _ = LoadFile(writeFile(t, t.TempDir(), "config.yaml", "bar:\n  padding: nope\n  scale: 2\n"))
	if cfg.Bar.Scale != 2 {
		t.Errorf("scale %v: the valid sibling of a bad size was not applied", cfg.Bar.Scale)
	}
}

func TestBarLayoutItemsFollowTheUntaggedOrder(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.yaml", `
bar:
  layout:
    - monitor: DP-1
      left: [clock, {module: clock, class: primary}, {name: g, modules: [battery]}]
`)
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	left := cfg.Bar.Layout[0].Left
	if len(left) != 3 || left[0].Module != ModuleClock || left[1].Class != "primary" || !left[2].IsGroup() {
		t.Fatalf("left = %+v", left)
	}
	// An unset section takes BarLayout's own default, as serde(default)
	// fills it.
	if right := cfg.Bar.Layout[0].Right; len(right) != 5 {
		t.Errorf("right = %+v, want the default layout's right section", right)
	}
	bad := writeFile(t, t.TempDir(), "config.yaml", "bar:\n  layout:\n    - left: [nope]\n")
	cfg, err = LoadFile(bad)
	if err == nil || !strings.Contains(err.Error(), "data did not match any variant of untagged enum BarItem") {
		t.Errorf("unknown module: %v", err)
	}
	if len(cfg.Bar.Layout) != 1 || cfg.Bar.Layout[0].Monitor != "*" {
		t.Errorf("a bad layout keeps the default: %+v", cfg.Bar.Layout)
	}
}

func TestLoadErrorUnwraps(t *testing.T) {
	_, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "bar: [unclosed\n"))
	if _, ok := errors.AsType[*LoadError](err); !ok {
		t.Fatalf("syntax error = %T %v, want a LoadError", err, err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	if _, err := LoadFile(filepath.Join(dir, "sub", "config.yaml")); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("unwritable dir: %v", err)
	}
}
