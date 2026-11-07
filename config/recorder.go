package config

import "time"

// RecorderConfig is ported from crates/wayle-config/src/schemas/modules/recorder/mod.rs.
//
// Native screen recorder encoding on the GPU's video encoder.
//
// An output is captured into GPU memory and encoded to H.264 there,
// through Vulkan Video or the GPU's VA-API driver, with Opus audio; a
// GPU without a video encoder cannot record.
//
// Click the bar button to start/stop; the dropdown exposes the recording
// options below. Controllable from the CLI / RPC socket:
// `lys-shell recorder start|stop|toggle|pause|status`.
type RecorderConfig struct {
	// Icon when idle (not recording).
	IconIdle string `cfg:"icon-idle"`
	// Icon while recording.
	IconRecording string `cfg:"icon-recording"`
	// Icon while recording is paused.
	IconPaused string `cfg:"icon-paused"`
	// Icon while only the instant replay is armed.
	IconReplay string `cfg:"icon-replay"`
	// Format string for the label.
	Format string `cfg:"format"`
	// Capture the microphone in the recording.
	Microphone bool `cfg:"microphone"`
	// Microphone PipeWire/PulseAudio source name. Empty uses the default source.
	MicrophoneDevice string `cfg:"microphone-device"`
	// Capture desktop (system) audio in the recording.
	SystemAudio bool `cfg:"system-audio"`
	// Capture framerate in frames per second.
	Framerate uint32 `cfg:"framerate"`
	// Overlay a webcam picture-in-picture frame into the recording.
	WebcamEnabled bool `cfg:"webcam-enabled"`
	// Webcam V4L2 device path. Empty auto-selects the first camera.
	WebcamDevice string `cfg:"webcam-device"`
	// Webcam frame horizontal position, as a percentage of the free
	// horizontal space (0 = flush left, 100 = flush right). Stored relative so
	// it stays correct across monitors of different resolutions.
	WebcamX Percentage `cfg:"webcam-x"`
	// Webcam frame vertical position, as a percentage of the free vertical
	// space (0 = flush top, 100 = flush bottom). Stored relative so it stays
	// correct across monitors of different resolutions.
	WebcamY Percentage `cfg:"webcam-y"`
	// Webcam frame width as a percentage of the recording width.
	WebcamSize Percentage `cfg:"webcam-size"`
	// Output directory for recordings. Empty uses the XDG Videos directory.
	OutputDirectory string `cfg:"output-directory,type=Directory"`
	// Container format / codec preset.
	OutputFormat RecorderFormat `cfg:"output-format"`
	// Draw the mouse cursor in the recording.
	ShowCursor bool `cfg:"show-cursor"`
	// Delay between choosing the capture source and the recording actually
	// starting, in milliseconds. Gives on-screen UI (the start toast) time to
	// clear so it isn't captured in the video.
	StartDelayMs uint32 `cfg:"start-delay-ms"`
	// Instant replay: keep the last seconds of an output encoded in memory
	// and save them on demand.
	Replay RecorderReplayConfig `cfg:"replay"`
	// Display border around button.
	BorderShow bool `cfg:"border-show"`
	// Border color token.
	BorderColor ColorValue `cfg:"border-color"`
	// Display module icon.
	IconShow bool `cfg:"icon-show"`
	// Show the module's state in a tooltip on hover.
	Tooltip bool `cfg:"tooltip"`
	// Icon foreground color. Auto selects based on variant for contrast.
	IconColor ColorValue `cfg:"icon-color"`
	// Icon container background color token.
	IconBgColor ColorValue `cfg:"icon-bg-color"`
	// Display label.
	LabelShow bool `cfg:"label-show"`
	// Label text color token.
	LabelColor ColorValue `cfg:"label-color"`
	// Max label characters before truncation with ellipsis. Set to 0 to disable.
	LabelMaxLength uint32 `cfg:"label-max-length"`
	// Button background color token.
	ButtonBgColor ColorValue `cfg:"button-bg-color"`
	// Action on left click. Default toggles recording.
	LeftClick ClickAction `cfg:"left-click"`
	// Action on right click. Default opens the recorder dropdown.
	RightClick ClickAction `cfg:"right-click"`
	// Action on middle click.
	MiddleClick ClickAction `cfg:"middle-click"`
	// Action on scroll up.
	ScrollUp ClickAction `cfg:"scroll-up"`
	// Action on scroll down.
	ScrollDown ClickAction `cfg:"scroll-down"`
}

