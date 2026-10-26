// Package intconv converts between integer types without silent
// wraparound: Exact refuses a value the target cannot hold, Clamp
// saturates it, and Duration scales a count into a time.Duration.
package intconv

import (
	"fmt"
	"math"
	"reflect"
	"time"
)

// Integer is every integer type.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// RangeError is a value its target type cannot hold.
type RangeError struct {
	Value  string
	Target reflect.Type
}

func (e *RangeError) Error() string {
	return fmt.Sprintf("%s does not fit in %s", e.Value, e.Target)
}

func fits[T, F Integer](v F) (T, bool) {
	t := T(v)
	return t, F(t) == v && (t < 0) == (v < 0)
}

// Exact returns v as a T, or a *RangeError when T cannot hold it.
func Exact[T, F Integer](v F) (T, error) {
	t, ok := fits[T](v)
	if !ok {
		return 0, &RangeError{Value: fmt.Sprint(v), Target: reflect.TypeFor[T]()}
	}
	return t, nil
}

// Clamp returns v as a T, saturated to T's range.
func Clamp[T, F Integer](v F) T {
	if t, ok := fits[T](v); ok {
		return t
	}
	if v < 0 {
		return minOf[T]()
	}
	return maxOf[T]()
}

func signed[T Integer]() bool {
	var zero T
	return zero-1 < 0
}

func minOf[T Integer]() T {
	if !signed[T]() {
		return 0
	}
	return T(1) << (reflect.TypeFor[T]().Bits() - 1)
}

func maxOf[T Integer]() T {
	if !signed[T]() {
		return ^T(0)
	}
	return T(1)<<(reflect.TypeFor[T]().Bits()-1) - 1
}

// Duration is n units, saturated to time.Duration's range. It panics
// on a unit that is not positive.
func Duration[F Integer](n F, unit time.Duration) time.Duration {
	if unit <= 0 {
		panic(fmt.Sprintf("intconv: Duration unit %v is not positive", unit))
	}
	c := Clamp[int64](n)
	switch {
	case c > math.MaxInt64/int64(unit):
		return math.MaxInt64
	case c < math.MinInt64/int64(unit):
		return math.MinInt64
	}
	return time.Duration(c) * unit
}
