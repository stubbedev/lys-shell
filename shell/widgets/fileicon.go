package widgets

import (
	"log"
	"os"
	"sync"

	"github.com/stubbedev/lys-ui/widget"
)

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
// until path has been checked and decoded off the loop at its tree's scale, so
// no paint reads the file. Something other than a regular file (a FIFO, a device) is never opened
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
	requestFileIcon(fileIconKey{path, size}, func() { post(func() { showAtScale(icon, path, size, post, show) }) })
	return icon
}

// showAtScale shows path once it is decoded at the scale icon's tree presents
// at, so its first paint there decodes nothing; an icon in no scaled tree yet
// shows at once and decodes as its window prepares it, off the loop.
func showAtScale(icon *widget.Icon, path string, size int, post func(func()), show func()) {
	num, denom := widget.DeviceScaleOf(icon)
	if num == 0 || widget.ThemeIconPreloaded(path, size, num, denom) {
		show()
		return
	}
	go func() {
		widget.PreloadThemeIcon(path, size, num, denom)
		post(func() { showAtScale(icon, path, size, post, show) })
	}()
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
	ok := regularFile(key.path)
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

func regularFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		log.Printf("icon: %s is not a regular file", path)
		return false
	}
	return true
}
