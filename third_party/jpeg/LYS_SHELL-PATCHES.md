# image/jpeg's decoder from Go 1.27.3, patched for lys-shell

A copy of the standard library's `image/jpeg` decoder (`reader.go`,
`scan.go`, `huffman.go`, and the inverse half of `dct.go`) whose
memory lives in memory-go instead of on the Go heap. The test images
under `testdata/` are the standard library's, plus
`flat-1024.progressive.444.jpeg`.

- `Decode(r, mem)` replaces `Decode`, `DecodeConfig` and the `image`
  format registration: it returns the image with the `memory.Slice`
  that frees its pixels. The stock decoder stays registered for every
  other caller.
- `scan.go`, `makeImg`: the gray plane, the YCbCr planes and the black
  plane of a 4-component image are one memory-go allocation, laid out
  as `image.NewYCbCr` lays them out (`chromaSize`).
- `scan.go`, `processSOS`: a progressive scan's coefficient blocks (an
  int32 per sample, four times the decoded image) are memory-go
  allocations, freed and trimmed back to the system before `Decode`
  returns. A 3840x2880 4:4:4 progressive photo held 133 MB of them on
  the Go heap.
- `reader.go`, `applyBlack` and `convertToRGB`: the converted image is
  a memory-go allocation, and the planes it was converted from are
  freed at once; `image/internal/imageutil.DrawYCbCr` becomes
  `image/draw.Draw`, which takes the same fast path.
- On any error every allocation is freed.
- The encoder, the deprecated `Reader` interface and the config-only
  decode are gone, and golangci-lint's modernize fixes are applied.

`decode_test.go` pins the decoded images to `image/jpeg`'s for every
test image, its errors to `image/jpeg`'s on cut files, that nothing
stays allocated after an error or a free, and that a progressive decode
allocates at most 64 KiB on the Go heap.
