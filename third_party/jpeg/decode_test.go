package jpeg

import (
	"bytes"
	stdjpeg "image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/stubbedev/memory-go"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertNothingHeld(t *testing.T, mem *memory.Shared) {
	t.Helper()
	if st := mem.Stats(); st.Live != 0 || st.Bytes != 0 {
		t.Fatalf("the allocator still holds %d allocations of %d bytes", st.Live, st.Bytes)
	}
}

func TestDecodeMatchesTheStandardLibrary(t *testing.T) {
	names, err := filepath.Glob(filepath.Join("testdata", "*.jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 30 {
		t.Fatalf("only %d test images", len(names))
	}
	for _, path := range names {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			data := readTestdata(t, name)
			want, err := stdjpeg.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("image/jpeg: %v", err)
			}
			var mem memory.Shared
			got, pix, err := Decode(bytes.NewReader(data), &mem)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("decoded %T %v, image/jpeg decodes %T %v differently", got, got.Bounds(), want, want.Bounds())
			}
			if st := mem.Stats(); st.Live != 1 || st.Bytes < pix.Len() {
				t.Errorf("after the decode the allocator holds %d allocations of %d bytes, want only the %d-byte image", st.Live, st.Bytes, pix.Len())
			}
			if err := memory.FreeSlice(&mem, pix); err != nil {
				t.Fatal(err)
			}
			assertNothingHeld(t, &mem)
		})
	}
}

func TestDecodeErrorHoldsNothing(t *testing.T) {
	for _, name := range []string{"video-001.progressive.jpeg", "video-001.jpeg"} {
		t.Run(name, func(t *testing.T) {
			data := readTestdata(t, name)
			data = data[:len(data)/2]
			if _, err := stdjpeg.Decode(bytes.NewReader(data)); err == nil {
				t.Fatal("image/jpeg decodes the broken file, so it tests nothing")
			}
			var mem memory.Shared
			img, pix, err := Decode(bytes.NewReader(data), &mem)
			if err == nil {
				t.Fatalf("decoded the broken file to a %v image", img.Bounds())
			}
			if img != nil || pix.Len() != 0 {
				t.Errorf("a failed decode returned %v and %d bytes", img, pix.Len())
			}
			assertNothingHeld(t, &mem)
		})
	}
}

func TestDecodeFailsWhereTheStandardLibraryFailsAndHoldsNothing(t *testing.T) {
	for _, name := range []string{"video-001.progressive.jpeg", "video-001.q50.420.progressive.jpeg", "video-001.cmyk.jpeg", "video-005.gray.q50.progressive.jpeg"} {
		data := readTestdata(t, name)
		for cut := 2; cut < len(data); cut += len(data)/17 + 1 {
			_, stdErr := stdjpeg.Decode(bytes.NewReader(data[:cut]))
			var mem memory.Shared
			img, pix, err := Decode(bytes.NewReader(data[:cut]), &mem)
			if (err == nil) != (stdErr == nil) {
				t.Errorf("%s cut at %d: error %v, image/jpeg's %v", name, cut, err, stdErr)
			}
			if err == nil {
				if err := memory.FreeSlice(&mem, pix); err != nil {
					t.Fatal(err)
				}
			} else if img != nil || pix.Len() != 0 {
				t.Errorf("%s cut at %d: a failed decode returned an image", name, cut)
			}
			assertNothingHeld(t, &mem)
		}
	}
}

func heapBytes(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func TestDecodeKeepsAProgressiveImageOffTheHeap(t *testing.T) {
	data := readTestdata(t, "flat-1024.progressive.444.jpeg")
	const imageBytes = 3 * 1024 * 1024
	const coefficientBytes = 4 * imageBytes
	std := heapBytes(func() {
		if _, err := stdjpeg.Decode(bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	})
	if std < imageBytes+coefficientBytes {
		t.Fatalf("image/jpeg allocated only %d bytes on the heap, below the %d its image and coefficients take: the measurement is off", std, imageBytes+coefficientBytes)
	}
	var mem memory.Shared
	var pix memory.Slice[byte]
	got := heapBytes(func() {
		var err error
		if _, pix, err = Decode(bytes.NewReader(data), &mem); err != nil {
			t.Fatal(err)
		}
	})
	if got > 64<<10 {
		t.Errorf("the decode allocated %d bytes on the Go heap, want at most 64 KiB (image/jpeg: %d)", got, std)
	}
	if pix.Len() < imageBytes {
		t.Errorf("the image holds %d bytes off the heap, want at least %d", pix.Len(), imageBytes)
	}
	if err := memory.FreeSlice(&mem, pix); err != nil {
		t.Fatal(err)
	}
	assertNothingHeld(t, &mem)
}
