// Package fswatch turns inotify events into coalesced change ticks, the
// Go counterpart of the `notify` crate watchers the Rust services use
// (the mail service's recursive maildir watch, the brightness sysfs
// watch). Most consumers only need "something under here changed": each
// burst is one tick. A named watcher (NewNamed) also keeps the names
// that changed, coalesced per name, for a consumer that applies each
// change instead of reading everything again.
//
// The reader blocks in poll(2) on the inotify fd and an eventfd, so an
// idle watcher costs nothing and Close wakes it without a timeout.
package fswatch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	"github.com/stubbedev/lys-lib/intconv"
	"golang.org/x/sys/unix"
)

// Changes is the event mask that counts as a change for a directory
// tree: files created, written, removed, renamed (maildir flags are
// renames), or their metadata changed. It matches notify's
// Create | Modify | Remove kinds.
const Changes uint32 = unix.IN_CREATE | unix.IN_MODIFY | unix.IN_DELETE |
	unix.IN_MOVED_FROM | unix.IN_MOVED_TO | unix.IN_ATTRIB |
	unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// Watcher is one inotify instance.
type Watcher struct {
	fd        int
	wake      int
	mask      uint32
	recursive bool

	mu     sync.Mutex
	dirs   map[int]string
	closed bool
	// named keeps pending, the events since the last Take, one per name
	// at index[name]; rescan replaces them when names were lost.
	named   bool
	pending []Event
	index   map[string]int
	rescan  bool

	ticks chan struct{}
	done  chan struct{}
}

// maxPending is how many distinct names a named watcher holds between
// two Takes; past it the names are dropped for a rescan.
const maxPending = 4096

// Event is one name in a watched directory and every event it saw
// since the last Take.
type Event struct {
	Name string
	Mask uint32
}

// Batch is what changed since the last Take: each name once, in the
// order it first changed. Rescan reports that names were lost (the
// kernel queue overflowed, or more names changed than a batch holds) or
// the watched directory itself was deleted or moved, so only reading it
// again is right; Events is then empty.
type Batch struct {
	Events []Event
	Rescan bool
}

// New starts a watcher for mask. With recursive set, Add walks the
// whole tree and directories created later are watched as they
// appear, which is what a recursive notify watcher does.
func New(mask uint32, recursive bool) (*Watcher, error) {
	return start(mask, recursive, false)
}

// NewNamed starts a watcher for mask on single directories that also
// keeps the names that changed for Take.
func NewNamed(mask uint32) (*Watcher, error) {
	return start(mask, false, true)
}

func start(mask uint32, recursive, named bool) (*Watcher, error) {
	if mask == 0 {
		return nil, errors.New("fswatch: empty event mask")
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, fmt.Errorf("fswatch: inotify: %w", err)
	}
	wake, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("fswatch: eventfd: %w", err)
	}
	polls, err := pollIn(fd, wake)
	if err != nil {
		_ = unix.Close(wake)
		_ = unix.Close(fd)
		return nil, err
	}
	w := &Watcher{
		fd:        fd,
		wake:      wake,
		mask:      mask,
		recursive: recursive,
		named:     named,
		index:     map[string]int{},
		dirs:      map[int]string{},
		ticks:     make(chan struct{}, 1),
		done:      make(chan struct{}),
	}
	go w.loop(polls)
	return w, nil
}

// Changed ticks once per burst of events; it closes after Close.
func (w *Watcher) Changed() <-chan struct{} { return w.ticks }

// Take hands over what changed since the last Take; a watcher not made
// by NewNamed has no names to hand over.
func (w *Watcher) Take() Batch {
	w.mu.Lock()
	defer w.mu.Unlock()
	b := Batch{Events: w.pending, Rescan: w.rescan}
	w.pending, w.rescan = nil, false
	clear(w.index)
	return b
}

// Add watches path (and, recursively, every directory below it).
func (w *Watcher) Add(path string) error {
	if !w.recursive {
		return w.addOne(path)
	}
	return filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory vanishing mid-walk is routine in a maildir
			// being synced; the root itself failing is not.
			if p == path {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		return w.addOne(p)
	})
}

