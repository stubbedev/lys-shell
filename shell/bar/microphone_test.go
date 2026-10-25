package bar

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/styling"
)

func TestMicrophoneLabel(t *testing.T) {
	if got := microphoneLabel(37.2); got != "37%" {
		t.Errorf("= %q, want 37%%", got)
	}
	if got := microphoneLabel(0); got != "0%" {
		t.Errorf("= %q, want 0%%", got)
	}
	// Rounded like the Rust module, not truncated.
	if got := microphoneLabel(37.6); got != "38%" {
		t.Errorf("= %q, want 38%%", got)
	}
}

func TestMicrophoneModuleDimsWhenMuted(t *testing.T) {
	cfg := config.Defaults()
	style := computeStyle(cfg, styling.Default()).forModules()
	source := &fakePulseSource{dev: pulse.Device{Name: "mic", Volume: pct(65)}, ticks: make(chan struct{}, 2)}
	m := &microphoneModule{
		ctx:    ModuleContext{Config: cfg, Font: testFont(t), Style: &style},
		source: source,
	}
	m.btn = newBarButton(m.ctx, m.icon, "")
	if err := m.refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got := m.btn.Label().Text(); got != "65%" {
		t.Errorf("label = %q, want 65%%", got)
	}
	if m.btn.Label().Color() != 0 {
		t.Errorf("active ink = %#08x, want the stylesheet's", m.btn.Label().Color())
	}

	// Muted changes the icon only; the label keeps label-color.
	source.dev.Muted = true
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if m.btn.Label().Color() != 0 {
		t.Errorf("muted ink = %#08x, want the stylesheet's", m.btn.Label().Color())
	}
}

func TestNewMicrophoneRequiresSource(t *testing.T) {
	cfg := config.Defaults()
	ctx := newTestContext(t, cfg)
	ctx.Pulse = nil
	if _, err := Create("microphone", ctx); err == nil {
		t.Fatal("nil pulse source: want an error, got a module")
	}
}

func TestLoadFileAppliesMicrophone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "modules:\n  microphone:\n    icon-muted: m-off\n    label-show: false\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if c.Microphone.IconMuted != "m-off" || c.Microphone.LabelShow {
		t.Errorf("config = %+v", c.Microphone)
	}
}

// The thresholds color the button by input volume (evaluate_thresholds,
// last match wins) and clear below them.
func TestMicrophoneThresholds(t *testing.T) {
	cfg := config.Defaults()
	warn, err := config.ParseColorValue("status-warning")
	if err != nil {
		t.Fatal(err)
	}
	above := 60.0
	cfg.Microphone.Thresholds = []config.ThresholdEntry{{Above: &above, LabelColor: &warn, IconColor: &warn}}
	style := computeStyle(cfg, styling.Default()).forModules()
	source := &fakePulseSource{dev: pulse.Device{Name: "mic", Volume: pct(65)}, ticks: make(chan struct{}, 2)}
	m := &microphoneModule{ctx: ModuleContext{Config: cfg, Font: testFont(t), Style: &style}, source: source}
	m.btn = newBarButton(m.ctx, m.icon, "")
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if got := m.btn.thresholds; got.LabelColor == nil || *got.LabelColor != warn || got.IconColor == nil {
		t.Errorf("at 65%%: thresholds %+v, want the warning colors", got)
	}
	source.dev.Volume = pct(30)
	if err := m.refresh(); err != nil {
		t.Fatal(err)
	}
	if !m.btn.thresholds.IsEmpty() {
		t.Errorf("at 30%%: thresholds %+v, want none", m.btn.thresholds)
	}
}
