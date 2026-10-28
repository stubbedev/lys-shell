package filechooser

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/stubbedev/lys-lib/intconv"
	"github.com/stubbedev/lys-lib/xdg"
	"golang.org/x/sys/unix"

	"github.com/stubbedev/lys-shell/internal/fswatch"
)

// WatchMounts calls changed (off the loop) each time the mount table
// changes, until stop is called: the kernel flags /proc/self/mounts
// with POLLPRI on every mount and unmount, and reading it again rearms
// the flag. A table that cannot be watched watches nothing.
func WatchMounts(changed func()) (stop func()) {
	table, err := os.Open("/proc/self/mounts")
	if err != nil {
		log.Printf("file chooser: cannot follow the mounts: %v", err)
		return func() {}
	}
	quitR, quitW, err := os.Pipe()
	if err != nil {
		_ = table.Close()
		log.Printf("file chooser: cannot follow the mounts: %v", err)
		return func() {}
	}
	tableFd, errT := intconv.Exact[int32](table.Fd())
	quitFd, errQ := intconv.Exact[int32](quitR.Fd())
	if err := errors.Join(errT, errQ); err != nil {
		_ = table.Close()
		_ = quitR.Close()
		_ = quitW.Close()
		log.Printf("file chooser: cannot follow the mounts: %v", err)
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, table)
		fds := []unix.PollFd{
			{Fd: tableFd, Events: unix.POLLPRI | unix.POLLERR},
			{Fd: quitFd, Events: unix.POLLIN},
		}
		for {
			if _, err := unix.Poll(fds, -1); err != nil {
				if errors.Is(err, unix.EINTR) {
					continue
				}
				log.Printf("file chooser: following the mounts: %v", err)
				return
			}
			if fds[1].Revents != 0 {
				return
			}
			if fds[0].Revents&(unix.POLLPRI|unix.POLLERR) != 0 {
				if _, err := table.Seek(0, io.SeekStart); err != nil {
					log.Printf("file chooser: following the mounts: %v", err)
					return
				}
				_, _ = io.Copy(io.Discard, table)
				changed()
			}
		}
	}()
	return func() {
		_ = quitW.Close()
		<-done
		_ = quitR.Close()
		_ = table.Close()
	}
}

// SystemLocations reads the mount table for OtherLocations, then the
// gvfs network shares.
func SystemLocations(home string) func() []Place {
	return func() []Place {
		locations := OtherLocations(readMounts(), home, os.Getenv("USER"))
		return append(locations, NetworkLocations(os.Getenv("XDG_RUNTIME_DIR"))...)
	}
}

// SystemTrashDirs reads the data home and the mount table for
// TrashDirs: the home trash, then the user-visible mounts' trashes.
func SystemTrashDirs(home string) func() []string {
	return func() []string {
		data, _ := xdg.OS.DataHome()
		return TrashDirs(data, VisibleMounts(readMounts(), home, os.Getenv("USER")), os.Getuid())
	}
}

// readMounts is the mount table, nil when it cannot be read.
func readMounts() io.Reader {
	data, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		log.Printf("file chooser: cannot read the mounts: %v", err)
		return nil
	}
	return bytes.NewReader(data)
}

// WatchSystemLocations calls changed (off the loop) whenever
// SystemLocations may have changed, until stop is called: a mount came
// or went (WatchMounts), or gvfs mounted or unmounted a share under
// $XDG_RUNTIME_DIR/gvfs, which the mount table does not show.
func WatchSystemLocations(changed func()) (stop func()) {
	stopMounts := WatchMounts(changed)
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		return stopMounts
	}
	w, err := fswatch.New(fswatch.Changes, false)
	if err != nil {
		log.Printf("file chooser: cannot follow the network shares: %v", err)
		return stopMounts
	}
	if err := w.Add(filepath.Join(runtime, "gvfs")); err != nil {
		_ = w.Close()
		return stopMounts
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range w.Changed() {
			changed()
		}
	}()
	return func() {
		stopMounts()
		_ = w.Close()
		<-done
	}
}
