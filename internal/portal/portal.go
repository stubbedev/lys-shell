// Package portal is the xdg-desktop-portal client side lys-shell needs: the
// Request/Response handshake and the FileChooser.
package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/stubbedev/lys-lib/dbusx"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"
)

// Portal bus identity.
const (
	BusName       = "org.freedesktop.portal.Desktop"
	ObjectPath    = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	RequestIface  = "org.freedesktop.portal.Request"
	requestPrefix = "/org/freedesktop/portal/desktop/request/"
)

// Response codes of org.freedesktop.portal.Request.Response.
const (
	ResponseSuccess   uint32 = 0
	ResponseCancelled uint32 = 1
	ResponseOther     uint32 = 2
)

// ErrCancelled is a request the user dismissed.
var ErrCancelled = errors.New("portal: request cancelled")

// token returns a fresh handle_token: the spec allows [A-Za-z0-9_].
func token() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "lys_shell_" + hex.EncodeToString(b[:])
}

// requestPath is where the portal will put the Request object for a
// handle_token: the sender's unique name, ':' dropped and '.' as '_'.
func requestPath(conn *dbus.Conn, handle string) dbus.ObjectPath {
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	return dbus.ObjectPath(requestPrefix + sender + "/" + handle)
}

// response is what a Request's Response carries.
type response struct {
	Code    uint32                  `dbus:"response"`
	Results map[string]dbus.Variant `dbus:"results"`
}

var responded = dbusx.NewSignal[response](RequestIface, "Response")

// call runs one request-style portal method: it watches for the Response
// signal on the predicted Request path before calling (the spec's
// race-free order), then waits for the response's results. A cancelled
// or failed response, or one that does not read as a Response, is an
// error; ctx cancellation closes the request.
func call(ctx context.Context, conn *dbus.Conn, method string, options map[string]dbus.Variant, args ...any) (map[string]dbus.Variant, error) {
	handle := token()
	options["handle_token"] = dbus.MakeVariant(handle)
	path := requestPath(conn, handle)
	responses, unreadable := make(chan response, 1), make(chan error, 1)
	var watches []*dbusx.Watch
	defer func() {
		for _, watch := range watches {
			_ = watch.Stop()
		}
	}()
	watch := func(at dbus.ObjectPath) error {
		w, err := dbusx.WatchSignals(ctx, conn, dbusx.FromOwner(BusName),
			func(err error) {
				select {
				case unreadable <- err:
				default:
				}
			},
			dbusx.On(responded, at, func(r response, _ dbus.Sequence) error {
				select {
				case responses <- r:
				default:
				}
				return nil
			}))
		if err != nil {
			return fmt.Errorf("portal: %s: %w", method, err)
		}
		watches = append(watches, w)
		return nil
	}
	if err := watch(path); err != nil {
		return nil, err
	}

	var got dbus.ObjectPath
	callArgs := append(args, options)
	if err := conn.Object(BusName, ObjectPath).CallWithContext(ctx, method, 0, callArgs...).Store(&got); err != nil {
		return nil, fmt.Errorf("portal: %s: %w", method, err)
	}
	if got != path {
		// An old portal that ignores handle_token: follow its path.
		path = got
		if err := watch(path); err != nil {
			return nil, err
		}
	}
	select {
	case <-ctx.Done():
		_ = conn.Object(BusName, path).Call(RequestIface+".Close", dbus.FlagNoReplyExpected).Err
		return nil, ctx.Err()
	case err := <-unreadable:
		return nil, fmt.Errorf("portal: %s: %w", method, err)
	case r := <-responses:
		switch r.Code {
		case ResponseSuccess:
			return r.Results, nil
		case ResponseCancelled:
			return nil, fmt.Errorf("%s: %w", method, ErrCancelled)
		}
		return nil, fmt.Errorf("portal: %s: request failed (response %d)", method, r.Code)
	}
}
