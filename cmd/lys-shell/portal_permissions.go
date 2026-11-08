package main

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/stubbedev/lys-desktop/portal/permissions"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/internal/cli"
)

// portalPermissionsCommand is `lys-shell portal permissions`: what apps
// were granted through the portal, and taking it back.
func portalPermissionsCommand() *cli.Command {
	tables := make([]cli.PossibleValue, len(permissions.Tables))
	for i, t := range permissions.Tables {
		tables[i] = cli.PossibleValue{Name: string(t)}
	}
	return &cli.Command{
		Name:  "permissions",
		About: "List and revoke what apps were granted through the portal (screen-share tokens, devices, background, shortcuts)",
		Subcommands: []*cli.Command{
			{Name: "list", About: "List every app's grants", Run: withPermissions(permissionsList)},
			{
				Name:  "revoke",
				About: "Revoke an app's grants in a table, or one entry of it",
				Args: []*cli.Arg{
					{ID: "app", Required: true, Help: "App id (e.g., org.mozilla.firefox)"},
					{ID: "table", Required: true, Value: cli.Enum(tables...), Help: "Table the grants are kept in"},
					{ID: "entry", Help: "Entry of the table to revoke (e.g., camera); every entry when left out"},
				},
				Run: withPermissions(permissionsRevoke),
			},
		},
	}
}

// withPermissions runs a handler over the session bus's permission
// store and lys's shortcut store.
func withPermissions(run func(context.Context, *cli.Matches, permissions.Store) error) func(*cli.Matches) error {
	return func(m *cli.Matches) error {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return cliMessage("Failed to connect to D-Bus session bus: " + err.Error())
		}
		defer func() { _ = conn.Close() }()
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return run(ctx, m, permissions.Store{Conn: conn, ShortcutsPath: permissions.ShortcutsPath()})
	}
}

func permissionsList(ctx context.Context, m *cli.Matches, s permissions.Store) error {
	apps, err := s.List(ctx)
	if err != nil {
		return cliMessage("Failed to list permissions: " + err.Error())
	}
	if len(apps) == 0 {
		_, _ = fmt.Fprintln(m.Stdout(), "No app was granted anything")
		return nil
	}
	w := tabwriter.NewWriter(m.Stdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "APP\tTABLE\tENTRY\tGRANTED")
	for _, a := range apps {
		for _, g := range a.Grants {
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, g.Table, g.ID, g.Value)
		}
	}
	return w.Flush()
}

func permissionsRevoke(ctx context.Context, m *cli.Matches, s permissions.Store) error {
	app, _ := cli.Value[string](m, "app")
	table, _ := cli.Value[string](m, "table")
	entry, _ := cli.Value[string](m, "entry")
	n, err := s.Revoke(ctx, app, permissions.Table(table), entry)
	if err != nil {
		return cliMessage(err.Error())
	}
	noun := "grants"
	if n == 1 {
		noun = "grant"
	}
	_, _ = fmt.Fprintf(m.Stdout(), "Revoked %d %s of %s in %s\n", n, noun, app, table)
	return nil
}
