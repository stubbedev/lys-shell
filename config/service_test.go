package config

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func collectDiags() (DiagnosticSink, func() []Diagnostic) {
	var mu sync.Mutex
	var got []Diagnostic
	return func(d Diagnostic) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, d)
		}, func() []Diagnostic {
			mu.Lock()
			defer mu.Unlock()
			return append([]Diagnostic(nil), got...)
		}
}

func TestRuntimeLayerOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "bar:\n  location: bottom\n")
	writeFile(t, dir, "runtime.yaml", "bar:\n  location: left\n")
	svc := Load(dir, DiscardDiagnostics)
	if got := svc.Config().Bar.Location; got != LocationLeft {
		t.Errorf("runtime > config: location = %q", got)
	}
	cleared, err := svc.ResetByPath("bar.location")
	if err != nil || !cleared {
		t.Fatalf("reset: %v %v", cleared, err)
	}
	if got := svc.Config().Bar.Location; got != LocationBottom {
		t.Errorf("after reset: location = %q, want the config value", got)
	}
	if cleared, _ := svc.ResetByPath("bar.location"); cleared {
		t.Error("second reset: nothing left to clear")
	}
}

func TestSetSavesAndReadsBack(t *testing.T) {
	dir := t.TempDir()
	svc := Load(dir, DiscardDiagnostics)
	if err := svc.SetByPath("bar.padding", ParseCLIValue("0.35")); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetByPath("modules.clock.format", ParseCLIValue("%H:%M")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "runtime.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// f32 values widen to f64; tables in field order.
	want := "bar:\n  padding: 0.3499999940395355\nmodules:\n  clock:\n    format: '%H:%M'\n"
	if string(data) != want {
		t.Errorf("runtime.yaml =\n%s\nwant\n%s", data, want)
	}
	reloaded := Load(dir, DiscardDiagnostics)
	if reloaded.Config().Clock.Format != "%H:%M" {
		t.Errorf("reloaded format = %q", reloaded.Config().Clock.Format)
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime.tmp")); err == nil {
		t.Error("runtime.tmp left behind")
	}
}

func TestSetRejectsBadValueWithTheRuntimeMessage(t *testing.T) {
	sink, diags := collectDiags()
	svc := Load(t.TempDir(), sink)
	err := svc.SetByPath("bar.location", "sideways")
	var rv *RuntimeValueError
	if !errors.As(err, &rv) || !strings.HasPrefix(err.Error(), "invalid value for 'bar.location': unknown variant `sideways`") {
		t.Fatalf("err = %v", err)
	}
	if d := diags(); len(d) != 1 || d[0].Title != "invalid runtime value" {
		t.Errorf("diagnostics = %+v", d)
	}
	if svc.Config().Bar.Location != LocationTop {
		t.Error("a rejected value must not apply")
	}
}

func TestGetByPath(t *testing.T) {
	svc := Load(t.TempDir(), DiscardDiagnostics)
	v, err := svc.GetByPath("bar.location")
	if err != nil || v != "top" {
		t.Errorf("bar.location = %v, %v", v, err)
	}
	v, _ = svc.GetByPath("bar.padding")
	if got := FormatGetValue(v); got != "0.3499999940395355" {
		t.Errorf("f32 get = %q", got)
	}
	_, err = svc.GetByPath("bar.nope")
	if err == nil || err.Error() != "invalid config field 'nope' in bar.nope: field not found" {
		t.Errorf("unknown path: %v", err)
	}
}

func TestResetByPathErrors(t *testing.T) {
	svc := Load(t.TempDir(), DiscardDiagnostics)
	for path, want := range map[string]string{
		"bar":                            "empty path",
		"bar.nope":                       "unknown field 'nope'",
		"bar.location.x":                 "no nested field at 'x'",
		"imports":                        "unknown field 'imports'",
		"modules.notification.icon-name": "",
	} {
		_, err := svc.ResetByPath(path)
		if want == "" {
			if err != nil {
				t.Errorf("%s: alias path: %v", path, err)
			}
			continue
		}
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v, want %q", path, err, want)
		}
	}
}

func TestRuntimeStopsAtTheFirstBadValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "runtime.yaml", "bar:\n  location: left\n  layer: nope\n  bg: red\n")
	svc := Load(dir, DiscardDiagnostics)
	cfg := svc.Config()
	if cfg.Bar.Location != LocationLeft {
		t.Error("the leaf before the bad one applies")
	}
	if cfg.Bar.BG != mustColor("bg-surface") {
		t.Error("the leaf after the bad one must not apply (the derive's `?`)")
	}
}

