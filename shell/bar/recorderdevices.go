package bar

import (
	"github.com/stubbedev/lys-shell/service/pulse"
	"github.com/stubbedev/lys-shell/service/recorder"
)

// microphoneSources is microphone_sources over the in-process audio
// service.
func microphoneSources(src pulse.Source) []recorder.DeviceChoice {
	if src == nil {
		return recorder.MicrophoneChoices(nil)
	}
	devs := src.InputDevices()
	sources := make([]recorder.Source, len(devs))
	for i, d := range devs {
		sources[i] = recorder.Source{Name: d.Name, Description: d.Description, Monitor: d.IsMonitor()}
	}
	return recorder.MicrophoneChoices(sources)
}

// recorderCameras lists the system's cameras; tests substitute it.
var recorderCameras = recorder.Cameras
