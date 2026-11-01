package fourier

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

// One hash, with no per-architecture variant: the transform gives the
// same bits on all of them.
func TestCoefficientsAreTheSameBitsEverywhere(t *testing.T) {
	const want = "1337fe53c59496eb"
	in := make([]float64, 4096)
	state := uint64(1)
	for i := range in {
		state = state*6364136223846793005 + 1442695040888963407
		in[i] = float64(int64(state>>40)) / (1 << 20)
	}
	h := sha256.New()
	var b [8]byte
	for _, c := range NewFFT(len(in)).Coefficients(nil, in) {
		for _, v := range []float64{real(c), imag(c)} {
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
			h.Write(b[:])
		}
	}
	if got := fmt.Sprintf("%x", h.Sum(nil)[:8]); got != want {
		t.Errorf("coefficients hash to %s, want %s", got, want)
	}
}
