package widgets

import (
	"log"
	"os"
	"sync"

	"github.com/stubbedev/lys-ui/widget"
)

// fileIconScales are the device scales a file icon decodes at ahead of its
// first paint; a surface at another one decodes once more as it paints.
var fileIconScales = [][2]int{{1, 1}, {2, 1}}

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
	if widget.ThemeIconPreloaded(path, size, 1, 1) && widget.ThemeIconPreloaded(path, size, 2, 1) {
		show()
		return icon
	}
	requestFileIcon(fileIconKey{path, size}, func() { post(show) })
	return icon
}

func requestFileIcon(key fileIconKey, done func()) {
	fileIconWaiters.Lock()
	defer fileIconWaiters.Unlock()
	waiters, running := fileIconWaiters.byKey[key]
	fileIconWaiters.byKey[key] = append(waiters, done)
	if !running {
		go loadFileIcon(key)
	}
}

func loadFileIcon(key fileIconKey) {
	fileIconLoads <- struct{}{}
	ok := decodeFileIcon(key)
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

func decodeFileIcon(key fileIconKey) bool {
	info, err := os.Stat(key.path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		log.Printf("icon: %s is not a regular file", key.path)
		return false
	}
	for _, scale := range fileIconScales {
		widget.PreloadThemeIcon(key.path, key.size, scale[0], scale[1])
	}
	return true
}
