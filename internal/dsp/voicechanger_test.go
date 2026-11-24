package dsp

import (
	"math"
	"math/rand"
	"testing"
)

func TestFFTRoundTrip(t *testing.T) {
	fft := NewFFT(2048)
	re := make([]float64, 2048)
	im := make([]float64, 2048)
	orig := make([]float64, 2048)
	for i := range re {
		orig[i] = math.Sin(2*math.Pi*440*float64(i)/48000) + 0.3*math.Cos(2*math.Pi*1200*float64(i)/48000)
		re[i] = orig[i]
	}

	fft.Transform(re, im, false)
	fft.Transform(re, im, true)

	for i := range orig {
		if math.Abs(re[i]-orig[i]) > 1e-6 || math.Abs(im[i]) > 1e-6 {
			t.Fatalf("FFT round-trip mismatch at %d: got (%f, %f) want (%f, 0)", i, re[i], im[i], orig[i])
		}
	}
}

func TestVoiceChangerPassthroughAndPitchShift(t *testing.T) {
	vc := NewVoiceChanger(48000)

	// 1. Disabled -> exact passthrough
	in := make([]float32, 480)
	for i := range in {
		in[i] = 0.4 * float32(math.Sin(2*math.Pi*180*float64(i)/48000))
	}
	buf := append([]float32(nil), in...)
	vc.Process(buf)
	for i := range in {
		if buf[i] != in[i] {
			t.Fatalf("disabled VC modified sample %d: %f != %f", i, buf[i], in[i])
		}
	}

	// 2. Enabled with +7 semitones (~1.498x) on harmonic voice-like signal (180Hz + harmonics) -> ~270Hz
	vc.SetParams(7.0, 1.20, 0.4, false)
	vc.SetEnabled(true)

	const totalFrames = 30 // 300ms
	outAll := make([]float32, 0, 480*totalFrames)
	frame := make([]float32, 480)
	sampleIdx := 0
	for f := 0; f < totalFrames; f++ {
		for i := range frame {
			tSec := float64(sampleIdx) / 48000.0
			frame[i] = float32(
				0.45*math.Sin(2*math.Pi*180.0*tSec) +
					0.25*math.Sin(2*math.Pi*360.0*tSec) +
					0.12*math.Sin(2*math.Pi*540.0*tSec),
			)
			sampleIdx++
		}
		vc.Process(frame)
		for i, s := range frame {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("NaN/Inf at frame %d sample %d", f, i)
			}
		}
		outAll = append(outAll, frame...)
	}

	// Analyze steady-state tail with 2048-point FFT to find dominant fundamental peak
	const anaN = 2048
	tail := outAll[len(outAll)-anaN:]
	anaFFT := NewFFT(anaN)
	re := make([]float64, anaN)
	im := make([]float64, anaN)
	for i := 0; i < anaN; i++ {
		w := 0.5 * (1.0 - math.Cos(2*math.Pi*float64(i)/float64(anaN)))
		re[i] = float64(tail[i]) * w
	}
	anaFFT.Transform(re, im, false)

	// Search fundamental band 150..400Hz
	binHz := 48000.0 / float64(anaN)
	bestBin := int(150.0 / binHz)
	bestMag := 0.0
	for k := int(150.0 / binHz); k <= int(400.0/binHz); k++ {
		m := math.Hypot(re[k], im[k])
		if m > bestMag {
			bestMag = m
			bestBin = k
		}
	}
	peakHz := float64(bestBin) * binHz
	if peakHz < 245 || peakHz > 295 {
		t.Fatalf("expected shifted fundamental near ~270Hz, got %.1f Hz (bin %d)", peakHz, bestBin)
	}

	// 3. Unvoiced noise (simulating "s/sh") remains finite and stable
	rng := rand.New(rand.NewSource(7))
	for f := 0; f < 5; f++ {
		for i := range frame {
			frame[i] = (rng.Float32()*2 - 1) * 0.2
		}
		vc.Process(frame)
		for i, s := range frame {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("NaN/Inf on unvoiced noise at frame %d sample %d", f, i)
			}
		}
	}

	// 4. Toggle OFF cleanly
	vc.SetEnabled(false)
	vc.Process(frame)
	for i, s := range frame {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("NaN/Inf on disable fade at %d", i)
		}
	}
}

func BenchmarkVoiceChanger10msFrame(b *testing.B) {
	vc := NewVoiceChanger(48000)
	vc.SetEnabled(true)
	frame := make([]float32, 480)
	for i := range frame {
		frame[i] = 0.3 * float32(math.Sin(2*math.Pi*220*float64(i)/48000))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vc.Process(frame)
	}
}
