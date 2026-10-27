package greeter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/stubbedev/lys-shell/config"
)

// RunApplyConfig is `lys-greeter apply-config [--config PATH]
// <STAGED.yaml>`, run as root through pkexec by lys-settings: it
// copies only the allowlisted login keys of the staged file into
// runtime.yaml beside the system config, never touching config.yaml.
// It returns the exit code and writes its messages to stdout/stderr.
func RunApplyConfig(args []string, stdout, stderr io.Writer) int {
	configPath := config.GreeterConfigPath
	staged := ""
	for rest := args; len(rest) > 0; {
		arg := rest[0]
		rest = rest[1:]
		switch arg {
		case "--config":
			if len(rest) == 0 {
				return applyFail(stderr, "--config requires a path")
			}
			configPath, rest = rest[0], rest[1:]
		default:
			if staged != "" {
				return applyFail(stderr, "unexpected extra argument")
			}
			staged = arg
		}
	}
	if staged == "" {
		return applyFail(stderr, "usage: lys-greeter apply-config [--config PATH] <STAGED.yaml>")
	}
	dest, err := applyConfig(staged, configPath, asPkexecCaller)
	if err != nil {
		return applyFail(stderr, err.Error())
	}
	_, _ = fmt.Fprintf(stdout, "wrote %s\n", dest)
	return 0
}

func applyFail(stderr io.Writer, msg string) int {
	_, _ = fmt.Fprintf(stderr, "lys-greeter apply-config: %s\n", msg)
	return 1
}

// applyConfig puts the staged login keys the greeter takes into
// runtime.yaml and returns its path. readAsCaller performs the one privileged read a
// user controls - the background image - with the caller's rights.
func applyConfig(staged, configPath string, readAsCaller func(func() ([]byte, error)) ([]byte, error)) (string, error) {
	text, err := os.ReadFile(staged) //nolint:gosec // the staged file lys-settings names
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", staged, err)
	}
	table, err := config.ParseYAML(string(text))
	if err != nil {
		return "", fmt.Errorf("invalid YAML in %s: %w", staged, err)
	}
	// Accept a login table or its keys at top level.
	var source any = table
	if inner, ok := table["login"].(map[string]any); ok {
		source = inner
	}
	login := map[string]any{}
	for _, key := range config.GreeterApplyKeys {
		v, ok := config.PathValue(source, key)
		if !ok {
			continue
		}
		if err := config.InsertPath(login, key, v); err != nil {
			return "", fmt.Errorf("staged %s: %w", key, err)
		}
	}
	if len(login) == 0 {
		return "", errors.New("no recognised login screen keys in staged file")
	}
	if err := relocateBackgroundImage(login, configPath, readAsCaller); err != nil {
		return "", err
	}

	dest := config.RuntimeOverlayPath(configPath)
	root := map[string]any{}
	if existing, err := os.ReadFile(dest); err == nil { //nolint:gosec // the system runtime overlay
		if root, err = config.ParseYAML(string(existing)); err != nil {
			return "", fmt.Errorf("existing %s is invalid: %w", dest, err)
		}
	}
	root["login"] = login
	out, err := config.YAMLDocument(root)
	if err != nil {
		return "", fmt.Errorf("serialize %s: %w", dest, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil { //nolint:gosec // /etc/lys-shell is world-readable config
		return "", fmt.Errorf("cannot create %s: %w", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, []byte(out), 0o644); err != nil { //nolint:gosec // the greeter user reads it
		return "", fmt.Errorf("cannot write %s: %w", dest, err)
	}
	return dest, nil
}

// relocateBackgroundImage copies an image-mode background next to the
// system config and points the key there: the greeter runs as the
// unprivileged greeter user and cannot read the picking user's home.
// Other modes leave the key alone. The read runs with the pkexec
// caller's rights, so a caller cannot point background-image at a
// root-only file (/etc/shadow) and get a world-readable copy.
func relocateBackgroundImage(login map[string]any, configPath string, readAsCaller func(func() ([]byte, error)) ([]byte, error)) error {
	if mode, _ := login["background-mode"].(string); mode != "image" {
		return nil
	}
	src, _ := login["background-image"].(string)
	if src == "" {
		return nil
	}
	data, err := readAsCaller(func() ([]byte, error) { return os.ReadFile(src) }) //nolint:gosec // read with the caller's rights
	if err != nil {
		return fmt.Errorf("cannot read background image %s: %w", src, err)
	}
	ext := filepath.Ext(src)
	if ext == "" {
		ext = ".img"
	}
	dest := filepath.Join(filepath.Dir(configPath), "greeter-background"+ext)
	if err := os.WriteFile(dest, data, 0o644); err != nil { //nolint:gosec // world-readable so the greeter user can load it
		return fmt.Errorf("cannot write %s: %w", dest, err)
	}
	_ = os.Chmod(dest, 0o644) //nolint:gosec // world-readable on purpose
	login["background-image"] = dest
	return nil
}

// asPkexecCaller runs read with the effective uid dropped to
// PKEXEC_UID, restoring root after; unchanged when PKEXEC_UID is unset
// (invoked directly as root). Go's Seteuid applies to every thread.
func asPkexecCaller(read func() ([]byte, error)) ([]byte, error) {
	uid, err := strconv.Atoi(os.Getenv("PKEXEC_UID"))
	if err != nil {
		return read()
	}
	if err := syscall.Seteuid(uid); err != nil {
		return nil, fmt.Errorf("drop to caller uid %d: %w", uid, err)
	}
	data, readErr := read()
	if err := syscall.Seteuid(0); err != nil {
		return nil, fmt.Errorf("restore root: %w", err)
	}
	return data, readErr
}
