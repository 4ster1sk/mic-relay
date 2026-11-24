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

// synthVowelFrame synthesizes a harmonic voice-like signal with 2 formant peaks (F1, F2).
func synthVowel(sr, n int, f0, f1, f2 float64) []float32 {
	out := make([]float32, n)
	maxHarm := int(6000.0 / f0)
	var amps []float64
	for h := 1; h <= maxHarm; h++ {
		freq := f0 * float64(h)
		d1 := (freq - f1) / 140.0
		d2 := (freq - f2) / 260.0
		env := 0.12 + 0.9*math.Exp(-0.5*d1*d1) + 0.65*math.Exp(-0.5*d2*d2)
		amps = append(amps, env/math.Pow(float64(h), 0.35))
	}
	for i := 0; i < n; i++ {
		tSec := float64(i) / float64(sr)
		sum := 0.0
		for h := 1; h <= maxHarm; h++ {
			sum += amps[h-1] * math.Sin(2.0*math.Pi*f0*float64(h)*tSec)
		}
		out[i] = float32(sum * 0.14)
	}
	return out
}

func runVCFrames(t *testing.T, vc *VoiceChanger, sig []float32) []float32 {
	t.Helper()
	out := make([]float32, 0, len(sig))
	frame := make([]float32, 480)
	for off := 0; off+480 <= len(sig); off += 480 {
		copy(frame, sig[off:off+480])
		vc.Process(frame)
		for i, s := range frame {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("NaN/Inf at offset %d sample %d", off, i)
			}
		}
		out = append(out, frame...)
	}
	return out
}

func analyzeSpectrum(samples []float32) []float64 {
	const anaN = 4096
	tail := samples[len(samples)-anaN:]
	fft := NewFFT(anaN)
	re := make([]float64, anaN)
	im := make([]float64, anaN)
	for i := 0; i < anaN; i++ {
		w := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(anaN)))
		re[i] = float64(tail[i]) * w
	}
	fft.Transform(re, im, false)
	mag := make([]float64, anaN/2+1)
	for k := range mag {
		mag[k] = math.Hypot(re[k], im[k])
	}
	return mag
}

func bandEnergy(mag []float64, sr int, loHz, hiHz float64) float64 {
	n := (len(mag) - 1) * 2
	binHz := float64(sr) / float64(n)
	lo := int(loHz / binHz)
	hi := int(hiHz / binHz)
	if lo < 1 {
		lo = 1
	}
	if hi >= len(mag) {
		hi = len(mag) - 1
	}
	sum := 0.0
	for k := lo; k <= hi; k++ {
		sum += mag[k] * mag[k]
	}
	return sum
}

func bandCentroid(mag []float64, sr int, loHz, hiHz float64) float64 {
	n := (len(mag) - 1) * 2
	binHz := float64(sr) / float64(n)
	lo := int(loHz / binHz)
	hi := int(hiHz / binHz)
	if lo < 1 {
		lo = 1
	}
	if hi >= len(mag) {
		hi = len(mag) - 1
	}
	var num, den float64
	for k := lo; k <= hi; k++ {
		e := mag[k] * mag[k]
		num += float64(k) * binHz * e
		den += e
	}
	if den <= 1e-12 {
		return 0
	}
	return num / den
}

func peakInBand(mag []float64, sr int, loHz, hiHz float64) float64 {
	n := (len(mag) - 1) * 2
	binHz := float64(sr) / float64(n)
	lo := int(loHz / binHz)
	hi := int(hiHz / binHz)
	bestK := lo
	bestM := -1.0
	for k := lo; k <= hi && k < len(mag); k++ {
		if mag[k] > bestM {
			bestM = mag[k]
			bestK = k
		}
	}
	return float64(bestK) * binHz
}

