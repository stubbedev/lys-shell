package filechooser

import (
	"errors"
	"io"
	"log"
	"os"

	"golang.org/x/sys/unix"
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
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, table)
		fds := []unix.PollFd{
			{Fd: int32(table.Fd()), Events: unix.POLLPRI | unix.POLLERR},
			{Fd: int32(quitR.Fd()), Events: unix.POLLIN},
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
