// Command distcheck fails when a distribution binary would not start
// on a standard Linux system: every ELF file it is given must request
// a program interpreter at a standard (FHS) path, or none. A Go
// toolchain from Nix links against a /nix/store loader, which exists
// only on NixOS, so a release built there without -I ships binaries
// that "cannot execute" anywhere else.
//
//	distcheck FILE...
package main

import (
	"debug/elf"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

// fhsInterpreters are the loaders a glibc system has at fixed paths.
var fhsInterpreters = []string{
	"/lib64/ld-linux-x86-64.so.2",
	"/lib/ld-linux-aarch64.so.1",
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: distcheck FILE...")
		os.Exit(2)
	}
	failed := false
	for _, path := range os.Args[1:] {
		if err := check(path); err != nil {
			fmt.Fprintln(os.Stderr, "distcheck:", err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

// check reports why path would not start on a standard system, nil
// when it would.
func check(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	interp, err := interpreter(f)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if interp != "" && !slices.Contains(fhsInterpreters, interp) {
		return fmt.Errorf("%s requests the interpreter %s, not a standard path (%s)", path, interp, strings.Join(fhsInterpreters, ", "))
	}
	return nil
}

// interpreter is the PT_INTERP path, "" for a static binary.
func interpreter(f *elf.File) (string, error) {
	for _, p := range f.Progs {
		if p.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(p.Open())
		if err != nil {
			return "", fmt.Errorf("read the interpreter: %w", err)
		}
		return strings.TrimRight(string(data), "\x00"), nil
	}
	return "", nil
}
