package widgets

import (
	"errors"
	"io/fs"
	"log"
	"sync"

	"github.com/stubbedev/lys-ui/widget"
)

// maxFileIconDecodes bounds the preloads running at once, so a dead mount holds a
// few goroutines rather than one per icon.
const maxFileIconDecodes = 4

var fileIconLoads = make(chan struct{}, maxFileIconDecodes)

type fileIconKey struct {
	path       string
	size       int
	num, denom int
}

var fileIconWaiters = struct {
	sync.Mutex
	byKey map[fileIconKey][]func()
}{byKey: map[fileIconKey][]func(){}}

// NewAsyncFileIcon is an icon showing fallback, a theme name or "" for nothing,
// until path has been decoded off the loop at its tree's scale, so no paint
// reads the file. A file that would paint nothing (missing, a FIFO or device,
// undecodable) keeps the fallback. ready runs on the loop through post once the
// file shows; it may be nil.
func NewAsyncFileIcon(path, fallback string, size int, post func(func()), ready func()) *widget.Icon {
	icon := widget.NewThemeIconFallback(fallback, size)
	show := func() {
		icon.SetThemeName(path)
		if ready != nil {
			ready()
		}
	}
	go post(func() { showAtScale(icon, path, size, post, show) })
	return icon
}

// showAtScale shows path once it is decoded at the scale icon's tree presents
// at, so its first paint there decodes nothing; an icon in no scaled tree yet is
// decoded at 1x, which tells whether it paints at all.
func showAtScale(icon *widget.Icon, path string, size int, post func(func()), show func()) {
	num, denom := widget.DeviceScaleOf(icon)
	if num == 0 {
		num, denom = 1, 1
	}
	requestFileIcon(fileIconKey{path, size, num, denom}, func() {
		post(func() {
			if n, d := widget.DeviceScaleOf(icon); n != 0 && (n != num || d != denom) && !widget.ThemeIconPreloaded(path, size, n, d) {
				showAtScale(icon, path, size, post, show)
				return
			}
			show()
		})
	})
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
	err := widget.PreloadThemeIcon(key.path, key.size, key.num, key.denom)
	<-fileIconLoads
	fileIconWaiters.Lock()
	waiters := fileIconWaiters.byKey[key]
	delete(fileIconWaiters.byKey, key)
	fileIconWaiters.Unlock()
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("icon: %v", err)
		}
		return
	}
	for _, done := range waiters {
		done()
	}
}
