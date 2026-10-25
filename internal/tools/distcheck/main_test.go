package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// build compiles a trivial program with the given linker flags.
func build(t *testing.T, ldflags string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\nimport _ \"os/user\"\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "bin")
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", out, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1", "GOFLAGS=-mod=mod")
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build a dynamically linked test binary: %v\n%s", err, b)
	}
	return out
}

// A binary asking for a standard loader passes; one asking for any
// other path (a /nix/store loader) fails, naming it.
func TestInterpreterMustBeStandard(t *testing.T) {
	ok := build(t, "-I /lib64/ld-linux-x86-64.so.2")
	if err := check(ok); err != nil {
		t.Errorf("a standard interpreter failed: %v", err)
	}
	bad := build(t, "-I /nix/store/x-glibc/lib/ld-linux-x86-64.so.2")
	err := check(bad)
	if err == nil || !strings.Contains(err.Error(), "/nix/store/x-glibc") {
		t.Errorf("a store interpreter = %v, want an error naming it", err)
	}
}

// A file that is not ELF is an error, not a pass.
func TestNotELFFails(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := check(p); err == nil {
		t.Error("a non-ELF file passed")
	}
}
