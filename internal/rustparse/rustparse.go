// Package rustparse parses numbers with Rust's str::parse grammar and
// error messages, so values the Rust CLI accepts or rejects behave the
// same in Go (strconv takes underscores, hex floats, and other forms
// Rust refuses, and words its errors differently).
package rustparse

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Error is a ParseIntError / ParseFloatError: its message is Rust's.
type Error string

func (e Error) Error() string { return string(e) }

// Rust's messages.
const (
	ErrIntEmpty   Error = "cannot parse integer from empty string"
	ErrIntDigit   Error = "invalid digit found in string"
	ErrIntHigh    Error = "number too large to fit in target type"
	ErrIntLow     Error = "number too small to fit in target type"
	ErrFloatEmpty Error = "cannot parse float from empty string"
	ErrFloat      Error = "invalid float literal"
)

// Int parses a signed integer of the given bit size (i8..i64): an
// optional '+' or '-', then ASCII digits only.
func Int(s string, bits int) (int64, error) {
	negative, err := checkDigits(s, true)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(s, 10, bits)
	if err != nil {
		if negative {
			return 0, ErrIntLow
		}
		return 0, ErrIntHigh
	}
	return v, nil
}

// Uint parses an unsigned integer of the given bit size (u8..u64): an
// optional '+', then ASCII digits only; a '-' is an invalid digit.
func Uint(s string, bits int) (uint64, error) {
	if _, err := checkDigits(s, false); err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "+"), 10, bits)
	if err != nil {
		return 0, ErrIntHigh
	}
	return v, nil
}

func checkDigits(s string, signed bool) (negative bool, err error) {
	if s == "" {
		return false, ErrIntEmpty
	}
	digits := s
	switch digits[0] {
	case '+':
		digits = digits[1:]
	case '-':
		if !signed {
			return false, ErrIntDigit
		}
		negative = true
		digits = digits[1:]
	}
	if digits == "" {
		return false, ErrIntDigit
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false, ErrIntDigit
		}
	}
	return negative, nil
}

// floatGrammar is f64::from_str's: decimal digits with an optional
// fraction and exponent, or inf/infinity/nan in any case, all with an
// optional sign.
var floatGrammar = regexp.MustCompile(`^[+-]?(?:(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?|(?i:inf|infinity|nan))$`)

// Float parses an f64; out-of-range magnitudes saturate to ±inf or 0
// as Rust's do.
func Float(s string) (float64, error) {
	if s == "" {
		return 0, ErrFloatEmpty
	}
	if !floatGrammar.MatchString(s) {
		return 0, ErrFloat
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return 0, ErrFloat
	}
	return v, nil
}
