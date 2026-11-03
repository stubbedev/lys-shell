package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
)

func TestStartingTheShellRefreshesASchemaFromAnotherVersion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir, err := config.Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(path, []byte(`{"$id":"lys-shell-config-0.0.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	want := errors.New("the shell ended")
	if err := refreshingSchema(func() error { return want })(); !errors.Is(err, want) {
		t.Fatalf("the shell's own result was replaced: %v", err)
	}
	expected := config.GenerateSchema()
	deadline := time.Now().Add(30 * time.Second)
	for {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) == expected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("schema.json still holds %d stale bytes", len(got))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
