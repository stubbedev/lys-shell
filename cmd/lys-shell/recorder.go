package main

import (
	"fmt"

	"github.com/stubbedev/lys-shell/internal/cli"
	"github.com/stubbedev/lys-shell/service/recorder"
	"github.com/stubbedev/lys-shell/service/sysinfo"
)

// recorderCommand is lys-shell/src/cli/recorder/commands.rs.
func recorderCommand() *cli.Command {
	return &cli.Command{
		Name:  "recorder",
		About: "Screen recorder control commands",
		Subcommands: []*cli.Command{
			{Name: "start", About: "Start recording", Run: withRecorder(recorderAction("start recording", "Start", "Recording started"))},
			{Name: "stop", About: "Stop recording", Run: withRecorder(recorderAction("stop recording", "Stop", "Recording stopped"))},
			{Name: "toggle", About: "Toggle recording on/off", Run: withRecorder(recorderToggle)},
			{Name: "pause", About: "Pause the active recording", Run: withRecorder(recorderAction("pause recording", "Pause", "Paused"))},
			{Name: "resume", About: "Resume a paused recording", Run: withRecorder(recorderAction("resume recording", "Resume", "Resumed"))},
			{Name: "status", About: "Show current recorder status", Run: withRecorder(recorderStatus)},
			{
				Name:  "replay",
				About: "Instant replay: keep the last seconds in memory and save them on demand",
				Subcommands: []*cli.Command{
					{Name: "start", About: "Arm the instant replay", Run: withRecorder(recorderAction("arm the instant replay", "ReplayStart", "Instant replay armed"))},
					{Name: "stop", About: "Disarm the instant replay and free its memory", Run: withRecorder(recorderAction("disarm the instant replay", "ReplayStop", "Instant replay stopped"))},
					{Name: "save", About: "Save the last seconds to a file and print its path", Run: withRecorder(recorderReplaySave)},
				},
			},
		},
	}
}

func withRecorder(run func(*cli.Matches, *daemonProxy) error) func(*cli.Matches) error {
	return withDaemon("Recorder", recorder.ServiceName, recorder.ServicePath, recorder.ServiceName, run)
}

// recorderAction is start.rs / stop.rs / pause.rs / resume.rs.
func recorderAction(op, method, done string) func(*cli.Matches, *daemonProxy) error {
	return func(m *cli.Matches, p *daemonProxy) error {
		if err := p.call(op, method, nil); err != nil {
			return err
		}
		fmt.Fprintln(m.Stdout(), done)
		return nil
	}
}

func recorderToggle(m *cli.Matches, p *daemonProxy) error {
	if err := p.call("toggle recording", "Toggle", nil); err != nil {
		return err
	}
	var active bool
	_ = p.prop("get active state", "Active", &active)
	if active {
		fmt.Fprintln(m.Stdout(), "Recording started")
	} else {
		fmt.Fprintln(m.Stdout(), "Recording stopped")
	}
	return nil
}

func recorderStatus(m *cli.Matches, p *daemonProxy) error {
	var active bool
	if err := p.prop("get active state", "Active", &active); err != nil {
		return err
	}
	var replay string
	if err := p.prop("get the instant replay state", "Replay", &replay); err != nil {
		return err
	}
	if replay == recorder.ReplayArmed.String() {
		var held uint64
		if err := p.prop("get the instant replay memory", "ReplayBytes", &held); err != nil {
			return err
		}
		fmt.Fprintf(m.Stdout(), "Instant replay armed (%s)\n", sysinfo.AutoBytes(held))
	}
	if !active {
		fmt.Fprintln(m.Stdout(), "Idle")
		return nil
	}
	var paused bool
	var elapsed uint32
	var file string
	_ = p.prop("get paused state", "Paused", &paused)
	_ = p.prop("get elapsed", "Elapsed", &elapsed)
	_ = p.prop("get file", "File", &file)
	state := "Recording"
	if paused {
		state = "Paused"
	}
	if file == "" {
		fmt.Fprintf(m.Stdout(), "%s (%d:%02d)\n", state, elapsed/60, elapsed%60)
	} else {
		fmt.Fprintf(m.Stdout(), "%s (%d:%02d) -> %s\n", state, elapsed/60, elapsed%60, file)
	}
	return nil
}

func recorderReplaySave(m *cli.Matches, p *daemonProxy) error {
	var path string
	if err := p.call("save the instant replay", "ReplaySave", []any{&path}); err != nil {
		return err
	}
	fmt.Fprintln(m.Stdout(), path)
	return nil
}