func TestVoiceChangerPassthroughAndPitchHarmonicity(t *testing.T) {
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

	// 2. Enabled with +10 semitones (~1.7818x) on 120Hz male vowel -> F0 ~213.8Hz, H2 ~427.6Hz
	vc.SetConfig(VoiceChangerParams{
		PitchSemitones:  10.0,
		FormantRatio:    1.18,
		ThroatFormant:   1.05,
		BrightFormant:   1.05,
		Breathiness:     0.0,
		Softness:        0.2,
		HighPassEnabled: false,
		HighPassHz:      140.0,
		DeEsser:         0.0,
	})
	vc.SetEnabled(true)

	sig := synthVowel(48000, 480*45, 120.0, 650.0, 1300.0) // 450ms
	out := runVCFrames(t, vc, sig)
	mag := analyzeSpectrum(out)

	f0Peak := peakInBand(mag, 48000, 175.0, 260.0)
	h2Peak := peakInBand(mag, 48000, 360.0, 500.0)
	wantF0 := 120.0 * math.Pow(2.0, 10.0/12.0) // ~213.8Hz
	if math.Abs(f0Peak-wantF0) > 18.0 {
		t.Fatalf("expected shifted F0 near %.1f Hz, got %.1f Hz", wantF0, f0Peak)
	}
	// Verify harmonicity (H2 should be tightly 2x F0, unlike STFT bin-rounding inharmonicity)
	if math.Abs(h2Peak-2.0*f0Peak) > 25.0 {
		t.Fatalf("expected harmonic H2 near %.1f Hz (2x F0), got %.1f Hz", 2.0*f0Peak, h2Peak)
	}

	// 3. Unvoiced noise remains finite and stable
	rng := rand.New(rand.NewSource(7))
	frame := make([]float32, 480)
	for f := 0; f < 6; f++ {
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

func TestVoiceChangerIndependentF1F2Formants(t *testing.T) {
	sig := synthVowel(48000, 480*40, 140.0, 620.0, 1700.0)

	runWith := func(throat, bright float64) []float64 {
		vc := NewVoiceChanger(48000)
		vc.SetConfig(VoiceChangerParams{
			PitchSemitones:  0.0,
			FormantRatio:    1.0,
			ThroatFormant:   throat,
			BrightFormant:   bright,
			Breathiness:     0.0,
			Softness:        0.0,
			HighPassEnabled: false,
			HighPassHz:      120.0,
			DeEsser:         0.0,
		})
		vc.SetEnabled(true)
		return analyzeSpectrum(runVCFrames(t, vc, sig))
	}

	magLowF1 := runWith(0.85, 1.00)
	magHighF1 := runWith(1.25, 1.00)
	cLowF1 := bandCentroid(magLowF1, 48000, 350.0, 1100.0)
	cHighF1 := bandCentroid(magHighF1, 48000, 350.0, 1100.0)
	if cHighF1 <= cLowF1+40.0 {
		t.Fatalf("expected ThroatFormant(F1) lift to raise F1 centroid: low=%.1fHz high=%.1fHz", cLowF1, cHighF1)
	}

	magLowF2 := runWith(1.00, 0.85)
	magHighF2 := runWith(1.00, 1.25)
	cLowF2 := bandCentroid(magLowF2, 48000, 1100.0, 3400.0)
	cHighF2 := bandCentroid(magHighF2, 48000, 1100.0, 3400.0)
	if cHighF2 <= cLowF2+120.0 {
		t.Fatalf("expected BrightFormant(F2) lift to raise F2 centroid: low=%.1fHz high=%.1fHz", cLowF2, cHighF2)
	}
}

func TestVoiceChangerBreathinessIsVoiceGated(t *testing.T) {
	vc := NewVoiceChanger(48000)
	vc.SetConfig(VoiceChangerParams{
		PitchSemitones:  6.0,
		FormantRatio:    1.15,
		ThroatFormant:   1.05,
		BrightFormant:   1.05,
		Breathiness:     1.0,
		Softness:        0.0,
		HighPassEnabled: false,
		HighPassHz:      150.0,
		DeEsser:         0.0,
	})
	vc.SetEnabled(true)

	// 1. Silence must produce strictly near-zero output (no hiss!)
	silence := make([]float32, 480*25)
	silOut := runVCFrames(t, vc, silence)
	maxSil := 0.0
	for _, s := range silOut[480*10:] {
		if a := math.Abs(float64(s)); a > maxSil {
			maxSil = a
		}
	}
	if maxSil > 1e-5 {
		t.Fatalf("expected zero breathiness hiss on silence, got peak %.6f", maxSil)
	}

	// 2. Voiced vowel with Breathiness=1.0 should have more 3-8kHz airy energy than Breathiness=0.0
	sig := synthVowel(48000, 480*40, 130.0, 650.0, 1800.0)
	vcDry := NewVoiceChanger(48000)
	cfgDry := vc.Config()
	cfgDry.Breathiness = 0.0
	vcDry.SetConfig(cfgDry)
	vcDry.SetEnabled(true)

	magDry := analyzeSpectrum(runVCFrames(t, vcDry, sig))
	magBreathy := analyzeSpectrum(runVCFrames(t, vc, sig))

	eDry := bandEnergy(magDry, 48000, 3200.0, 8000.0)
	eBreathy := bandEnergy(magBreathy, 48000, 3200.0, 8000.0)
	if eBreathy <= eDry*1.25 {
		t.Fatalf("expected Breathiness=1.0 to raise 3.2-8kHz voiced air energy: dry=%.6f breathy=%.6f", eDry, eBreathy)
	}
}

func TestVoiceChangerSoftnessHPFAndDeEsser(t *testing.T) {
	// 1. Variable 4th-order HPF @ 220Hz strongly attenuates 120Hz chest tone while passing 360Hz
	runTone := func(freqHz float64, hpOn bool, hpHz float64) float64 {
		vc := NewVoiceChanger(48000)
		vc.SetConfig(VoiceChangerParams{
			PitchSemitones:  0.0,
			FormantRatio:    1.0,
			ThroatFormant:   1.0,
			BrightFormant:   1.0,
			Breathiness:     0.0,
			Softness:        0.0,
			HighPassEnabled: hpOn,
			HighPassHz:      hpHz,
			DeEsser:         0.0,
		})
		vc.SetEnabled(true)
		sig := make([]float32, 480*30)
		for i := range sig {
			sig[i] = 0.3 * float32(math.Sin(2.0*math.Pi*freqHz*float64(i)/48000.0))
		}
		out := runVCFrames(t, vc, sig)
		sumSq := 0.0
		tail := out[len(out)-4800:]
		for _, s := range tail {
			sumSq += float64(s) * float64(s)
		}
		return math.Sqrt(sumSq / float64(len(tail)))
	}
	rms120Off := runTone(120.0, false, 220.0)
	rms120On := runTone(120.0, true, 220.0)
	if rms120On > rms120Off*0.25 {
		t.Fatalf("expected 4th-order HPF@220Hz to attenuate 120Hz by >12dB: off=%.4f on=%.4f", rms120Off, rms120On)
	}
	rms360Off := runTone(360.0, false, 220.0)
	rms360On := runTone(360.0, true, 220.0)
	if rms360On < rms360Off*0.85 {
		t.Fatalf("expected 4th-order HPF@220Hz to preserve 360Hz: off=%.4f on=%.4f", rms360Off, rms360On)
	}

	// 2. Softness reduces 1.5-3.0kHz buzz relative to fundamental
	sig := synthVowel(48000, 480*40, 140.0, 650.0, 2000.0)
	runSoft := func(soft float64) []float64 {
		vc := NewVoiceChanger(48000)
		vc.SetConfig(VoiceChangerParams{
			PitchSemitones:  7.0,
			FormantRatio:    1.15,
			ThroatFormant:   1.0,
			BrightFormant:   1.0,
			Breathiness:     0.0,
			Softness:        soft,
			HighPassEnabled: false,
			HighPassHz:      150.0,
			DeEsser:         0.0,
		})
		vc.SetEnabled(true)
		return analyzeSpectrum(runVCFrames(t, vc, sig))
	}
	magHard := runSoft(0.0)
	magSoft := runSoft(1.0)
	ratioHard := bandEnergy(magHard, 48000, 1500, 3000) / (bandEnergy(magHard, 48000, 180, 340) + 1e-12)
	ratioSoft := bandEnergy(magSoft, 48000, 1500, 3000) / (bandEnergy(magSoft, 48000, 180, 340) + 1e-12)
	if ratioSoft >= ratioHard*0.65 {
		t.Fatalf("expected Softness=1.0 to lower 1.5-3kHz/H1 ratio: hard=%.4f soft=%.4f", ratioHard, ratioSoft)
	}

	// 3. Dynamic De-Esser attenuates sharp 6.5kHz sibilance while preserving 300Hz vowel
	sibSig := make([]float32, 480*35)
	for i := range sibSig {
		tSec := float64(i) / 48000.0
		sibSig[i] = 0.25 * float32(math.Sin(2.0*math.Pi*6500.0*tSec))
	}
	runDeEss := func(deEss float64, input []float32) []float64 {
		vc := NewVoiceChanger(48000)
		vc.SetConfig(VoiceChangerParams{
			PitchSemitones:  0.0,
			FormantRatio:    1.0,
			ThroatFormant:   1.0,
			BrightFormant:   1.0,
			Breathiness:     0.0,
			Softness:        0.0,
			HighPassEnabled: false,
			HighPassHz:      120.0,
			DeEsser:         deEss,
		})
		vc.SetEnabled(true)
		return analyzeSpectrum(runVCFrames(t, vc, input))
	}
	magSibOff := runDeEss(0.0, sibSig)
	magSibOn := runDeEss(1.0, sibSig)
	eSibOff := bandEnergy(magSibOff, 48000, 5000.0, 9000.0)
	eSibOn := bandEnergy(magSibOn, 48000, 5000.0, 9000.0)
	if eSibOn > eSibOff*0.35 {
		t.Fatalf("expected DeEsser=1.0 to attenuate 6.5kHz sibilance strongly: off=%.4f on=%.4f", eSibOff, eSibOn)
	}
}

func TestVoiceChangerPresetMax(t *testing.T) {
	p := PresetMax.VoiceChangerParams
	clamped := p.Clamp()
	if clamped != p {
		t.Fatalf("PresetMax params were altered by Clamp: got %+v want %+v", clamped, p)
	}
	if p.PitchSemitones != 16.0 ||
		p.FormantRatio != 1.45 ||
		p.ThroatFormant != 1.30 ||
		p.BrightFormant != 1.30 ||
		p.Breathiness != 1.0 ||
		p.Softness != 1.0 ||
		!p.HighPassEnabled ||
		p.HighPassHz != 280.0 ||
		p.DeEsser != 1.0 {
		t.Fatalf("PresetMax does not match maximum parameter bounds: %+v", p)
	}

	vc := NewVoiceChanger(48000)
	vc.SetConfig(p)
	vc.SetEnabled(true)

	sig := synthVowel(48000, 480*30, 130.0, 650.0, 1700.0)
	out := runVCFrames(t, vc, sig)
	if len(out) != len(sig) {
		t.Fatalf("unexpected output length: got %d want %d", len(out), len(sig))
	}
}

func BenchmarkVoiceChanger10msFrame(b *testing.B) {
	vc := NewVoiceChanger(48000)
	vc.SetConfig(PresetFemaleNatural.VoiceChangerParams)
	vc.SetEnabled(true)
	frame := make([]float32, 480)
	for i := range frame {
		frame[i] = 0.3 * float32(math.Sin(2*math.Pi*140*float64(i)/48000))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vc.Process(frame)
	}
}
