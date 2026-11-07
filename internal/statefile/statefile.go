// Package statefile writes small state files off the UI loop, whole
// each time through lys-lib atomicfile: the newest content handed over
// wins, an older write still running never lands over it, and Wait
// returns once every write handed over has ended.
package statefile

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/stubbedev/lys-lib/atomicfile"
)

// File is one state file; the zero File with an empty Path keeps
// nothing.
type File struct {
	Path    string
	mu      sync.Mutex
	queued  uint64
	written uint64
	writes  sync.WaitGroup
}

// Write hands data over to be written, in a directory only the user
// may enter; a write that fails is logged, as nothing waits on it.
func (f *File) Write(data []byte) {
	if f.Path == "" {
		return
	}
	f.mu.Lock()
	f.queued++
	seq := f.queued
	f.mu.Unlock()
	f.writes.Go(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if seq < f.written {
			return
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
			log.Printf("saving %s: %v", f.Path, err)
			return
		}
		if err := atomicfile.WriteFile(f.Path, data, 0o600); err != nil {
			log.Printf("saving %s: %v", f.Path, err)
			return
		}
		f.written = seq
	})
}

// Wait waits for the writes handed over.
func (f *File) Wait() { f.writes.Wait() }
