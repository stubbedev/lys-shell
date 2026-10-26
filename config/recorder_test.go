package config

import (
	"errors"
	"testing"
)

// output-format takes mp4 or mkv. WebM is gone with the VP9 encoder:
// a config still naming it is an error naming the key and the formats
// that remain, the default kept rather than a guess at what was meant.
func TestRecorderOutputFormatLoadsMP4AndMKVOnly(t *testing.T) {
	for raw, want := range map[string]RecorderFormat{"mp4": RecorderFormatMp4, "mkv": RecorderFormatMkv} {
		cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  recorder:\n    output-format: "+raw+"\n"))
		if err != nil || cfg.Recorder.OutputFormat != want {
			t.Errorf("output-format %s: %q, %v", raw, cfg.Recorder.OutputFormat, err)
		}
	}
	cfg, err := LoadFile(writeFile(t, t.TempDir(), "config.yaml", "modules:\n  recorder:\n    output-format: webm\n    framerate: 30\n"))
	var d Diagnostic
	const want = "unknown variant `webm`, expected `mp4` or `mkv`"
	if !errors.As(err, &d) || d.Field("Field") != "modules.recorder.output-format" || d.Field("Error") != want {
		t.Errorf("output-format webm: error %v, want %q", err, want)
	}
	if cfg.Recorder.OutputFormat != RecorderFormatMkv || cfg.Recorder.Framerate != 30 {
		t.Errorf("output-format webm: format %q framerate %d, want the default kept and the valid sibling applied", cfg.Recorder.OutputFormat, cfg.Recorder.Framerate)
	}
}
