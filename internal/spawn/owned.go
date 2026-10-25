package spawn

import (
	"context"
	"os/exec"
	"syscall"
	"time"
)

// ownedGrace is how long an owned command's group has to exit after
// SIGTERM before its pipes are closed under it.
const ownedGrace = 2 * time.Second

// Owned is `sh -c command` bound to ctx as a whole: it runs in a
// process group of its own, and ctx's end signals that group, so the
// script's children stop with it. exec.CommandContext alone kills only
// sh, and a watch script's pipeline outlived every bar rebuild.
func Owned(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // the command comes from the user's own config
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = ownedGrace
	return cmd
}
