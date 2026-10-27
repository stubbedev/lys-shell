package filechooser

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestMeasureThumbnailScroll measures a grid scrolled through a folder
// of 200 6000x4000 JPEGs: the peak Go heap while every thumbnail is
// made, and the heap and RSS once the dialog closed. It reports and
// asserts nothing; LYS_MEASURE_THUMBS=1 runs it.
func TestMeasureThumbnailScroll(t *testing.T) {
	if os.Getenv("LYS_MEASURE_THUMBS") == "" {
		t.Skip("LYS_MEASURE_THUMBS=1 measures the thumbnail scroll")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hs := newHarness(t, "")
	dir := filepath.Join(hs.root, "photos")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	photo := gradientJPEG(t, 6000, 4000)
	const count = 200
	for i := range count {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d.jpg", i)), photo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hs.open(t, OpenRequest{CurrentFolder: dir})
	settleHeap()
	base := heapBytes()

	var peak atomic.Uint64
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			peak.Store(max(peak.Load(), heapBytes()))
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	start := time.Now()
	hs.do(func() {
		grid := rows{hs.c, true}
		for i := range grid.Len() {
			grid.Row(i)
		}
	})
	for i := range count {
		hs.waitThumbFor(t, filepath.Join(dir, fmt.Sprintf("%03d.jpg", i)), gridIconPx, time.Minute)
	}
	took := time.Since(start)
	close(stop)
	<-sampled
	hs.do(func() { hs.c.finish(nil) })
	settleHeap()
	t.Logf("%d thumbnails in %v: peak heap %s over a %s base; after close heap %s, RSS %s",
		count, took.Round(time.Millisecond), mib(max(peak.Load(), base)-base), mib(base), mib(heapBytes()), mib(rssBytes(t)))
}

// gradientJPEG is a w x h JPEG of a smooth gradient: small on disk,
// full size decoded.
func gradientJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewYCbCr(image.Rect(0, 0, w, h), image.YCbCrSubsampleRatio420)
	for y := range h {
		for x := range w {
			img.Y[img.YOffset(x, y)] = uint8(x * 255 / w)
		}
	}
	for i := range img.Cb {
		img.Cb[i], img.Cr[i] = 128, 128
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// settleHeap collects twice and returns freed pages, so what is
// measured is what is still reachable.
func settleHeap() {
	runtime.GC()
	runtime.GC()
	debug.FreeOSMemory()
}

func heapBytes() uint64 {
	s := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	return s[0].Value.Uint64()
}

func rssBytes(t *testing.T) uint64 {
	t.Helper()
	f, err := os.Open("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for sc := bufio.NewScanner(f); sc.Scan(); {
		if rest, ok := strings.CutPrefix(sc.Text(), "VmRSS:"); ok {
			kb, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return kb << 10
		}
	}
	t.Fatal("no VmRSS in /proc/self/status")
	return 0
}

func mib(b uint64) string { return fmt.Sprintf("%.1f MiB", float64(b)/(1<<20)) }
