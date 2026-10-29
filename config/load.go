package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// File loading with imports: a config file may list `imports: [...]`
// (YAML paths relative to the importing file, a bare name trying .yaml,
// then .yml); imports merge in order and the importing file wins;
// import cycles are an error naming the chain.

// LoadError is a whole-file failure: the file (or an import) could not
// be read or parsed. The cause is available through errors.Unwrap.
type LoadError struct {
	msg string
	err error
}

func (e *LoadError) Error() string { return e.msg }
func (e *LoadError) Unwrap() error { return e.err }

// loadTree reads path with its imports resolved into one merged tree.
// A missing main file is created with a stub first, so fresh installs
// have a file to edit and to watch, unless a TOML config from before
// the YAML switch sits beside it: that is an error until the user
// converts it, never silently replaced by an empty config.
func loadTree(path string) (any, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		if err := legacyTOMLError(filepath.Join(filepath.Dir(path), "config.toml"), path); err != nil {
			return nil, err
		}
		if err := createDefaultConfigFile(path); err != nil {
			return nil, err
		}
	}
	return readTree(path)
}

// readExistingTree is loadTree for a file its reader does not own: a
// missing main file is no tree and no error, and nothing is written.
func readExistingTree(path string) (any, error) {
	tree, err := readTree(path)
	if errors.Is(err, errMainMissing) {
		return nil, nil
	}
	return tree, err
}

// errMainMissing marks readTree's main file as absent, apart from a
// missing import, which stays an error everywhere.
var errMainMissing = errors.New("config file is missing")

// readTree reads the existing file at path with its imports resolved.
func readTree(path string) (any, error) {
	canonical, err := resolveFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &LoadError{msg: "cannot resolve path", err: errors.Join(errMainMissing, err)}
	}
	if err != nil {
		return nil, &LoadError{msg: "cannot resolve path", err: err}
	}
	var chain []string
	return loadMerged(canonical, path, &chain)
}

// resolveFile is path with its symlinks resolved, made absolute.
func resolveFile(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(canonical)
}

// legacyTOMLError reports a TOML file left from before lys-shell read
// YAML only, naming the YAML file that replaces it; nil when there is
// none.
func legacyTOMLError(legacy, replacement string) error {
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	return &LoadError{msg: fmt.Sprintf("%s is TOML, which lys-shell no longer reads: convert it to %s", legacy, replacement)}
}

// defaultConfigStub is a fresh config file: the schema modeline the
// YAML language server reads for completion and validation.
const defaultConfigStub = "# yaml-language-server: $schema=./schema.json\n# lys-shell configuration file\n"

func createDefaultConfigFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // the config dir is user-owned and world-readable
		return &LoadError{msg: "cannot create directory", err: err}
	}
	if err := os.WriteFile(path, []byte(defaultConfigStub), 0o644); err != nil { //nolint:gosec // config files are not secret
		return &LoadError{msg: "cannot write file", err: err}
	}
	return nil
}

// loadMerged loads the main file; importBase is the path imports
// resolve against (the path as given, not its symlink target).
func loadMerged(path, importBase string, chain *[]string) (any, error) {
	if err := detectCycle(*chain, path); err != nil {
		return nil, err
	}
	*chain = append(*chain, path)
	data, err := os.ReadFile(path) //nolint:gosec // a config path resolved by discovery or an import
	if err != nil {
		return nil, &LoadError{msg: "cannot read file", err: err}
	}
	merged, err := mergeWithImports(data, path, importBase, chain)
	if err != nil {
		return nil, err
	}
	*chain = (*chain)[:len(*chain)-1]
	return merged, nil
}

// mergeWithImports parses one file, loads its imports, then merges the
// file itself on top.
func mergeWithImports(data []byte, path, importBase string, chain *[]string) (any, error) {
	main, err := parseYAML(data)
	if err != nil {
		return nil, &LoadError{msg: fmt.Sprintf("%s: %v", path, err), err: err}
	}
	importPaths := importList(main)
	imported := make([]any, 0, len(importPaths))
	for _, imp := range importPaths {
		resolved := resolveImport(importBase, imp)
		if !isYAMLPath(resolved) {
			return nil, &LoadError{msg: fmt.Sprintf("cannot import '%s': imports must be YAML (.yaml or .yml)", resolved)}
		}
		canonical, err := resolveFile(resolved)
		if err != nil {
			return nil, &LoadError{
				msg: fmt.Sprintf("cannot import '%s'", resolved),
				err: &LoadError{msg: "cannot resolve path", err: err},
			}
		}
		tree, err := loadImport(canonical, chain)
		if err != nil {
			return nil, err
		}
		imported = append(imported, tree)
	}
	return mergeAll(imported, main), nil
}

func loadImport(path string, chain *[]string) (any, error) {
	if err := detectCycle(*chain, path); err != nil {
		return nil, err
	}
	*chain = append(*chain, path)
	defer func() { *chain = (*chain)[:len(*chain)-1] }()
	data, err := os.ReadFile(path) //nolint:gosec // an import path from the user's own config
	if err != nil {
		return nil, &LoadError{
			msg: fmt.Sprintf("cannot import '%s'", path),
			err: &LoadError{msg: "cannot read file", err: err},
		}
	}
	return mergeWithImports(data, path, path, chain)
}

// detectCycle errors when path is already on the import chain, naming
// the chain by file names: "a.yaml -> b.yaml -> a.yaml".
func detectCycle(chain []string, path string) error {
	for _, p := range chain {
		if p != path {
			continue
		}
		names := make([]string, 0, len(chain)+1)
		for _, c := range chain {
			names = append(names, filepath.Base(c))
		}
		names = append(names, filepath.Base(path))
		return &LoadError{msg: "circular import detected: " + strings.Join(names, " -> ")}
	}
	return nil
}

// importList reads the string entries of a top-level `imports` array;
// non-strings are skipped and a non-array is no imports.
func importList(tree any) []string {
	raw, _ := lookup(tree, "imports")
	list, _ := raw.([]any)
	var out []string
	for _, entry := range list {
		if s, ok := entry.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// resolveImport resolves an import against the importing file's
// directory (an absolute import stands alone). With no extension an
// existing .yml sibling is taken, otherwise the .yaml one.
func resolveImport(base, imp string) string {
	dir := filepath.Dir(base)
	join := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(dir, p)
	}
	if filepath.Ext(imp) != "" {
		return join(imp)
	}
	if candidate := join(imp + ".yml"); fileExists(candidate) && !fileExists(join(imp+".yaml")) {
		return candidate
	}
	return join(imp + ".yaml")
}

// isYAMLPath reports whether path names a YAML file (.yaml/.yml).
func isYAMLPath(path string) bool {
	switch filepath.Ext(path) {
	case ".yaml", ".yml":
		return true
	}
	return false
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
