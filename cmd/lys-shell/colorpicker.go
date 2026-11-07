package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/stubbedev/lys-lib/portaldialogs"
	dbus "github.com/stubbedev/lys-lib/third_party/godbus"

	"github.com/stubbedev/lys-shell/config"
	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/internal/dbuscli"
	"github.com/stubbedev/lys-shell/shell/screenshot"
)

// colorpickerCommand asks the running shell for a color pick over its
// own overlay, so it makes no Wayland connection of its own, and prints
// the color in the format the picker showed at the click.
func colorpickerCommand() *cli.Command {
	formats := make([]cli.PossibleValue, len(config.ColorFormats))
	for i, f := range config.ColorFormats {
		formats[i] = cli.PossibleValue{Name: string(f)}
	}
	return &cli.Command{
		Name:  "colorpicker",
		About: "Pick a color on screen, print it and copy it; exit status 1 when cancelled",
		Args: []*cli.Arg{
			{ID: "format", Short: 'f', Long: "format", Value: cli.Enum(formats...), Help: "Format the readout starts in (Tab in the picker cycles it) [default: colorpicker.format, or the last one used]"},
			{ID: "no_copy", Long: "no-copy", Help: "Print the color without copying it"},
			{ID: "no_loupe", Long: "no-loupe", Help: "Show a crosshair instead of the magnifier"},
		},
		Run: pickColor,
	}
}

func pickColor(m *cli.Matches) error {
	var format config.ColorFormat
	if name, ok := cli.Value[string](m, "format"); ok {
		f, err := config.ParseColorFormat(name)
		if err != nil {
			return err
		}
		format = f
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("Failed to connect to D-Bus session bus: %w", err) //nolint:staticcheck // the CLI's message, as screenshot's
	}
	defer func() { _ = conn.Close() }()
	text, picked, err := screenshot.NewClient(conn).PickColorText(context.Background(), portaldialogs.Caller{}, format, m.Flag("no_loupe"), !m.Flag("no_copy"))
	if errors.Is(err, screenshot.ErrBusy) {
		return dbuscli.Message("Color not picked: " + err.Error())
	}
	if err != nil {
		return dbuscli.FormatError("Screenshot", "pick color", err)
	}
	if !picked {
		return cli.ExitError{Code: 1}
	}
	_, _ = fmt.Fprintln(m.Stdout(), text)
	return nil
}
