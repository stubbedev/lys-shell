// Package client is a Secret Service client for lys components that keep
// a secret of their own in the user's keyring, such as the portal's
// per-app master secrets. It works against any provider on the bus
// (lys-secrets or gnome-keyring), over a plain session: the secret never
// leaves the user's own bus.
package client

import (
	"context"
	"errors"
	"fmt"

	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

const (
	busName         = "org.freedesktop.secrets"
	servicePath     = dbus.ObjectPath("/org/freedesktop/secrets")
	ifaceService    = "org.freedesktop.Secret.Service"
	ifaceCollection = "org.freedesktop.Secret.Collection"
	ifaceSession    = "org.freedesktop.Secret.Session"
	ifacePrompt     = "org.freedesktop.Secret.Prompt"
	noPath          = dbus.ObjectPath("/")
)

// ErrDismissed is the user (or the client) dismissing a prompt.
var ErrDismissed = errors.New("secrets: the prompt was dismissed")

type secret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// Client is one plain session with the Secret Service.
type Client struct {
	conn    *dbus.Conn
	svc     dbus.BusObject
	session dbus.ObjectPath
}

// Open starts a session; without a provider on the bus it fails.
func Open(ctx context.Context, conn *dbus.Conn) (*Client, error) {
	c := &Client{conn: conn, svc: conn.Object(busName, servicePath)}
	var out dbus.Variant
	if err := c.svc.CallWithContext(ctx, ifaceService+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &c.session); err != nil {
		return nil, fmt.Errorf("secrets: open a session with %s: %w", busName, err)
	}
	return c, nil
}

// Close ends the session.
func (c *Client) Close() {
	_ = c.conn.Object(busName, c.session).Call(ifaceSession+".Close", 0).Err
}

// Lookup finds the first item holding attrs and returns its secret,
// unlocking its collection through a prompt when it is locked. found is
// false when no item matches.
func (c *Client) Lookup(ctx context.Context, attrs map[string]string) (value []byte, found bool, err error) {
	var unlocked, locked []dbus.ObjectPath
	if err := c.svc.CallWithContext(ctx, ifaceService+".SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil {
		return nil, false, fmt.Errorf("secrets: search: %w", err)
	}
	var item dbus.ObjectPath
	switch {
	case len(unlocked) > 0:
		item = unlocked[0]
	case len(locked) > 0:
		item = locked[0]
		var done []dbus.ObjectPath
		var prompt dbus.ObjectPath
		if err := c.svc.CallWithContext(ctx, ifaceService+".Unlock", 0, []dbus.ObjectPath{item}).Store(&done, &prompt); err != nil {
			return nil, false, fmt.Errorf("secrets: unlock: %w", err)
		}
		if prompt != noPath {
			if _, err := c.prompt(ctx, prompt); err != nil {
				return nil, false, fmt.Errorf("secrets: unlock: %w", err)
			}
		}
	default:
		return nil, false, nil
	}
	var secrets map[dbus.ObjectPath]secret
	if err := c.svc.CallWithContext(ctx, ifaceService+".GetSecrets", 0, []dbus.ObjectPath{item}, c.session).Store(&secrets); err != nil {
		return nil, false, fmt.Errorf("secrets: read %s: %w", item, err)
	}
	s, ok := secrets[item]
	if !ok {
		return nil, false, fmt.Errorf("secrets: %s stayed locked", item)
	}
	return s.Value, true, nil
}

// Store saves value under attrs in the default collection, replacing an
// item with the same attributes; a missing default collection is created
// through the provider's prompt.
func (c *Client) Store(ctx context.Context, label string, attrs map[string]string, value []byte) error {
	var coll dbus.ObjectPath
	if err := c.svc.CallWithContext(ctx, ifaceService+".ReadAlias", 0, "default").Store(&coll); err != nil {
		return fmt.Errorf("secrets: read the default alias: %w", err)
	}
	if coll == noPath {
		var prompt dbus.ObjectPath
		props := map[string]dbus.Variant{ifaceCollection + ".Label": dbus.MakeVariant("Login")}
		if err := c.svc.CallWithContext(ctx, ifaceService+".CreateCollection", 0, props, "default").Store(&coll, &prompt); err != nil {
			return fmt.Errorf("secrets: create the default collection: %w", err)
		}
		if prompt != noPath {
			result, err := c.prompt(ctx, prompt)
			if err != nil {
				return fmt.Errorf("secrets: create the default collection: %w", err)
			}
			if coll, _ = result.Value().(dbus.ObjectPath); coll == "" || coll == noPath {
				return errors.New("secrets: the provider created no default collection")
			}
		}
	}
	props := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant(label),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(attrs),
	}
	var item, prompt dbus.ObjectPath
	s := secret{Session: c.session, Parameters: []byte{}, Value: value, ContentType: "application/octet-stream"}
	if err := c.conn.Object(busName, coll).CallWithContext(ctx, ifaceCollection+".CreateItem", 0, props, s, true).Store(&item, &prompt); err != nil {
		return fmt.Errorf("secrets: store in %s: %w", coll, err)
	}
	if prompt != noPath {
		if _, err := c.prompt(ctx, prompt); err != nil {
			return fmt.Errorf("secrets: store in %s: %w", coll, err)
		}
	}
	return nil
}

// prompt shows a provider prompt and waits for its Completed; a dismissed
// prompt is ErrDismissed, and ctx ending dismisses it.
func (c *Client) prompt(ctx context.Context, path dbus.ObjectPath) (dbus.Variant, error) {
	match := []dbus.MatchOption{dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(ifacePrompt), dbus.WithMatchMember("Completed")}
	if err := c.conn.AddMatchSignalContext(ctx, match...); err != nil {
		return dbus.Variant{}, fmt.Errorf("watch the prompt: %w", err)
	}
	defer func() { _ = c.conn.RemoveMatchSignal(match...) }()
	signals := make(chan *dbus.Signal, 8)
	c.conn.Signal(signals)
	defer c.conn.RemoveSignal(signals)
	obj := c.conn.Object(busName, path)
	if err := obj.CallWithContext(ctx, ifacePrompt+".Prompt", 0, "").Err; err != nil {
		return dbus.Variant{}, fmt.Errorf("show the prompt: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			_ = obj.Call(ifacePrompt+".Dismiss", 0).Err
			return dbus.Variant{}, ctx.Err()
		case sig := <-signals:
			if sig.Path != path || sig.Name != ifacePrompt+".Completed" || len(sig.Body) != 2 {
				continue
			}
			if dismissed, _ := sig.Body[0].(bool); dismissed {
				return dbus.Variant{}, ErrDismissed
			}
			result, _ := sig.Body[1].(dbus.Variant)
			return result, nil
		}
	}
}
