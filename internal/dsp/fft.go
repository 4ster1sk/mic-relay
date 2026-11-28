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
	if n < 2 {
		return
	}
	_ = re[n-1]
	_ = im[n-1]
	bitRev := f.bitRev
	for i := 0; i < n; i++ {
		j := bitRev[i]
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}

	// Unrolled Stage 1 (size = 2): twiddle is identically (1, 0), zero multiplications
	for i := 0; i < n; i += 2 {
		r0, i0 := re[i], im[i]
		r1, i1 := re[i+1], im[i+1]
		re[i] = r0 + r1
		im[i] = i0 + i1
		re[i+1] = r0 - r1
		im[i+1] = i0 - i1
	}

	sign := 1.0
	if inverse {
		sign = -1.0
	}

	// Unrolled Stage 2 (size = 4): k=0 -> (1, 0), k=n/4 -> (0, -sign)
	if n >= 4 {
		for i := 0; i < n; i += 4 {
			r0, i0 := re[i], im[i]
			r2, i2 := re[i+2], im[i+2]
			re[i] = r0 + r2
			im[i] = i0 + i2
			re[i+2] = r0 - r2
			im[i+2] = i0 - i2

			r1, i1 := re[i+1], im[i+1]
			r3, i3 := re[i+3], im[i+3]
			tr := sign * i3
			ti := -sign * r3
			re[i+1] = r1 + tr
			im[i+1] = i1 + ti
			re[i+3] = r1 - tr
			im[i+3] = i1 - ti
		}
	}

	cosTab := f.cosTab
	sinTab := f.sinTab
	for size := 8; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for i := 0; i < n; i += size {
			k := 0
			for j := i; j < i+half; j++ {
				wr := cosTab[k]
				wi := sign * sinTab[k]
				k += step

				j2 := j + half
				r2, i2 := re[j2], im[j2]
				tr := r2*wr - i2*wi
				ti := r2*wi + i2*wr

				rj, ij := re[j], im[j]
				re[j2] = rj - tr
				im[j2] = ij - ti
				re[j] = rj + tr
				im[j] = ij + ti
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
