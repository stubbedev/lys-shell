package wallpaper

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stubbedev/lys-shell/config"
)

func cachedHarness(t *testing.T, dir string) *harness {
	t.Helper()
	h := newHarness(t, config.AnimationNone, "DP-1")
	h.s.rasters.d.cache = &rasterCache{dir: dir}
	return h
}

func loginWith(t *testing.T, dir, image string) *harness {
	t.Helper()
	h := cachedHarness(t, dir)
	h.useFile(t, image)
	h.plug("DP-1", 64, 36)
	h.run()
	return h
}

func (h *harness) livePixels() []byte {
	h.t.Helper()
	if len(h.s.rasters.live) != 1 {
		h.t.Fatalf("%d live rasters, want 1", len(h.s.rasters.live))
	}
	for _, r := range h.s.rasters.live {
		return r.image.Pix
	}
	return nil
}

func TestSecondLoginSkipsTheDecode(t *testing.T) {
	dir := t.TempDir()
	first := loginWith(t, dir, "")
	source := first.path("a.png")
	if first.decodes[source] != 1 {
		t.Fatalf("the first login decoded %d times, want 1", first.decodes[source])
	}
	want := bytes.Clone(first.livePixels())
	first.s.shutdown()

	second := loginWith(t, dir, source)
	if n := second.decodes[source]; n != 0 {
		t.Errorf("the second login decoded %d times, want 0", n)
	}
	if !bytes.Equal(second.livePixels(), want) {
		t.Error("the cached raster differs from the decoded one")
	}
	second.wantLive(1, "the cached raster alone")
	second.s.shutdown()
	second.wantLive(0, "shut down")
}

func TestChangedFileIsDecodedAgain(t *testing.T) {
	dir := t.TempDir()
	first := loginWith(t, dir, "")
	source := first.path("a.png")
	first.s.shutdown()
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(source, later, later); err != nil {
		t.Fatal(err)
	}
	second := loginWith(t, dir, source)
	if n := second.decodes[source]; n != 1 {
		t.Errorf("a changed file decoded %d times, want 1", n)
	}
	second.s.shutdown()
	third := loginWith(t, dir, source)
	if n := third.decodes[source]; n != 0 {
		t.Errorf("the rewritten copy decoded %d times, want 0", n)
	}
}

func TestDamagedCacheFileIsDecodedAgainAndReplaced(t *testing.T) {
	for name, damage := range map[string]func([]byte) []byte{
		"truncated":   func(b []byte) []byte { return b[:len(b)-1] },
		"header only": func(b []byte) []byte { return b[:rasterHeaderBytes] },
		"short":       func(b []byte) []byte { return b[:3] },
		"not a cache": func(b []byte) []byte { c := bytes.Clone(b); c[0] = 'X'; return c },
		"too large":   func(b []byte) []byte { c := bytes.Clone(b); c[len(rasterCacheMagic)+1] = 0xff; return c },
		"empty":       func([]byte) []byte { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			first := loginWith(t, dir, "")
			source := first.path("a.png")
			first.s.shutdown()
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("cache holds %v, %v", entries, err)
			}
			file := filepath.Join(dir, entries[0].Name())
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, damage(data), 0o600); err != nil {
				t.Fatal(err)
			}
			logs := captureLog(t)
			second := loginWith(t, dir, source)
			if n := second.decodes[source]; n != 1 {
				t.Errorf("a damaged file decoded %d times, want 1", n)
			}
			if second.s.surfaces["DP-1"].view.cur == nil {
				t.Fatal("a damaged file left the output empty")
			}
			if !strings.Contains(logs.String(), "decoding "+source+" again") {
				t.Errorf("the damage logged %q", logs.String())
			}
			second.s.shutdown()
			third := loginWith(t, dir, source)
			if n := third.decodes[source]; n != 0 {
				t.Errorf("the replaced copy decoded %d times, want 0", n)
			}
		})
	}
}

func TestOtherOutputSizeAndFitAreCachedApart(t *testing.T) {
	c := &rasterCache{dir: t.TempDir()}
	base := rasterKey{path: "/a", fit: "fill", box: target{w: 64, h: 36, scale: 1}, known: true}
	seen := map[string]bool{c.file(base): true}
	for _, k := range []rasterKey{
		{path: "/b", fit: "fill", box: base.box, known: true},
		{path: "/a", fit: "fit", box: base.box, known: true},
		{path: "/a", fit: "fill", box: target{w: 65, h: 36, scale: 1}, known: true},
		{path: "/a", fit: "fill", box: target{w: 64, h: 37, scale: 1}, known: true},
		{path: "/a", fit: "fill", box: target{w: 64, h: 36, scale: 2}, known: true},
	} {
		if f := c.file(k); seen[f] {
			t.Errorf("%+v shares a file", k)
		} else {
			seen[f] = true
		}
	}
}

func TestUnreportedOutputSizeIsNotCached(t *testing.T) {
	dir := t.TempDir()
	h := cachedHarness(t, dir)
	h.s.AddOutput(testOutput("DP-1", 0, 0))
	h.run()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("cached %d files for an output of no known size", len(entries))
	}
	h.s.shutdown()
	h.wantLive(0, "shut down")
}

func TestUnwritableCacheOnlyLogs(t *testing.T) {
	logs := captureLog(t)
	h := cachedHarness(t, filepath.Join(t.TempDir(), "missing"))
	h.plug("DP-1", 64, 36)
	h.run()
	if h.s.surfaces["DP-1"].view.cur == nil {
		t.Fatal("a failed write hid the wallpaper")
	}
	if !strings.Contains(logs.String(), "next login decodes") {
		t.Errorf("the failure logged %q", logs.String())
	}
	h.wantLive(1, "the save's reference is back")
}

func TestShutdownDuringASaveFreesTheRaster(t *testing.T) {
	h := cachedHarness(t, t.TempDir())
	h.plug("DP-1", 64, 36)
	h.step()
	h.step()
	h.step()
	if len(h.queued) != 1 {
		t.Fatalf("%d jobs queued, want the save", len(h.queued))
	}
	h.ended.Store(true)
	go h.run()
	h.s.shutdown()
	h.wantLive(0, "shut down")
}

func TestRasterStaysAliveThroughItsSave(t *testing.T) {
	h := cachedHarness(t, t.TempDir())
	h.plug("DP-1", 64, 36)
	h.step()
	h.step()
	h.step()
	if len(h.queued) != 1 {
		t.Fatalf("%d jobs queued, want the save", len(h.queued))
	}
	h.s.RemoveOutput(h.s.outputs["DP-1"])
	h.wantLive(1, "the save holds it")
	h.wantForgotten("the save holds it")
	h.run()
	h.wantLive(0, "the save ended")
}

func TestPruneKeepsTheBudgetAndTheNewest(t *testing.T) {
	dir := t.TempDir()
	c := &rasterCache{dir: dir}
	now := time.Now()
	write := func(name string, size int64, age time.Duration) string {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		when := now.Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
		return path
	}
	newest := write("newest", rasterCacheBudget/2, 0)
	next := write("next", rasterCacheBudget/4, time.Minute)
	old := write("old", rasterCacheBudget/2, time.Hour)
	if err := c.prune(newest); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]bool{newest: true, next: true, old: false} {
		_, err := os.Stat(path)
		if got := err == nil; got != want {
			t.Errorf("%s kept = %v, want %v", path, got, want)
		}
	}
	huge := write("huge", 2*rasterCacheBudget, 2*time.Hour)
	if err := c.prune(huge); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(huge); err != nil {
		t.Errorf("the file just written was pruned: %v", err)
	}
}
