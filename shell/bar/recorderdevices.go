package bar

import (
	"github.com/stubbedev/lys-desktop/service/pulse"
	"github.com/stubbedev/lys-desktop/service/recorder/devicechoice"
)

// microphoneSources is microphone_sources over the in-process audio
// service.
func microphoneSources(src pulse.Source) []devicechoice.DeviceChoice {
	if src == nil {
		return devicechoice.MicrophoneChoices(nil)
	}
	devs := src.InputDevices()
	sources := make([]devicechoice.Source, len(devs))
	for i, d := range devs {
		sources[i] = devicechoice.Source{Name: d.Name, Description: d.Description, Monitor: d.IsMonitor()}
	}
	return devicechoice.MicrophoneChoices(sources)
}

// recorderCameras lists the system's cameras; tests substitute it.
var recorderCameras = devicechoice.Cameras
