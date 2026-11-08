// Package privacy follows what records the user: the applications
// capturing a microphone, from the audio server's record streams, and
// the processes holding a camera open, from inotify on the video
// devices and one /proc scan per open or close.
package privacy

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/stubbedev/lys-desktop/feed"
	"github.com/stubbedev/lys-desktop/fswatch"
	"github.com/stubbedev/lys-desktop/service/pulse"
	"golang.org/x/sys/unix"
)

// MicUser is one application recording a microphone.
type MicUser struct {
	Stream pulse.StreamKey
	// Name is the application's name, else its binary, else the stream's.
	Name   string
	Icon   string
	Muted  bool
	Source string
}

// peakDetectName is the stream name level meters (pavucontrol and its
// kin) give the streams that only read a source's peak.
const peakDetectName = "Peak detect"

// MicUsers are the record streams that capture a microphone: running,
// attached to a source that is not a sink's monitor, not a level
// meter's peak stream, and not opened by the process self (lys-shell's
// own recorder and visualizer).
func MicUsers(streams []pulse.AudioStream, sources []pulse.InputDevice, self uint32) []MicUser {
	byIndex := make(map[uint32]pulse.InputDevice, len(sources))
	for _, s := range sources {
		byIndex[s.Key.Index] = s
	}
	var out []MicUser
	for _, st := range streams {
		src, ok := byIndex[st.DeviceIndex]
		switch {
		case st.Key.Type != pulse.StreamRecord, st.Corked, !ok, src.IsMonitor(),
			st.Name == peakDetectName, self != 0 && st.PID == self:
			continue
		}
		name := cmp.Or(st.ApplicationName, st.Binary, st.Name)
		out = append(out, MicUser{Stream: st.Key, Name: name, Icon: st.Properties["application.icon_name"], Muted: st.Muted, Source: cmp.Or(src.Description, src.Name)})
	}
	slices.SortStableFunc(out, func(a, b MicUser) int { return cmp.Compare(a.Stream.Index, b.Stream.Index) })
	return out
}

// CameraUser is one process holding a camera open.
type CameraUser struct {
	PID    int
	Name   string
	Device string
}

// ScanCameras lists the processes of uid under procRoot (/proc) with
// one of devices open, by reading their file descriptor links; the
// processes it may not read are someone else's and are skipped.
func ScanCameras(procRoot string, uid int, devices []string) ([]CameraUser, error) {
	if len(devices) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("privacy: listing %s: %w", procRoot, err)
	}
	var out []CameraUser
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		dir := filepath.Join(procRoot, e.Name())
		info, err := os.Stat(dir)
		if err != nil {
			continue
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
			continue
		}
		device, ok := holding(filepath.Join(dir, "fd"), devices)
		if !ok {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(dir, "comm")) //nolint:gosec // a /proc entry of the user's own process
		if err != nil {
			continue
		}
		out = append(out, CameraUser{PID: pid, Name: strings.TrimSpace(string(comm)), Device: device})
	}
	slices.SortFunc(out, func(a, b CameraUser) int { return cmp.Compare(a.PID, b.PID) })
	return out, nil
}

// holding reports the first of devices a process's fd directory links
// to.
func holding(fdDir string, devices []string) (string, bool) {
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		return "", false
	}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
		if err != nil {
			continue
		}
		if slices.Contains(devices, target) {
			return target, true
		}
	}
	return "", false
}

// videoDevices lists the video4linux nodes in devDir.
func videoDevices(devDir string) ([]string, error) {
	entries, err := os.ReadDir(devDir)
	if err != nil {
		return nil, fmt.Errorf("privacy: listing %s: %w", devDir, err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "video") {
			out = append(out, filepath.Join(devDir, e.Name()))
		}
	}
	return out, nil
}

// Cameras follows which processes hold a camera open. Nothing runs
// between events: inotify reports opens and closes of the video nodes
// and nodes appearing in the device directory, and each report rescans
// /proc once.
type Cameras struct {
	devDir, procRoot string
	uid              int
	dir, nodes       *fswatch.Watcher
	changes          feed.Tick
	done             chan struct{}
	wg               sync.WaitGroup

	mu      sync.Mutex
	users   []CameraUser
	watched []string
	scans   int
}

// nodeEvents are what a camera node reports: an open and each close.
const nodeEvents = unix.IN_OPEN | unix.IN_CLOSE_WRITE | unix.IN_CLOSE_NOWRITE

// dirEvents are the device directory gaining or losing a node.
const dirEvents = unix.IN_CREATE | unix.IN_DELETE

// WatchCameras starts following the cameras of devDir (/dev) held by
// uid's processes under procRoot (/proc).
func WatchCameras(devDir, procRoot string, uid int) (*Cameras, error) {
	dir, err := fswatch.New(dirEvents, false)
	if err != nil {
		return nil, fmt.Errorf("privacy: watching %s: %w", devDir, err)
	}
	nodes, err := fswatch.New(nodeEvents, false)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("privacy: watching the camera nodes: %w", err), dir.Close())
	}
	c := &Cameras{devDir: devDir, procRoot: procRoot, uid: uid, dir: dir, nodes: nodes, done: make(chan struct{})}
	if err := dir.Add(devDir); err != nil {
		return nil, errors.Join(fmt.Errorf("privacy: watching %s: %w", devDir, err), c.Close())
	}
	if err := c.rescan(); err != nil {
		return nil, errors.Join(err, c.Close())
	}
	c.wg.Go(c.loop)
	return c, nil
}

func (c *Cameras) loop() {
	for {
		select {
		case <-c.done:
			return
		case <-c.dir.Changed():
			c.dir.Take()
		case <-c.nodes.Changed():
			c.nodes.Take()
		}
		if err := c.rescan(); err != nil {
			log.Printf("privacy: %v", err)
		}
	}
}

// rescan watches every video node there is now and lists who holds one.
func (c *Cameras) rescan() error {
	devices, err := videoDevices(c.devDir)
	if err != nil {
		return err
	}
	for _, d := range devices {
		c.mu.Lock()
		known := slices.Contains(c.watched, d)
		c.mu.Unlock()
		if known {
			continue
		}
		if err := c.nodes.Add(d); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("privacy: watching %s: %w", d, err)
		}
		c.mu.Lock()
		c.watched = append(c.watched, d)
		c.mu.Unlock()
	}
	users, err := ScanCameras(c.procRoot, c.uid, devices)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.scans++
	changed := !slices.Equal(users, c.users)
	c.users = users
	c.mu.Unlock()
	if changed {
		feed.Notify(&c.changes)
	}
	return nil
}

// Users are the processes holding a camera now.
func (c *Cameras) Users() []CameraUser {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.users)
}

// Subscribe ticks when Users changes.
func (c *Cameras) Subscribe() (<-chan struct{}, func()) { return c.changes.Subscribe() }

// Close stops following the cameras.
func (c *Cameras) Close() error {
	select {
	case <-c.done:
	default:
		close(c.done)
	}
	c.wg.Wait()
	return errors.Join(c.dir.Close(), c.nodes.Close())
}
