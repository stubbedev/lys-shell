package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSchemaCurrentReplacesAnythingElse(t *testing.T) {
	for name, stale := range map[string]string{
		"another version":             `{"$id":"lys-shell-config-0.0.1"}`,
		"a half-written earlier file": `{"$id"`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(SchemaPath(dir), []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := EnsureSchemaCurrent(dir); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := os.ReadFile(SchemaPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != GenerateSchema() {
			t.Errorf("%s: schema.json was not replaced", name)
		}
	}
}

func TestEnsureSchemaCurrentReplacesTheSameVersionWithOtherDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureSchemaCurrent(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SchemaPath(dir), []byte(GenerateSchema()+" "), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchemaCurrent(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(SchemaPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != GenerateSchema() {
		t.Error("a schema carrying this version's id but other content was kept")
	}
}

func TestEnsureSchemaCurrentLeavesACurrentFileAlone(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureSchemaCurrent(dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(SchemaPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchemaCurrent(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(SchemaPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("a current schema.json was rewritten")
	}
}

func TestEnsureSchemaCurrentNamesTheFileItCannotWrite(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchemaCurrent(blocked); err == nil {
		t.Fatal("a config dir that is a file gave no error")
	}
}
