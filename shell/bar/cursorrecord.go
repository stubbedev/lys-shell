package bar

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// cursorRecordTimeout bounds lys-login record-cursor: one exchange on
// the display manager's socket.
const cursorRecordTimeout = 10 * time.Second

// recordCursor hands the session's cursor to lys-login's display
// manager for the next greeter: `lys-login record-cursor` reads
// XCURSOR_THEME and XCURSOR_SIZE (else HYPRCURSOR_SIZE) from the
// environment it inherits from the shell. The error carries the
// command's stderr; without lys-login's display manager (greetd, or no
// lys-login at all) that is the normal outcome.
func recordCursor(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, cursorRecordTimeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "lys-login", "record-cursor")
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("cursor record: lys-login record-cursor: %w: %s", err, msg)
		}
		return fmt.Errorf("cursor record: lys-login record-cursor: %w", err)
	}
	return nil
}
