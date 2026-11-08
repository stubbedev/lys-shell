package bar

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/config"

	"github.com/stubbedev/lys-shell/internal/apptest"
)

func TestBarsAffected(t *testing.T) {
	base := config.Defaults()
	same := config.Defaults()
	if barsAffected(base, same) {
		t.Error("identical configs rebuild the bars")
	}
	capture := config.Defaults()
	capture.Recorder.Microphone = !base.Recorder.Microphone
	capture.Recorder.MicrophoneDevice = "mic0"
	capture.Recorder.WebcamX, capture.Recorder.WebcamY = 33, 66
	capture.Recorder.WebcamEnabled = !base.Recorder.WebcamEnabled
	if barsAffected(base, capture) {
		t.Error("recorder capture settings rebuild the bars")
	}
	tone := config.Defaults()
	tone.Launcher.Emoji.SkinTone = config.SkinToneDark
	if barsAffected(base, tone) {
		t.Error("the emoji skin tone picked in the launcher rebuilds the bars")
	}
	icon := config.Defaults()
	icon.Recorder.IconIdle = "other"
	if !barsAffected(base, icon) {
		t.Error("a recorder icon change does not rebuild the bars")
	}
	loc := config.Defaults()
	loc.Bar.Location = config.LocationBottom
	if !barsAffected(base, loc) {
		t.Error("a bar change does not rebuild the bars")
	}
	if !barsAffected(nil, base) {
		t.Error("no previous config must rebuild")
	}
	// The scrubbed fields are scrubbed on copies, not the live configs.
	if capture.Recorder.MicrophoneDevice != "mic0" {
		t.Error("barsAffected mutated its argument")
	}
}

func TestConfigSetterPersists(t *testing.T) {
	dir := t.TempDir()
	svc := config.Load(dir, config.DiscardDiagnostics)
	defer svc.Close()
	w := &configWriter{store: svc}
	set := func(path string, value any) {
		w.set(path, value)
		w.flush(10 * time.Second)
	}
	set("modules.recorder.microphone-device", "mic0")
	if got := svc.Config().Recorder.MicrophoneDevice; got != "mic0" {
		t.Errorf("live value = %q", got)
	}
	body, err := os.ReadFile(filepath.Join(dir, "runtime.yaml"))
	if err != nil || !strings.Contains(string(body), "microphone-device: mic0\n") {
		t.Errorf("runtime.yaml = %q (%v)", body, err)
	}
	// A bad path is logged and leaves everything as it was.
	set("modules.recorder.nope", true)
	if got := svc.Config().Recorder.MicrophoneDevice; got != "mic0" {
		t.Errorf("after a bad set = %q", got)
	}
	var nilCtx ModuleContext
	nilCtx.setConfig("modules.recorder.microphone", true) // no service: a no-op
}

type heldConfigStore struct {
	mu      sync.Mutex
	sets    []string
	saves   int
	entered chan struct{}
	release chan struct{}
}

func (s *heldConfigStore) SetByPath(path string, _ any) error {
	s.entered <- struct{}{}
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sets = append(s.sets, path)
	return nil
}

func (s *heldConfigStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	return nil
}

func TestConfigWriterCoalescesSavesAndNeverHoldsItsCaller(t *testing.T) {
	store := &heldConfigStore{entered: make(chan struct{}, 8), release: make(chan struct{})}
	w := &configWriter{store: store}
	w.set("a", 1)
	apptest.Answer(t, "the first set reached the store", store.entered)
	w.set("b", 2)
	close(store.release)
	w.flush(10 * time.Second)
	store.mu.Lock()
	defer store.mu.Unlock()
	if !slices.Equal(store.sets, []string{"a", "b"}) {
		t.Errorf("sets %v, want both in order", store.sets)
	}
	if store.saves != 1 {
		t.Errorf("%d saves for two sets, want one", store.saves)
	}
}
