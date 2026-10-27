// Package prompt asks the user for lys-secrets' passwords through the
// shell's dialog host (dev.stubbe.lys.PortalDialogs1), so the prompts
// look like every other lys dialog and lys-secrets draws no UI itself.
package prompt

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/portalreq"
	"github.com/stubbedev/lys-shell/shell/portaldialogs"
)

// Dialogs is a service.Prompter over the shell's dialogs.
type Dialogs struct {
	client *portaldialogs.Client
	seq    uint64
}

// New prompts through the dialog host on conn.
func New(conn *dbus.Conn) *Dialogs { return &Dialogs{client: portaldialogs.NewClient(conn)} }

func (d *Dialogs) ask(ctx context.Context, r portaldialogs.PasswordRequest) ([]byte, bool, error) {
	d.seq++
	caller := portalreq.Caller{Handle: fmt.Sprintf("/dev/stubbe/lys/secrets/prompt/%d", d.seq)}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			dismiss, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = d.client.Dismiss(dismiss, caller.Handle)
		case <-done:
		}
	}()
	ok, password, err := d.client.Password(ctx, caller, r)
	if err != nil {
		return nil, false, fmt.Errorf("ask through %s: %w", portaldialogs.ServiceName, err)
	}
	if !ok {
		return nil, false, nil
	}
	return []byte(password), true, nil
}

// Password asks for an existing collection's password.
func (d *Dialogs) Password(ctx context.Context, collection string, retry bool) ([]byte, bool, error) {
	return d.ask(ctx, portaldialogs.PasswordRequest{
		Title: "Unlock keyring",
		Body:  fmt.Sprintf("An application wants to use the “%s” keyring, which is locked.", collection),
		Retry: retry,
	})
}

// NewPassword asks for a new collection's password, twice.
func (d *Dialogs) NewPassword(ctx context.Context, collection string) ([]byte, bool, error) {
	return d.ask(ctx, portaldialogs.PasswordRequest{
		Title:   "New keyring password",
		Body:    fmt.Sprintf("Choose a password for the “%s” keyring. It encrypts the secrets stored in it.", collection),
		Confirm: true,
	})
}
