// Copyright ©2018 The Gonum Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fftpack

// swap returns c with the real and imaginary parts swapped.
func swap(c complex128) complex128 {
	return complex(imag(c), real(c))
}

// scale scales the complex number c by f.
func scale(f float64, c complex128) complex128 {
	return complex(float64(f*real(c)), float64(f*imag(c)))
}

// cmul is a*b with each product rounded: the operator's own products
// fuse into FMAs on arm64, where they would round differently.
func cmul(a, b complex128) complex128 {
	return complex(float64(real(a)*real(b))-float64(imag(a)*imag(b)), float64(real(a)*imag(b))+float64(imag(a)*real(b)))
}