func TestSubscribersSeeChangesOnly(t *testing.T) {
	svc := Load(t.TempDir(), DiscardDiagnostics)
	var calls int
	var gotLocation Location
	cancel := Watch(svc, func(c *Config) Location { return c.Bar.Location }, func(l Location) {
		calls++
		gotLocation = l
	})
	defer cancel()
	_ = svc.SetByPath("bar.exclusive", false)
	if calls != 0 {
		t.Errorf("an unrelated change fired the location watch")
	}
	_ = svc.SetByPath("bar.location", "bottom")
	if calls != 1 || gotLocation != LocationBottom {
		t.Errorf("calls %d location %q", calls, gotLocation)
	}
	cancel()
	_ = svc.SetByPath("bar.location", "left")
	if calls != 1 {
		t.Error("a cancelled watch fired")
	}
}

func TestReloadWarnsWhenRuntimeShadowsConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "bar:\n  location: bottom\n")
	sink, diags := collectDiags()
	svc := Load(dir, sink)
	if err := svc.SetByPath("bar.location", "left"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "config.yaml", "bar:\n  location: right\n")
	if err := svc.reloadMain(); err != nil {
		t.Fatal(err)
	}
	var warned bool
	for _, d := range diags() {
		if d.Kind == DiagnosticWarning && d.Field("Field") == "bar.location" && d.Hint == "lys-shell config reset bar.location" {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no shadow warning in %+v", diags())
	}
	if svc.Config().Bar.Location != LocationLeft {
		t.Error("the runtime override must still win")
	}
	// A broken main file keeps everything.
	writeFile(t, dir, "config.yaml", "bar: [unclosed\n")
	if err := svc.reloadMain(); err == nil {
		t.Error("broken config: want an error")
	}
	if svc.Config().Bar.Location != LocationLeft {
		t.Error("a failed reload must keep the previous config")
	}
}

// Overrides left in a runtime.toml from before the YAML switch are
// reported instead of silently dropped, and the next save writes
// runtime.yaml.
func TestLegacyRuntimeTOMLIsReported(t *testing.T) {
	dir := t.TempDir()
	legacy := writeFile(t, dir, "runtime.toml", "[bar]\nlocation = \"left\"\n")
	if _, err := readRuntime(filepath.Join(dir, "runtime.yaml")); err == nil ||
		!strings.Contains(err.Error(), legacy+" is TOML, which lys-shell no longer reads: convert it to "+filepath.Join(dir, "runtime.yaml")) {
		t.Errorf("legacy runtime.toml: %v", err)
	}
	svc := Load(dir, DiscardDiagnostics)
	if svc.Config().Bar.Location != LocationTop {
		t.Error("the TOML overrides applied")
	}
	if err := svc.SetByPath("bar.location", "right"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Save(); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "runtime.yaml")); string(data) != "bar:\n  location: right\n" {
		t.Errorf("runtime.yaml = %q", data)
	}
	if _, err := readRuntime(filepath.Join(dir, "runtime.yaml")); err != nil {
		t.Errorf("with runtime.yaml present the TOML file is irrelevant: %v", err)
	}
}

func TestResetAllRuntime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "runtime.yaml", "bar:\n  location: left\n")
	svc := Load(dir, DiscardDiagnostics)
	if err := svc.ResetAllRuntime(); err != nil {
		t.Fatal(err)
	}
	if svc.Config().Bar.Location != LocationTop {
		t.Error("overrides survived")
	}
	if _, err := os.Stat(filepath.Join(dir, "runtime.yaml")); err == nil {
		t.Error("runtime.yaml survived")
	}
	if err := svc.ResetAllRuntime(); err != nil {
		t.Errorf("no file: %v", err)
	}
}

