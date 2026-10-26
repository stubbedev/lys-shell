package recorder

import (
	"image"

	"github.com/stubbedev/lys-shell/config"
)

// Webcam is the picture-in-picture overlay.
type Webcam struct {
	// Device is the V4L2 node; "" auto-selects the first camera.
	Device string
	// X and Y place the frame in the free space, 0-100.
	X, Y uint32
	// Size is the frame width as a percentage of the recording, 1-100.
	Size uint32
}

// Options is one recording's settings.
type Options struct {
	OutputPath string
	Format     config.RecorderFormat
	Framerate  uint32
	ShowCursor bool
	// Microphone captures MicrophoneDevice ("" the default source);
	// SystemAudio the default sink's monitor.
	Microphone       bool
	MicrophoneDevice string
	SystemAudio      bool
	// Webcam is the overlay, nil for none.
	Webcam *Webcam
}

// The webcam frame's geometry: a 16:9 frame no smaller than the
// minimum, inset from the screen's edges by a margin.
const (
	edgeMarginPct = 5
	webcamMinW    = 80
	webcamMinH    = 60
	webcamAspectW = 16
	webcamAspectH = 9
)

// WebcamMargin is the uniform inset, a percentage of the shorter side.
// The dropdown's position preview uses it too.
func WebcamMargin(w, h int32) int32 { return min(w, h) * edgeMarginPct / 100 }

// webcamRect is where cam's frame sits on a screenW x screenH
// recording: Size percent of the width, 16:9, at X and Y percent of the
// travel inside the margin.
func webcamRect(cam Webcam, screenW, screenH int32) image.Rectangle {
	camW := max(int32(float64(screenW)*float64(min(max(cam.Size, 1), 100))/100), webcamMinW)
	camH := max(camW*webcamAspectH/webcamAspectW, webcamMinH)
	margin := WebcamMargin(screenW, screenH)
	travelW := max(screenW-camW-2*margin, 0)
	travelH := max(screenH-camH-2*margin, 0)
	x := margin + travelW*int32(min(cam.X, 100))/100
	y := margin + travelH*int32(min(cam.Y, 100))/100
	return image.Rect(int(x), int(y), int(x+camW), int(y+camH))
}
