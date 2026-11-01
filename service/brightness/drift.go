package brightness

import (
	"bytes"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
)

// levelProbe holds each backlight's brightness attribute open, so
// looking for a level no watch reported is one pread a device, and a
// level that did not move wakes nobody.
type levelProbe struct {
	levels []probedLevel
}

type probedLevel struct {
	file *os.File
	last []byte
}

// levelAttrSize fits the decimal of changed uint32 attribute and its newline.
const levelAttrSize = 12

// open replaces the probe's files with the devices now present, and
// remembers their levels. A device that will not open is logged and
// left out; hotplug gets another look at it.
func (p *levelProbe) open(devices []os.DirEntry) {
	p.close()
	for _, device := range devices {
		path := filepath.Join(BacklightDir, device.Name(), "brightness")
		file, err := os.Open(path) //nolint:gosec // the path is BacklightDir plus a directory entry
		if err != nil {
			log.Printf("brightness: drift probe: %v", err)
			continue
		}
		level := probedLevel{file: file, last: make([]byte, 0, levelAttrSize)}
		if _, err := level.moved(); err != nil {
			log.Printf("brightness: drift probe: %v", err)
			_ = file.Close()
			continue
		}
		p.levels = append(p.levels, level)
	}
}

func (p *levelProbe) close() {
	for _, level := range p.levels {
		_ = level.file.Close()
	}
	p.levels = p.levels[:0]
}

func (p *levelProbe) empty() bool { return len(p.levels) == 0 }

// moved reports whether changed level changed since the last look; an
// attribute that no longer reads is an error, the device being gone.
func (p *levelProbe) moved() (bool, error) {
	changed := false
	var failed error
	for i := range p.levels {
		moved, err := p.levels[i].moved()
		failed = errors.Join(failed, err)
		changed = changed || moved
	}
	return changed, failed
}

func (l *probedLevel) moved() (bool, error) {
	var buf [levelAttrSize]byte
	n, err := l.file.ReadAt(buf[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	if bytes.Equal(buf[:n], l.last) {
		return false, nil
	}
	l.last = append(l.last[:0], buf[:n]...)
	return true, nil
}
