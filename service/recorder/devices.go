package recorder

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/stubbedev/lys-lib/v4l2"
)

// DeviceChoice is DeviceChoice: ID is what the recorder consumes (""
// is the default or automatic entry), Label what a picker shows.
type DeviceChoice struct {
	ID, Label string
}

// Source is an audio input as the microphone pickers see it.
type Source struct {
	Name, Description string
	// Monitor is a sink's monitor, never a microphone.
	Monitor bool
}

// MicrophoneChoices is microphone_sources: "Default", then every
// named non-monitor source by description (else name).
func MicrophoneChoices(sources []Source) []DeviceChoice {
	choices := []DeviceChoice{{"", "Default"}}
	for _, s := range sources {
		if s.Name == "" || s.Monitor {
			continue
		}
		label := s.Description
		if label == "" {
			label = s.Name
		}
		choices = append(choices, DeviceChoice{s.Name, label})
	}
	return choices
}

// Cameras is cameras(): "Automatic", then one entry per distinct card
// name at its lowest capture node.
func Cameras() []DeviceChoice {
	return cameras("/sys/class/video4linux", "/dev", v4l2.CaptureCard)
}

// cameras lists the nodes from sysDir (/sys/class/video4linux) and
// probes them in devDir (/dev).
func cameras(sysDir, devDir string, probe func(path string) (string, bool)) []DeviceChoice {
	choices := []DeviceChoice{{"", "Automatic"}}
	entries, err := os.ReadDir(sysDir)
	if err != nil {
		return choices
	}
	var nodes []int
	for _, e := range entries {
		if n, err := strconv.Atoi(strings.TrimPrefix(e.Name(), "video")); err == nil && strings.HasPrefix(e.Name(), "video") {
			nodes = append(nodes, n)
		}
	}
	slices.Sort(nodes)
	seen := map[string]bool{}
	for _, n := range nodes {
		path := filepath.Join(devDir, "video"+strconv.Itoa(n))
		card, ok := probe(path)
		if !ok || seen[card] {
			continue
		}
		seen[card] = true
		choices = append(choices, DeviceChoice{path, card})
	}
	return choices
}

// ChoiceIndex is index_of: the saved id's index, else the default 0.
func ChoiceIndex(choices []DeviceChoice, id string) int {
	return max(slices.IndexFunc(choices, func(c DeviceChoice) bool { return c.ID == id }), 0)
}

// ChoiceLabels are the picker rows.
func ChoiceLabels(choices []DeviceChoice) []string {
	labels := make([]string, len(choices))
	for i, c := range choices {
		labels[i] = c.Label
	}
	return labels
}

// WithSaved keeps a saved device the list does not offer (unplugged,
// or not enumerated yet) selectable: it is appended, labelled by its
// id. The default "" needs no entry.
func WithSaved(choices []DeviceChoice, id string) []DeviceChoice {
	if id == "" || slices.ContainsFunc(choices, func(c DeviceChoice) bool { return c.ID == id }) {
		return choices
	}
	return append(slices.Clip(choices), DeviceChoice{id, id})
}

// webcamMaxFPS caps the overlay's updates: a webcam picture needs no
// more, and each one is a conversion and an upload.
const webcamMaxFPS = 30

// openWebcam streams device, "" the first camera, letterboxed to frame.
func openWebcam(device string, frame image.Point) (*v4l2.Camera, error) {
	if device == "" {
		choices := Cameras()
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