func (w *Watcher) addOne(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("fswatch: watcher closed")
	}
	mask := w.mask
	if w.recursive {
		// New subdirectories must be seen to be watched, whatever
		// the caller asked for.
		mask |= unix.IN_CREATE | unix.IN_MOVED_TO
	}
	wd, err := unix.InotifyAddWatch(w.fd, path, mask)
	if err != nil {
		return fmt.Errorf("fswatch: watch %s: %w", path, err)
	}
	w.dirs[wd] = path
	return nil
}

// Close stops the reader, releases the fds, and closes Changed.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	var one [8]byte
	binary.NativeEndian.PutUint64(one[:], 1)
	_, _ = unix.Write(w.wake, one[:])
	<-w.done
	_ = unix.Close(w.wake)
	return unix.Close(w.fd)
}

func pollIn(fds ...int) ([]unix.PollFd, error) {
	out := make([]unix.PollFd, len(fds))
	for i, fd := range fds {
		n, err := intconv.Exact[int32](fd)
		if err != nil {
			return nil, fmt.Errorf("fswatch: poll fd: %w", err)
		}
		out[i] = unix.PollFd{Fd: n, Events: unix.POLLIN}
	}
	return out, nil
}

func (w *Watcher) loop(fds []unix.PollFd) {
	defer close(w.done)
	defer close(w.ticks)
	buf := make([]byte, 64*(unix.SizeofInotifyEvent+unix.NAME_MAX+1))
	for {
		if _, err := unix.Poll(fds, -1); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		if fds[1].Revents != 0 {
			return
		}
		changed := false
		for {
			n, err := unix.Read(w.fd, buf)
			if err != nil || n <= 0 {
				break
			}
			if w.handle(buf[:n]) {
				changed = true
			}
		}
		if changed {
			select {
			case w.ticks <- struct{}{}:
			default:
			}
		}
	}
}

// lost is a kernel queue overflow, or the watched directory itself
// going: events that cannot be applied name by name.
const lost = unix.IN_Q_OVERFLOW | unix.IN_DELETE_SELF | unix.IN_MOVE_SELF

// note keeps a named watcher's event: merged into its name's, or a
// rescan for one that lost names.
func (w *Watcher) note(name string, mask uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.rescan || (name == "" && mask&lost == 0) {
		return
	}
	if i, ok := w.index[name]; ok && mask&lost == 0 {
		w.pending[i].Mask |= mask
		return
	}
	if mask&lost != 0 || len(w.pending) >= maxPending {
		w.pending, w.rescan = nil, true
		clear(w.index)
		return
	}
	w.index[name] = len(w.pending)
	w.pending = append(w.pending, Event{Name: name, Mask: mask})
}

// handle parses one read's worth of events, adding watches for new
// subdirectories, and reports whether any matched the mask or lost
// events.
func (w *Watcher) handle(buf []byte) bool {
	changed := false
	for off := 0; off+unix.SizeofInotifyEvent <= len(buf); {
		// struct inotify_event: wd, mask, cookie, len, then the name.
		wd := int(int32(binary.NativeEndian.Uint32(buf[off:])))
		mask := binary.NativeEndian.Uint32(buf[off+4:])
		nameLen := int(binary.NativeEndian.Uint32(buf[off+12:]))
		nameStart := off + unix.SizeofInotifyEvent
		nameEnd := nameStart + nameLen
		if nameEnd > len(buf) {
			break
		}
		name := cString(buf[nameStart:nameEnd])
		off = nameEnd

		if mask&unix.IN_IGNORED != 0 {
			w.mu.Lock()
			delete(w.dirs, wd)
			w.mu.Unlock()
		}
		if w.recursive && mask&unix.IN_ISDIR != 0 && mask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
			w.mu.Lock()
			parent, ok := w.dirs[wd]
			w.mu.Unlock()
			if ok && name != "" {
				_ = w.Add(filepath.Join(parent, name))
			}
		}
		if mask&(w.mask|unix.IN_Q_OVERFLOW) == 0 {
			continue
		}
		changed = true
		if w.named {
			w.note(name, mask&(w.mask|lost))
		}
	}
	return changed
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
