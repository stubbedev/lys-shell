package portal

import (
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/stubbedev/lys-shell/internal/dbusx"
	"github.com/stubbedev/lys-shell/internal/fileuri"
)

// email is org.freedesktop.impl.portal.Email: the user's mail client
// opens a pre-filled compose window, through `xdg-email --attach` when
// there are attachments and a mailto: URI handed to xdg-open otherwise.
// The frontend turns the app's attachment_fds into the attachments
// list of paths it passes here; a mail client that cannot take them is
// a failure, never a compose window silently missing the files.
type email struct {
	conn  *dbus.Conn
	spawn func(argv, env []string) error
}

func emailIface(conn *dbus.Conn, spawn func(argv, env []string) error) dbusx.Interface {
	return dbusx.Interface{
		Name:    "org.freedesktop.impl.portal.Email",
		Methods: email{conn, spawn},
	}
}

// ComposeEmail opens the compose window.
func (e email) ComposeEmail(handle dbus.ObjectPath, appID, parent string, options Vardict) (uint32, Vardict, *dbus.Error) {
	c, ok := beginOr(e.conn, handle, appID, parent, nil)
	if !ok {
		return ResponseOther, Vardict{}, nil
	}
	defer c.end()
	var env []string
	if token, ok := optString(options, "activation_token"); ok && token != "" {
		env = []string{"XDG_ACTIVATION_TOKEN=" + token}
	}
	argv := []string{"xdg-open", mailto(options)}
	if attachments := attachmentPaths(options); len(attachments) > 0 {
		argv = xdgEmailArgv(options, attachments)
	}
	if err := e.spawn(argv, env); err != nil {
		warnf("email: cannot launch %s: %v", argv[0], err)
		return ResponseOther, Vardict{}, nil
	}
	return ResponseSuccess, Vardict{}, nil
}

// attachmentPaths reads the attachments option: paths, or file:// URIs
// as the interface describes them.
func attachmentPaths(options Vardict) []string {
	list := stringList(options, "attachments")
	paths := make([]string, 0, len(list))
	for _, a := range list {
		if path, ok := fileuri.Path(a); ok {
			a = path
		}
		paths = append(paths, a)
	}
	return paths
}

// recipients is the addresses list plus the single address.
func recipients(options Vardict) []string {
	to := stringList(options, "addresses")
	if s, ok := optString(options, "address"); ok {
		to = append(to, s)
	}
	return to
}

func xdgEmailArgv(options Vardict, attachments []string) []string {
	argv := []string{"xdg-email", "--utf8"}
	if s, ok := optString(options, "subject"); ok {
		argv = append(argv, "--subject", s)
	}
	if s, ok := optString(options, "body"); ok {
		argv = append(argv, "--body", s)
	}
	for _, cc := range stringList(options, "cc") {
		argv = append(argv, "--cc", cc)
	}
	for _, bcc := range stringList(options, "bcc") {
		argv = append(argv, "--bcc", bcc)
	}
	for _, path := range attachments {
		argv = append(argv, "--attach", path)
	}
	return append(argv, recipients(options)...)
}

// mailto builds the mailto: URI: the recipients, then cc, bcc, subject
// and body as query parameters.
func mailto(options Vardict) string {
	uri := "mailto:" + strings.Join(recipients(options), ",")
	var params []string
	for _, key := range []string{"cc", "bcc"} {
		if list := stringList(options, key); len(list) > 0 {
			params = append(params, key+"="+fileuri.Encode(strings.Join(list, ","), ""))
		}
	}
	for _, key := range []string{"subject", "body"} {
		if s, ok := optString(options, key); ok {
			params = append(params, key+"="+fileuri.Encode(s, ""))
		}
	}
	if len(params) > 0 {
		uri += "?" + strings.Join(params, "&")
	}
	return uri
}

// stringList reads an `as` option.
func stringList(options Vardict, key string) []string {
	list, _ := options[key].Value().([]string)
	return list
}
