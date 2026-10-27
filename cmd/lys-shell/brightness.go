package main

import (
	"fmt"
	"math"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/service/brightness"
)

func brightnessCommand() *cli.Command {
	return &cli.Command{
		Name:  "brightness",
		About: "Set an output's brightness through its backlight or DDC/CI",
		Args: []*cli.Arg{
			{
				ID: "change", ValueName: "CHANGE", Required: true, AllowHyphenValues: true,
				Value: cli.Func(brightness.ParseChange),
				Help:  "Percent: N sets, +N raises, -N lowers; never below modules.brightness.min-brightness",
			},
			{ID: "output", Short: 'o', Long: "output", ValueName: "NAME", Value: cli.String, Help: "Output connector name (e.g. DP-1); the focused output when omitted"},
		},
		Run: withDaemon("Brightness", brightness.ServiceName, brightness.ServicePath, brightness.ServiceName, setBrightness),
	}
}

func setBrightness(m *cli.Matches, p *daemonProxy) error {
	change, _ := cli.Value[brightness.Change](m, "change")
	output, _ := cli.Value[string](m, "output")
	var name string
	var level float64
	if err := p.call("set brightness", "Apply", []any{&name, &level}, output, change.IsRelative(), change.Percent()); err != nil {
		return err
	}
	fmt.Fprintf(m.Stdout(), "%s: %d%%\n", name, int64(math.Round(level)))
	return nil
}
