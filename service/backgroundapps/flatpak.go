package backgroundapps

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-desktop/desktopentry"
	"github.com/stubbedev/lys-lib/xdg"
	"golang.org/x/sys/unix"
)

// InstanceID is a Flatpak instance id: a decimal number, the name of the
// instance's directory under $XDG_RUNTIME_DIR/.flatpak, so it can never
// be a path separator or "..".
type InstanceID struct{ id string }

// ParseInstanceID validates s as an instance id.
func ParseInstanceID(s string) (InstanceID, error) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return InstanceID{}, fmt.Errorf("background apps: %q is not a Flatpak instance id", s)
	}
	return InstanceID{id: s}, nil
}

// String is the id as Flatpak writes it.
func (i InstanceID) String() string { return i.id }

// procRoot is where the kernel lists processes.
const procRoot = "/proc"

// killInstance does what `flatpak kill` does: SIGKILL to the sandbox's
// first process, the child-pid of the instance's bwrapinfo.json.
func killInstance(id InstanceID, proc string) error {
	runtime, ok := xdg.OS.RuntimeHome()
	if !ok {
		return fmt.Errorf("instance %s: XDG_RUNTIME_DIR is not an absolute path", id)
	}
	return killInstanceIn(filepath.Join(runtime, ".flatpak", id.String()), id, proc)
}

// killInstanceIn signals the instance whose directory is dir through a
// pidfd, which cannot reach another process once open, after checking
// the process it holds is still that instance's: its root's
// .flatpak-info names id. A pid the instance left and the kernel handed
// on fails the check; one that dies after it fails the signal.
func killInstanceIn(dir string, id InstanceID, proc string) (err error) {
	pid, err := childPID(filepath.Join(dir, "bwrapinfo.json"))
	if err != nil {
		return fmt.Errorf("instance %s: %w", id, err)
	}
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return fmt.Errorf("instance %s: open process %d: %w", id, pid, err)
	}
	defer func() { err = errors.Join(err, unix.Close(fd)) }()
	running, err := runningInstance(filepath.Join(proc, strconv.Itoa(pid), "root", ".flatpak-info"))
	if err != nil {
		return fmt.Errorf("instance %s: process %d: %w", id, pid, err)
	}
	if running != id.String() {
		return fmt.Errorf("instance %s: process %d is instance %q now", id, pid, running)
	}
	if err := unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0); err != nil {
		return fmt.Errorf("instance %s: kill process %d: %w", id, pid, err)
	}
	return nil
}

func childPID(path string) (int, error) {
	data, err := os.ReadFile(path) //nolint:gosec // under the user's runtime dir, by a validated instance id
	if err != nil {
		return 0, fmt.Errorf("read the instance's bwrap info: %w", err)
	}
	var info struct {
		ChildPID int `json:"child-pid"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return 0, fmt.Errorf("decode %s: %w", path, err)
	}
	if info.ChildPID <= 0 {
		return 0, fmt.Errorf("%s names no child-pid", path)
	}
	return info.ChildPID, nil
}

func runningInstance(path string) (string, error) {
	kf, err := desktopentry.LoadKeyFile(path)
	if err != nil {
		return "", fmt.Errorf("read its .flatpak-info: %w", err)
	}
	running, ok := kf.String("Instance", "instance-id")
	if !ok {
		return "", fmt.Errorf("%s names no instance-id", path)
	}
	return running, nil
}
