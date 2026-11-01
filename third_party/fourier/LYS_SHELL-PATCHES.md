# gonum's dsp/fourier from v0.17.0, patched for lys-shell

A copy of the real forward FFT of gonum.org/v1/gonum v0.17.0's `dsp/fourier` (BSD
3-Clause, LICENSE), the only part `cava` uses. Its `doc.go`, `fourier.go` and
`internal/fftpack` are carried without the complex, cosine, sine and quarter-wave
transforms or the inverse.

- Each product that reaches an addition or a subtraction is converted first, `cmul`
  spells out complex multiplication the same way, and the twiddle tables come from
  lys-lib's `third_party/gomath`. gonum's products fuse into FMAs on arm64 and not on
  amd64, so the same audio hashed to different coefficients on the two (1337fe53c59496eb
  against 3015fde0e866f0ec for `lys_test.go`'s input). amd64's results are unchanged, and
  `lys_test.go` holds that hash on every architecture.
