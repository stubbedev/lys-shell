package widgets

import (
	"log"
	"os"
	"slices"
	"sync"

	"github.com/stubbedev/lys-ui/widget"
)

// fileIconScales are the device scales the shell's surfaces have painted at,
// which a file icon decodes at ahead of its first paint. lys-ui tells a scale
// only through the canvas a surface paints, so a surface at a scale not seen
// before shows the nearest raster resampled while the exact one decodes.
var fileIconScales = struct {
	sync.Mutex
	seen []deviceScale
}{seen: []deviceScale{{1, 1}}}

type deviceScale struct{ num, denom int }

// NoteDeviceScale records a device scale a surface painted at, num/denom device
// pixels per logical one, so the icons made from now on decode at it too.
func NoteDeviceScale(num, denom int) {
	if num <= 0 || denom <= 0 {
		return
	}
	s := deviceScale{num, denom}
	fileIconScales.Lock()
	defer fileIconScales.Unlock()
	if !slices.Contains(fileIconScales.seen, s) {
		fileIconScales.seen = append(fileIconScales.seen, s)
	}
}

func seenScales() []deviceScale {
	fileIconScales.Lock()
	defer fileIconScales.Unlock()
	return slices.Clone(fileIconScales.seen)
}

// maxFileIconDecodes bounds the decodes running at once, so a dead mount holds a
// few goroutines rather than one per icon.
const maxFileIconDecodes = 4

var fileIconLoads = make(chan struct{}, maxFileIconDecodes)

type fileIconKey struct {
	path string
	size int
}

var fileIconWaiters = struct {
	sync.Mutex
	byKey map[fileIconKey][]func()
}{byKey: map[fileIconKey][]func(){}}

// NewAsyncFileIcon is an icon showing fallback, a theme name or "" for nothing,
// until path has been checked and decoded off the loop, so no paint reads the
// file. Something other than a regular file (a FIFO, a device) is never opened
// and the fallback stays. ready runs on the loop through post once the file
// shows; it may be nil.
func NewAsyncFileIcon(path, fallback string, size int, post func(func()), ready func()) *widget.Icon {
	icon := widget.NewThemeIconFallback(fallback, size)
	show := func() {
		icon.SetThemeName(path)
		if ready != nil {
			ready()
		}
	}
	scales := seenScales()
	if slices.ContainsFunc(scales, func(s deviceScale) bool { return !widget.ThemeIconPreloaded(path, size, s.num, s.denom) }) {
		requestFileIcon(fileIconKey{path, size}, scales, func() { post(show) })
		return icon
	}
	show()
	return icon
}

func requestFileIcon(key fileIconKey, scales []deviceScale, done func()) {
	fileIconWaiters.Lock()
	defer fileIconWaiters.Unlock()
	waiters, running := fileIconWaiters.byKey[key]
	fileIconWaiters.byKey[key] = append(waiters, done)
	if !running {
		go loadFileIcon(key, scales)
	}
}

func loadFileIcon(key fileIconKey, scales []deviceScale) {
	fileIconLoads <- struct{}{}
	ok := decodeFileIcon(key, scales)
	<-fileIconLoads
	fileIconWaiters.Lock()
	waiters := fileIconWaiters.byKey[key]
	delete(fileIconWaiters.byKey, key)
	fileIconWaiters.Unlock()
	if !ok {
		return
	}
	for _, done := range waiters {
		done()
	}
}

func decodeFileIcon(key fileIconKey, scales []deviceScale) bool {
	info, err := os.Stat(key.path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		log.Printf("icon: %s is not a regular file", key.path)
		return false
	}
	for _, s := range scales {
		widget.PreloadThemeIcon(key.path, key.size, s.num, s.denom)
	}
	return true
}