// DefaultsRecorder returns the schema defaults.
func DefaultsRecorder() RecorderConfig {
	return RecorderConfig{
		IconIdle:         "ld-video-symbolic",
		IconRecording:    "ld-circle-dot-symbolic",
		IconPaused:       "ld-circle-pause-symbolic",
		IconReplay:       "ld-history-symbolic",
		Format:           "{{ elapsed }}",
		Microphone:       false,
		MicrophoneDevice: "",
		SystemAudio:      true,
		Framerate:        60,
		WebcamEnabled:    false,
		WebcamDevice:     "",
		WebcamX:          100,
		WebcamY:          100,
		WebcamSize:       20,
		OutputDirectory:  "",
		OutputFormat:     RecorderFormatMkv,
		ShowCursor:       true,
		StartDelayMs:     1400,
		Replay:           DefaultsRecorderReplay(),
		BorderShow:       false,
		BorderColor:      mustColor("red"),
		IconShow:         true,
		Tooltip:          true,
		IconColor:        mustColor("auto"),
		IconBgColor:      mustColor("red"),
		LabelShow:        true,
		LabelColor:       mustColor("fg-default"),
		LabelMaxLength:   0,
		ButtonBgColor:    mustColor("bg-surface-elevated"),
		LeftClick:        ParseClickAction("lys-shell recorder toggle"),
		RightClick:       ParseClickAction("dropdown:recorder"),
		MiddleClick:      ClickAction{},
		ScrollUp:         ClickAction{},
		ScrollDown:       ClickAction{},
	}
}

// Clicks returns the five input bindings.
func (c RecorderConfig) Clicks() ClickConfig {
	return ClickConfig{c.LeftClick, c.RightClick, c.MiddleClick, c.ScrollUp, c.ScrollDown}
}

// RecorderFormat is ported from crates/wayle-config/src/schemas/modules/recorder/types.rs.
//
// Container format / codec preset for recordings.
type RecorderFormat string

// RecorderFormat values.
const (
	// H.264 in an MP4 container.
	RecorderFormatMp4 RecorderFormat = "mp4"
	// H.264 in a Matroska container.
	RecorderFormatMkv RecorderFormat = "mkv"
)

var _ = registerEnum(RecorderFormatMp4, RecorderFormatMkv)

// ReplayOutputFocused is the replay output that follows focus: the
// output focused when the replay is armed.
const ReplayOutputFocused = "focused"

// RecorderReplayConfig is modules.recorder.replay.
//
// The instant replay: while armed it captures an output and keeps the
// last `seconds` of encoded video and audio in memory, cut on keyframes,
// so `lys-shell recorder replay save` writes what just happened to a file
// in the output directory without stopping. Memory grows with the
// encoded bitrate times `seconds`, and the recorder dropdown shows it.
// Controllable from the CLI: `lys-shell recorder replay start|stop|save`.
type RecorderReplayConfig struct {
	// Arm the instant replay at start and whenever this is switched on.
	Enable bool `cfg:"enable"`
	// Seconds a save writes, plus at most one 2 s keyframe interval.
	Seconds ReplaySeconds `cfg:"seconds"`
	// Output captured: `focused` (when armed) or a name such as `DP-1`.
	Output string `cfg:"output"`
}

// DefaultsRecorderReplay returns the schema defaults.
func DefaultsRecorderReplay() RecorderReplayConfig {
	return RecorderReplayConfig{Enable: false, Seconds: 30, Output: ReplayOutputFocused}
}

// ReplaySeconds is the instant replay's length.
//
// Seconds of instant replay, clamped to 5-600.
type ReplaySeconds uint32

// UnmarshalConfig implements Unmarshaler.
func (s *ReplaySeconds) UnmarshalConfig(v any) error {
	raw, err := decodeClamped(v, ReplaySeconds(5), ReplaySeconds(600), "replay seconds")
	*s = raw
	return err
}

// MarshalConfig implements Marshaler.
func (s ReplaySeconds) MarshalConfig() any { return int64(s) }

func (ReplaySeconds) configSchema(*schemaGen) Schema {
	return rangedSchema(typeOf[ReplaySeconds](), int64(5), int64(600))
}

// Span is the replay's length as a duration.
func (s ReplaySeconds) Span() time.Duration { return time.Duration(s) * time.Second }
