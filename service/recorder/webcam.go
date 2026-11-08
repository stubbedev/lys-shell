package recorder

import (
	"errors"
	"fmt"
	"image"

	"github.com/stubbedev/lys-lib/v4l2"

	"github.com/stubbedev/lys-shell/service/recorder/devicechoice"
)

// webcamMaxFPS caps the overlay's updates: a webcam picture needs no
// more, and each one is a conversion and an upload.
const webcamMaxFPS = 30

// openWebcam streams device, "" the first camera, letterboxed to frame.
func openWebcam(device string, frame image.Point) (*v4l2.Camera, error) {
	if device == "" {
		choices := devicechoice.Cameras()
		if len(choices) < 2 {
			return nil, errors.New("recorder: the webcam overlay is on but no camera is connected")
		}
		device = choices[1].ID
	}
	cam, err := v4l2.Open(device, frame, webcamMaxFPS)
	if err != nil {
		return nil, fmt.Errorf("recorder: webcam: %w", err)
	}
	return cam, nil
}