func TestWatcherReloadsAfterTheDebounce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "bar:\n  location: bottom\n")
	svc := Load(dir, DiscardDiagnostics)
	if err := svc.startWatcher(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	changed := make(chan Location, 4)
	Watch(svc, func(c *Config) Location { return c.Bar.Location }, func(l Location) { changed <- l })
	writeFile(t, dir, "config.yaml", "bar:\n  location: left\n")
	select {
	case l := <-changed:
		if l != LocationLeft {
			t.Errorf("reloaded location = %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reload after a config write")
	}
	writeFile(t, dir, "runtime.yaml", "bar:\n  location: right\n")
	select {
	case l := <-changed:
		if l != LocationRight {
			t.Errorf("runtime reload = %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no reload after a runtime.yaml write")
	}
}

func TestDebounceCeiling(t *testing.T) {
	start := time.Unix(0, 0)
	ceiling := start.Add(maxDebounceDelay)
	if got := nextDeadline(start, ceiling); !got.Equal(start.Add(debounceDelay)) {
		t.Errorf("isolated event: %v", got.Sub(start))
	}
	late := start.Add(maxDebounceDelay - 10*time.Millisecond)
	if got := nextDeadline(late, ceiling); !got.Equal(ceiling) {
		t.Errorf("continuous stream: deadline %v past the ceiling", got.Sub(start))
	}
}

func TestIsImmutableStore(t *testing.T) {
	t.Setenv("NIX_STORE_DIR", "")
	if !isImmutableStore("/nix/store") || isImmutableStore("/nix/store/abc-source") || isImmutableStore("/home/u/.config/lys-shell") {
		t.Error("only the store root is immutable")
	}
}

func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "LYS_SHELL_TEST_A=one\n# comment\nexport LYS_SHELL_TEST_B=\"two words\"\n")
	writeFile(t, dir, ".api.env", "LYS_SHELL_TEST_C='three' \n")
	writeFile(t, dir, "not.env", "LYS_SHELL_TEST_D=four\n")
	t.Setenv("LYS_SHELL_TEST_A", "preset")
	for _, k := range []string{"LYS_SHELL_TEST_B", "LYS_SHELL_TEST_C", "LYS_SHELL_TEST_D"} {
		_ = os.Unsetenv(k)
		t.Cleanup(func() { _ = os.Unsetenv(k) })
	}
	LoadEnvFiles(dir)
	if got, _ := ResolveSecret("$LYS_SHELL_TEST_A"); got != "preset" {
		t.Errorf("an existing variable was overridden: %q", got)
	}
	if got, ok := ResolveSecret("$LYS_SHELL_TEST_B"); !ok || got != "two words" {
		t.Errorf("B = %q %v", got, ok)
	}
	if got, ok := ResolveSecret("$LYS_SHELL_TEST_C"); !ok || got != "three" {
		t.Errorf("C = %q %v", got, ok)
	}
	if _, ok := ResolveSecret("$LYS_SHELL_TEST_D"); ok {
		t.Error("not.env is not a secrets file")
	}
	if got, ok := ResolveSecret("literal"); !ok || got != "literal" {
		t.Errorf("literal = %q", got)
	}
	if !isEnvFile("/x/.env") || !isEnvFile("/x/.a.env") || isEnvFile("/x/a.env") {
		t.Error("isEnvFile")
	}
}

// logged runs fn with the standard logger writing to a buffer and
// returns what it logged.
func logged(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	fn()
	return buf.String()
}

// runtime.yaml exists only once an override was written: without it
// the config loads silently, and deleting it drops the overrides. A
// runtime.yaml that cannot be read or parsed still says so.
func TestMissingRuntimeYAMLIsNoOverrides(t *testing.T) {
	dir := t.TempDir()
	var svc *Service
	if out := logged(t, func() { svc = Load(dir, DiscardDiagnostics) }); out != "" {
		t.Errorf("loading without runtime.yaml logged %q", out)
	}
	if svc.Config().Bar.Location != LocationTop {
		t.Errorf("bar.location = %v without overrides", svc.Config().Bar.Location)
	}

	runtime := writeFile(t, dir, "runtime.yaml", "bar:\n  location: left\n")
	svc.reloadRuntime()
	if svc.Config().Bar.Location != LocationLeft {
		t.Fatalf("bar.location = %v after the override", svc.Config().Bar.Location)
	}
	if err := os.Remove(runtime); err != nil {
		t.Fatal(err)
	}
	svc.reloadRuntime()
	if svc.Config().Bar.Location != LocationTop {
		t.Errorf("bar.location = %v after runtime.yaml was deleted", svc.Config().Bar.Location)
	}

	for name, write := range map[string]func(string){
		"malformed":  func(p string) { writeFile(t, dir, "runtime.yaml", "bar: [\n") },
		"unreadable": func(p string) { writeFile(t, dir, "runtime.yaml", "bar: {}\n"); _ = os.Chmod(p, 0) },
	} {
		write(runtime)
		if out := logged(t, func() { Load(dir, DiscardDiagnostics) }); !strings.Contains(out, "config: runtime.yaml failed") {
			t.Errorf("%s runtime.yaml logged %q", name, out)
		}
		_ = os.Remove(runtime)
	}
}
