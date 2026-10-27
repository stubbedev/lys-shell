package config

import (
	"errors"
	"fmt"
)

// MaxLockBlur is the largest lock blur, a sigma in pixels. It is far
// past any useful blur: at 1000 px a 4K background is already a wash of
// a few soft blotches. The three box passes approximating the gaussian
// have radii of about sigma, each pass costs O((w + h) * radius) on top
// of its O(w * h) to prime its sums, and the exact fast division holds
// to a radius of about 134 million: 1000 keeps both orders of magnitude
// away.
const MaxLockBlur = 1000

// ErrLockBlurRange is a lock blur outside [0, MaxLockBlur].
var ErrLockBlurRange = errors.New("lock blur out of range")

// LockBlur is the lock background's blur, refused at load above
// MaxLockBlur rather than clamped.
//
// Lock background blur in pixels, 0 for none.
type LockBlur struct{ sigma uint32 }

// NewLockBlur validates a blur of sigma pixels.
func NewLockBlur(sigma int64) (LockBlur, error) {
	if sigma < 0 || sigma > MaxLockBlur {
		return LockBlur{}, fmt.Errorf("%w: %d px, valid 0-%d", ErrLockBlurRange, sigma, MaxLockBlur)
	}
	return LockBlur{sigma: uint32(sigma)}, nil
}

// Sigma is the gaussian's standard deviation in the background's
// pixels, 0 for none.
func (b LockBlur) Sigma() float64 { return float64(b.sigma) }

// UnmarshalConfig implements Unmarshaler.
func (b *LockBlur) UnmarshalConfig(v any) error {
	sigma, err := decodeInt64(v)
	if err != nil {
		return err
	}
	parsed, err := NewLockBlur(sigma)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

// MarshalConfig implements Marshaler.
func (b LockBlur) MarshalConfig() any { return int64(b.sigma) }

func (LockBlur) configSchema(*schemaGen) Schema {
	return uint32RangeSchema(typeOf[LockBlur](), 0, MaxLockBlur)
}
