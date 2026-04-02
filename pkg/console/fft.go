//go:build console

package console

import (
	"math"
	"math/cmplx"
)

// fft computes an in-place radix-2 Cooley-Tukey FFT.
func fft(a []complex128) {
	n := len(a)
	if n <= 1 {
		return
	}

	// Bit-reversal permutation
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}

	// Butterfly stages
	for length := 2; length <= n; length <<= 1 {
		angle := -2 * math.Pi / float64(length)
		wn := cmplx.Exp(complex(0, angle))
		for i := 0; i < n; i += length {
			w := complex(1, 0)
			for j := 0; j < length/2; j++ {
				u := a[i+j]
				v := w * a[i+j+length/2]
				a[i+j] = u + v
				a[i+j+length/2] = u - v
				w *= wn
			}
		}
	}
}

// rfft computes the real FFT of x, returning n/2+1 complex bins
// where n is the next power of 2 >= len(x).
func rfft(x []float64) ([]complex128, int) {
	n := nextPow2(len(x))
	buf := make([]complex128, n)
	for i, v := range x {
		buf[i] = complex(v, 0)
	}
	fft(buf)
	return buf[:n/2+1], n
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}
