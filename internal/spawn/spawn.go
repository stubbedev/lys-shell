// Package spawn runs the shell commands the config names (click actions,
// power commands) and the apps the launcher starts.
package spawn

import (
	"bytes"
	"errors"
	"log"
	"os/exec"
	"strings"
)

// Quiet runs cmd through sh -c in the background and returns at once, a
// click waiting on neither the fork nor the session bus.
func Quiet(cmd string) {
	if strings.TrimSpace(cmd) == "" {
		return
	}
	scopes.moves.Add(1)
	go runQuiet(cmd)
}

func runQuiet(cmd string) {
	c := exec.Command("sh", "-c", cmd) //nolint:gosec // the command comes from the user's own config
	var stderr bytes.Buffer
	c.Stderr = &stderr
	err := Scoped(c, strings.Fields(cmd)[0])
	scopes.moves.Done()
	if err != nil {
		log.Printf("cannot spawn command %q: %v", cmd, err)
		return
	}
	err = c.Wait()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			log.Printf("command failed: %q exit %d: %s", cmd, exit.ExitCode(), msg)
		} else {
			log.Printf("command failed: %q exit %d", cmd, exit.ExitCode())
		}
	case err != nil:
		log.Printf("cannot wait on command %q: %v", cmd, err)
	}
}
