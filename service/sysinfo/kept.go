package sysinfo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// keptFile re-reads one procfs file through a descriptor held open, so
// a poll costs preads instead of an open, reads and a close.
type keptFile struct {
	path string
	file *os.File
	buf  []byte
}

// keptFileStart is the first buffer: every file here fits it on most
// machines, and read grows it on the others.
const keptFileStart = 8192

// read returns the file's content from its start, up to the chunk that
// holds through, or the whole file when through is nil: stopping early
// spares the pread that would only report the end. The result is valid
// until the next read. A failed read closes the descriptor, so the next
// read opens the file afresh.
func (k *keptFile) read(through []byte) ([]byte, error) {
	if k.file == nil {
		file, err := os.Open(k.path)
		if err != nil {
			return nil, fmt.Errorf("sysinfo: %w", err)
		}
		k.file = file
		if k.buf == nil {
			k.buf = make([]byte, keptFileStart)
		}
	}
	fd := int(k.file.Fd())
	defer runtime.KeepAlive(k.file)
	n := 0
	for {
		if n == len(k.buf) {
			k.buf = append(k.buf, make([]byte, len(k.buf))...)
		}
		m, err := unix.Pread(fd, k.buf[n:], int64(n))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			k.Close()
			return nil, fmt.Errorf("sysinfo: read %s: %w", k.path, err)
		}
		if m == 0 {
			return k.buf[:n], nil
		}
		n += m
		if through != nil && holdsLine(k.buf[:n], through) {
			return k.buf[:n], nil
		}
	}
}

// Close releases the descriptor; a later read opens the file again.
func (k *keptFile) Close() {
	if k.file != nil {
		_ = k.file.Close()
		k.file = nil
	}
}

// holdsLine reports whether data has prefix and the end of its line.
func holdsLine(data, prefix []byte) bool {
	i := bytes.Index(data, prefix)
	return i >= 0 && bytes.IndexByte(data[i+len(prefix):], '\n') >= 0
}
