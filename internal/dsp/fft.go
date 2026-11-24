package dsp

import "math"

// FFT performs in-place radix-2 Cooley-Tukey FFT/IFFT with precomputed tables (allocation-free).
type FFT struct {
	n      int
	bits   int
	bitRev []int
	cosTab []float64
	sinTab []float64
}

func NewFFT(n int) *FFT {
	bits := 0
	for (1 << bits) < n {
		bits++
	}
	bitRev := make([]int, n)
	for i := 0; i < n; i++ {
		rev := 0
		x := i
		for b := 0; b < bits; b++ {
			rev = (rev << 1) | (x & 1)
			x >>= 1
		}
		bitRev[i] = rev
	}
	cosTab := make([]float64, n/2)
	sinTab := make([]float64, n/2)
	for i := 0; i < n/2; i++ {
		angle := -2.0 * math.Pi * float64(i) / float64(n)
		cosTab[i] = math.Cos(angle)
		sinTab[i] = math.Sin(angle)
	}
	return &FFT{
		n:      n,
		bits:   bits,
		bitRev: bitRev,
		cosTab: cosTab,
		sinTab: sinTab,
	}
}

// Transform computes forward FFT (inverse=false) or inverse FFT (inverse=true, scaled by 1/N) in-place.
func (f *FFT) Transform(re, im []float64, inverse bool) {
	n := f.n
	for i := 0; i < n; i++ {
		j := f.bitRev[i]
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}

	sign := 1.0
	if inverse {
		sign = -1.0
	}

	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for i := 0; i < n; i += size {
			k := 0
			for j := i; j < i+half; j++ {
				wr := f.cosTab[k]
				wi := sign * f.sinTab[k]
				k += step

				j2 := j + half
				tr := re[j2]*wr - im[j2]*wi
				ti := re[j2]*wi + im[j2]*wr

				re[j2] = re[j] - tr
				im[j2] = im[j] - ti
				re[j] += tr
				im[j] += ti
			}
		}
	}

	if inverse {
		invN := 1.0 / float64(n)
		for i := 0; i < n; i++ {
			re[i] *= invN
			im[i] *= invN
		}
	}
}
