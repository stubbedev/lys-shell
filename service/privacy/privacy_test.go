package privacy

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stubbedev/lys-desktop/service/pulse"
)

func source(index uint32, name string, monitor bool) pulse.InputDevice {
	var d pulse.InputDevice
	d.Key, d.Name, d.Description = pulse.DeviceKey{Index: index, Type: pulse.DeviceInput}, name, name
	if monitor {
		d.MonitorOfSink = pulse.IndexOf(1)
	}
	return d
}

func record(index, device, pid uint32, app string) pulse.AudioStream {
	return pulse.AudioStream{Key: pulse.StreamKey{Index: index, Type: pulse.StreamRecord}, DeviceIndex: device, PID: pid, ApplicationName: app, Name: "capture"}
}

func TestMicUsersAreTheAppsRecordingAMicrophone(t *testing.T) {
	sources := []pulse.InputDevice{source(10, "Built-in Microphone", false), source(11, "Monitor of Speakers", true)}
	meter := record(5, 10, 300, "pavucontrol")
	meter.Name = peakDetectName
	paused := record(6, 10, 400, "Recorder")
	paused.Corked = true
	streams := []pulse.AudioStream{
		record(3, 10, 100, "Firefox"),
		record(2, 11, 200, "OBS"),
		meter,
		paused,
		record(7, 10, 999, "lys-shell"),
		{Key: pulse.StreamKey{Index: 8, Type: pulse.StreamPlayback}, DeviceIndex: 10, ApplicationName: "Music"},
		{Key: pulse.StreamKey{Index: 1, Type: pulse.StreamRecord}, DeviceIndex: 10, Binary: "arecord", Muted: true},
	}
	got := MicUsers(streams, sources, 999)
	var names []string
	for _, u := range got {
		names = append(names, u.Name)
	}
	if !slices.Equal(names, []string{"arecord", "Firefox"}) {
		t.Fatalf("mic users %v, want arecord and Firefox alone", names)
	}
	if !got[0].Muted || got[1].Source != "Built-in Microphone" {
		t.Errorf("users %+v", got)
	}
	if MicUsers(streams[1:2], sources, 999) != nil {
		t.Error("a monitor recording counts as the microphone")
	}
}

func TestCamerasFollowOpensAndClosesOfTheVideoNodes(t *testing.T) {
	dev := t.TempDir()
	node := filepath.Join(dev, "video0")
	if err := os.WriteFile(node, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dev, "null"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := WatchCameras(dev, "/proc", os.Getuid())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ticks, stop := c.Subscribe()
	defer stop()
	scans := func() int {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.scans
	}
	idle := scans()
	time.Sleep(100 * time.Millisecond)
	if got := scans(); got != idle {
		t.Fatalf("%d /proc scans with nothing opened, want none", got-idle)
	}
	f, err := os.Open(node)
	if err != nil {
		t.Fatal(err)
	}
	await(t, ticks, "the open")
	users := c.Users()
	if len(users) != 1 || users[0].PID != os.Getpid() || users[0].Device != node || users[0].Name == "" {
		t.Fatalf("users %+v, want this process on %s", users, node)
	}
	f.Close()
	await(t, ticks, "the close")
	if users := c.Users(); len(users) != 0 {
		t.Errorf("users after the close %+v", users)
	}
	other, err := os.Open(filepath.Join(dev, "null"))
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	before := scans()
	time.Sleep(100 * time.Millisecond)
	if got := scans(); got != before {
		t.Errorf("opening another device scanned /proc %d times", got-before)
	}
	second := filepath.Join(dev, "video1")
	if err := os.WriteFile(second, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		c.mu.Lock()
		watched := slices.Contains(c.watched, second)
		c.mu.Unlock()
		if watched {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a new video node is not watched")
		}
	}
	g, err := os.Open(second)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	await(t, ticks, "the open of the new node")
	if users := c.Users(); len(users) != 1 || users[0].Device != second {
		t.Errorf("users %+v, want this process on the new node", users)
	}
}

func TestScanCamerasSkipsOtherUsersAndNoDevices(t *testing.T) {
	if users, err := ScanCameras("/proc", os.Getuid(), nil); err != nil || users != nil {
		t.Errorf("no devices: %v, %v", users, err)
	}
	dev := filepath.Join(t.TempDir(), "video0")
	if err := os.WriteFile(dev, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dev)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if users, err := ScanCameras("/proc", os.Getuid()+1, []string{dev}); err != nil || len(users) != 0 {
		t.Errorf("another uid's view: %v, %v", users, err)
	}
	if _, err := ScanCameras(filepath.Join(t.TempDir(), "missing"), 0, []string{dev}); err == nil {
		t.Error("a missing /proc is no error")
	}
}

func await(t *testing.T, ticks <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Fatalf("no tick for %s", what)
	}
}
