package filechooser

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
	"unsafe"

	"github.com/stubbedev/memory-go"
	"github.com/stubbedev/memory-go/bufpool"
	"golang.org/x/sys/unix"
)

// The listing's buffers: getdents64 reads into dirBufSize, about 3,000
// names a call, and the paths are written pathChunk at a time.
const (
	dirBufSize = 128 << 10
	pathChunk  = 256 << 10
)

// entriesStart is the listing's first capacity; it doubles from there,
// where append would grow a large listing by a quarter at a time.
const entriesStart = 512

// linux_dirent64: d_ino, d_off, then d_reclen, d_type and the name.
const (
	direntReclen = 16
	direntName   = 19
)

// dirReader lists folders: one directory fd a listing, getdents64 into
// a pooled off-heap buffer, and every entry stated relative to that fd
// straight from the name in the buffer, so no path is walked and no
// string is made per entry beyond the path written into a shared chunk.
type dirReader struct {
	mem  memory.Shared
	bufs *bufpool.Pool
}

func newDirReader() *dirReader {
	r := &dirReader{}
	bufs, err := bufpool.New(&r.mem, 4*dirBufSize)
	if err != nil {
		panic(fmt.Sprintf("filechooser: a dirent pool retaining %d bytes: %v", 4*dirBufSize, err))
	}
	r.bufs = bufs
	return r
}

// dirCounts are the system calls a listing made.
type dirCounts struct {
	getdents, stats int
}

// List lists dir in full, sorted by path. Only the mode rule applies
// here, because it cannot change while the listing is cached; the
// hidden toggle, the type filter and the search text partition the
// cached listing. A directory that cannot be read is an error; an
// entry that cannot be stated (a dangling link) is listed as a file
// with nothing known about it.
func (r *dirReader) List(dir string, mode Mode) ([]Entry, dirCounts, error) {
	var counts dirCounts
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, counts, &fs.PathError{Op: "open", Path: dir, Err: err}
	}
	defer func() { _ = unix.Close(fd) }()
	buf, err := r.bufs.Get(dirBufSize)
	if err != nil {
		return nil, counts, fmt.Errorf("listing %s: a %d byte buffer: %w", dir, dirBufSize, err)
	}
	defer func() { _ = r.bufs.Put(buf) }()
	b := buf.Bytes()
	paths := pathArena{prefix: dirPrefix(dir)}
	var entries []Entry
	var st unix.Stat_t
	for {
		n, err := unix.Getdents(fd, b)
		counts.getdents++
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, counts, &fs.PathError{Op: "getdents", Path: dir, Err: err}
		}
		if n == 0 {
			break
		}
		for off := 0; off < n; {
			reclen := int(binary.NativeEndian.Uint16(b[off+direntReclen:]))
			name := b[off+direntName : off+reclen]
			name = name[:bytes.IndexByte(name, 0)]
			at := off + direntName
			off += reclen
			if string(name) == "." || string(name) == ".." {
				continue
			}
			counts.stats++
			e := Entry{Path: paths.join(name)}
			if statAt(fd, &b[at], &st) == nil {
				e = entryOf(e.Path, &st)
			}
			if !e.IsDir && mode.folders() {
				continue
			}
			if len(entries) == cap(entries) {
				entries = slices.Grow(entries, max(len(entries), entriesStart))
			}
			entries = append(entries, e)
		}
	}
	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	return entries, counts, nil
}

// statAt stats the NUL-terminated name at name relative to the
// directory fd, following a link as os.Stat does: newfstatat without a
// path conversion, so a listing allocates nothing per entry.
func statAt(dirfd int, name *byte, st *unix.Stat_t) error {
	_, _, errno := unix.Syscall6(unix.SYS_NEWFSTATAT, uintptr(dirfd), uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(st)), 0, 0, 0) //nolint:gosec // the name lives in the off-heap dirent buffer, the stat on the stack
	if errno != 0 {
		return errno
	}
	return nil
}

// dirPrefix is what a name in dir is appended to for its path.
func dirPrefix(dir string) string {
	if strings.HasSuffix(dir, "/") {
		return dir
	}
	return dir + "/"
}

// pathArena writes a listing's paths, prefix and name, into shared
// chunks on the Go heap and hands out strings over them: a chunk is
// only ever appended to, and the strings keep it alive for as long as
// any row or label holds one.
type pathArena struct {
	prefix string
	chunk  []byte
}

func (a *pathArena) join(name []byte) string {
	n := len(a.prefix) + len(name)
	if cap(a.chunk)-len(a.chunk) < n {
		a.chunk = make([]byte, 0, max(pathChunk, n))
	}
	start := len(a.chunk)
	a.chunk = append(a.chunk, a.prefix...)
	a.chunk = append(a.chunk, name...)
	return unsafe.String(&a.chunk[start], n) //nolint:gosec // the chunk is never written below its length again
}

// statEntry stats path, following a link, into an Entry: false when
// nothing is there or the mode does not list it. A dangling link is a
// file with nothing known about it.
func statEntry(path string, mode Mode) (Entry, bool) {
	var st unix.Stat_t
	e := Entry{Path: path}
	switch {
	case unix.Stat(path, &st) == nil:
		e = entryOf(path, &st)
	case unix.Lstat(path, &st) != nil:
		return Entry{}, false
	}
	if !e.IsDir && mode.folders() {
		return Entry{}, false
	}
	return e, true
}

// entryOf is path's entry as st describes it.
func entryOf(path string, st *unix.Stat_t) Entry {
	return Entry{
		Path: path, IsDir: st.Mode&unix.S_IFMT == unix.S_IFDIR,
		Size: st.Size, Modified: time.Unix(st.Mtim.Sec, st.Mtim.Nsec),
	}
}
