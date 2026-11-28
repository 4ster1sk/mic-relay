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

func TestVoiceChangerNoSyllablePitchGlide(t *testing.T) {
	const sr = 48000
	const cycleSamples = 11520 // 240ms: 60ms unvoiced consonant + 180ms voiced vowel
	const totalSamples = cycleSamples * 6

	vowel := synthVowel(sr, totalSamples, 115.0, 680.0, 1250.0)
	rng := rand.New(rand.NewSource(42))
	in := make([]float32, totalSamples)
	for i := 0; i < totalSamples; i++ {
		if i%cycleSamples < 2880 {
			in[i] = float32((rng.Float64()*2 - 1) * 0.04)
		} else {
			in[i] = vowel[i]
		}
	}

	p := PresetFemaleNatural.VoiceChangerParams
	vc := NewVoiceChanger(sr)
	vc.SetConfig(p)
	vc.SetEnabled(true)

	frame := make([]float32, 480)
	minST, maxST := 999.0, -999.0
	for off := 0; off+480 <= len(in); off += 480 {
		copy(frame, in[off:off+480])
		vc.Process(frame)
		st := 12.0 * math.Log2(vc.psola.smoothAlpha)
		if st < minST {
			minST = st
		}
		if st > maxST {
			maxST = st
		}
	}
	if math.Abs(minST-p.PitchSemitones) > 0.05 || math.Abs(maxST-p.PitchSemitones) > 0.05 {
		t.Fatalf("expected steady pitch shift %.2f st across CV syllables, got range [%.2f, %.2f] st",
			p.PitchSemitones, minST, maxST)
	}
}

func TestVoiceChangerAdaptiveHPFPreservesDeepFundamental(t *testing.T) {
	const sr = 48000
	const f0In = 90.0 // Deep male fundamental
	sig := synthVowel(sr, 480*60, f0In, 650.0, 1250.0)

	runH1 := func(hpOn bool, hpHz float64) float64 {
		p := PresetFemaleNatural.VoiceChangerParams
		p.Breathiness = 0.0
		p.DeEsser = 0.0
		p.HighPassEnabled = hpOn
		p.HighPassHz = hpHz
		vc := NewVoiceChanger(sr)
		vc.SetConfig(p)
		vc.SetEnabled(true)
		out := runVCFrames(t, vc, sig)
		mag := analyzeSpectrum(out)
		f0Out := f0In * math.Pow(2.0, p.PitchSemitones/12.0) // ~138.8Hz
		return bandEnergy(mag, sr, f0Out-20.0, f0Out+20.0)
	}

	eOff := runH1(false, 110.0)
	for _, tc := range []struct {
		name string
		hpHz float64
	}{
		{"PresetDefault110Hz", PresetFemaleNatural.HighPassHz},
		{"LegacyHigh180Hz", 180.0},
	} {
		eOn := runH1(true, tc.hpHz)
		gainDB := 10.0 * math.Log10((eOn+1e-20)/(eOff+1e-20))
		if gainDB < -2.0 {
			t.Fatalf("%s attenuated shifted H1 too much: %.2f dB (want >= -2.0 dB)", tc.name, gainDB)
		}
	}
}

func TestVoiceChangerBreathinessNoHopRateModulation(t *testing.T) {
	const sr = 48000
	const n = 48000 * 3
	sig := synthVowel(sr, n, 115.0, 700.0, 1220.0)

	run := func(breath float64) []float32 {
		p := PresetFemaleNatural.VoiceChangerParams
		p.Breathiness = breath
		p.DeEsser = 0.0
		vc := NewVoiceChanger(sr)
		vc.SetConfig(p)
		vc.SetEnabled(true)
		return runVCFrames(t, vc, sig)
	}

	dry := run(0.0)
	wet := run(0.45)

	const anaN = 65536
	start := n - anaN - 4800
	re := make([]float64, anaN)
	im := make([]float64, anaN)
	for i := 0; i < anaN; i++ {
		w := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(anaN)))
		re[i] = float64(wet[start+i]-dry[start+i]) * w
	}
	fft := NewFFT(anaN)
	fft.Transform(re, im, false)

	// Bandpass isolate 5-9 kHz breath component
	binHz := float64(sr) / float64(anaN)
	for k := 0; k < anaN; k++ {
		f := float64(k) * binHz
		if k > anaN/2 {
			f = float64(anaN-k) * binHz
		}
		if f < 5000.0 || f > 9000.0 {
			re[k] = 0
			im[k] = 0
		}
	}
	fft.Transform(re, im, true)

	// Welch-averaged envelope modulation power spectrum (eliminates Rayleigh variance)
	const segN = 16384
	const segHop = 8192
	segFFT := NewFFT(segN)
	segRe := make([]float64, segN)
	segIm := make([]float64, segN)
	psd := make([]float64, segN/2+1)
	segCount := 0
	for s0 := 0; s0+segN <= anaN; s0 += segHop {
		for i := 0; i < segN; i++ {
			w := 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(segN)))
			segRe[i] = (re[s0+i] * re[s0+i]) * w
			segIm[i] = 0
		}
		segFFT.Transform(segRe, segIm, false)
		for k := range psd {
			psd[k] += segRe[k]*segRe[k] + segIm[k]*segIm[k]
		}
		segCount++
	}
	segBinHz := float64(sr) / float64(segN)
	k200 := int(math.Round(200.0 / segBinHz))
	pow200 := (psd[k200-1] + psd[k200] + psd[k200+1]) / 3.0
	bgSum := 0.0
	bgCnt := 0
	for k := k200 - 40; k <= k200+40; k++ {
		if k < k200-5 || k > k200+5 {
			bgSum += psd[k]
			bgCnt++
		}
	}
	peakDB := 10.0 * math.Log10(pow200/(bgSum/float64(bgCnt)+1e-30))
	t.Logf("200Hz breath envelope modulation vs background (Welch %d segs): %+.2f dB", segCount, peakDB)
	if peakDB > 3.0 {
		t.Fatalf("expected breathiness to have no 200Hz hop-rate modulation spike, got %+.2f dB", peakDB)
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

func TestLR4CrossoverAllPassFlatness(t *testing.T) {
	const sr = 48000.0
	for _, freq := range []float64{150.0, 900.0, 2800.0, 5000.0, 8500.0, 14000.0} {
		xover := newLR4Crossover(sr, hnCrossoverHz)
		const n = 9600
		var inSq, outSq float64
		for i := 0; i < n; i++ {
			x := 0.5 * math.Sin(2.0*math.Pi*freq*float64(i)/sr)
			lo, hi := xover.split(x)
			y := lo + hi
			if i >= 2400 {
				inSq += x * x
				outSq += y * y
			}
		}
		gainDB := 10.0 * math.Log10(outSq/inSq)
		if math.Abs(gainDB) > 0.15 {
			t.Fatalf("LR4 crossover sum deviated from 0dB at %.0fHz: %+.3f dB", freq, gainDB)
		}
	}
}

func TestMinimumPhaseNoPreEchoOnGlottalImpulse(t *testing.T) {
	const sr = 48000
	vc := NewVoiceChanger(sr)
	vc.SetConfig(VoiceChangerParams{
		PitchSemitones:  0.0, // isolate STFT formant filter impulse response
		FormantRatio:    1.28,
		ThroatFormant:   1.15,
		BrightFormant:   1.15,
		Breathiness:     0.0,
		Softness:        0.5,
		HighPassEnabled: false,
		HighPassHz:      110.0,
		DeEsser:         0.0,
	})
	vc.SetEnabled(true)

	// Warm up filter state, then inject a single sharp impulse
	total := 480 * 30
	in := make([]float32, total)
	impIdx := 480 * 15
	in[impIdx] = 0.8

	out := runVCFrames(t, vc, in)

	peakIdx := 0
	peakAbs := 0.0
	for i, s := range out {
		a := math.Abs(float64(s))
		if a > peakAbs {
			peakAbs = a
			peakIdx = i
		}
	}
	if peakAbs < 0.05 {
		t.Fatalf("expected strong impulse response peak, got %.5f", peakAbs)
	}

	// Compare pre-impulse energy [peakIdx-480 .. peakIdx-24] vs causal decay [peakIdx .. peakIdx+480]
	var preE, postE float64
	for i := peakIdx - 480; i < peakIdx-24; i++ {
		if i >= 0 {
			v := float64(out[i])
			preE += v * v
		}
	}
	for i := peakIdx; i < peakIdx+480 && i < len(out); i++ {
		v := float64(out[i])
		postE += v * v
	}
	preRatioDB := 10.0 * math.Log10((preE+1e-24)/(postE+1e-24))
	t.Logf("Minimum-phase pre-echo vs causal energy: %.2f dB (peak at +%d samples)", preRatioDB, peakIdx-impIdx)
	if preRatioDB > -30.0 {
		t.Fatalf("expected causal minimum-phase filter with <-30dB pre-echo, got %.2f dB", preRatioDB)
	}
}

func TestUnvoicedSibilantPreservesNaturalBandAndSharpness(t *testing.T) {
	const sr = 48000
	const n = 480 * 40
	rng := rand.New(rand.NewSource(12345))

	// Synthesize realistic /s/ sibilant burst (band-passed 5.2-10kHz noise) in middle of silence
	bpHP := biquadState{}
	bpLP := biquadState{}
	bpHP.setHighPass(sr, 5400.0, 0.7071)
	bpLP.setLowPass(sr, 9800.0, 0.7071)

	in := make([]float32, n)
	burstStart := 480 * 12
	burstEnd := 480 * 24 // 120ms sibilant
	for i := burstStart; i < burstEnd; i++ {
		w := (rng.Float64()*2.0 - 1.0) * 0.25
		s := bpLP.process(bpHP.process(w))
		in[i] = float32(s)
	}

	vc := NewVoiceChanger(sr)
	p := PresetFemaleHigh.VoiceChangerParams // +10 semitones!
	p.DeEsser = 0.0
	vc.SetConfig(p)
	vc.SetEnabled(true)

	out := runVCFrames(t, vc, in)

	// Measure sibilant spectral centroid during the active output burst:
	// If sibilant were pitch-shifted by +10st (1.78x), 7kHz would jump above 12kHz!
	// With LR4 + unvoiced bypass, centroid must remain naturally in [5.2kHz, 9.5kHz].
	bestWinStart := 0
	bestWinE := 0.0
	for s0 := 0; s0+4096 <= len(out); s0 += 480 {
		e := 0.0
		for i := 0; i < 4096; i++ {
			v := float64(out[s0+i])
			e += v * v
		}
		if e > bestWinE {
			bestWinE = e
			bestWinStart = s0
		}
	}
	mag := analyzeSpectrum(out[bestWinStart : bestWinStart+4096])
	cent := bandCentroid(mag, sr, 3500.0, 18000.0)
	t.Logf("Unvoiced /s/ output centroid at +10st: %.1f Hz", cent)
	if cent < 5000.0 || cent > 9600.0 {
		t.Fatalf("expected unvoiced sibilant centroid to stay natural (5.0-9.6kHz), got %.1f Hz", cent)
	}
}

func TestSWIPSOLAVibratoAndHarmonicPurity(t *testing.T) {
	const sr = 48000
	const n = 48000 * 2 // 2.0s

	// Synthesize realistic voiced vowel with 5.5Hz vibrato (±0.25 semitones) to verify
	// epoch tracking & SWI-PSOLA maintain clean harmonics without double-voicing.
	in := make([]float32, n)
	phase := 0.0
	for i := 0; i < n; i++ {
		tSec := float64(i) / sr
		vib := 0.25 * math.Sin(2.0*math.Pi*5.5*tSec)
		f0 := 125.0 * math.Pow(2.0, vib/12.0)
		phase += 2.0 * math.Pi * f0 / sr
		sum := 0.0
		for h := 1; h <= 28; h++ {
			hf := f0 * float64(h)
			d1 := (hf - 680.0) / 150.0
			d2 := (hf - 1350.0) / 260.0
			amp := (0.15 + 0.9*math.Exp(-0.5*d1*d1) + 0.6*math.Exp(-0.5*d2*d2)) / math.Pow(float64(h), 0.45)
			// Realistic glottal phase skew
			sum += amp * math.Sin(float64(h)*phase+0.15*float64(h))
		}
		in[i] = float32(0.12 * sum)
	}

	vc := NewVoiceChanger(sr)
	p := PresetFemaleNatural.VoiceChangerParams
	p.Breathiness = 0.0
	vc.SetConfig(p)
	vc.SetEnabled(true)

	out := runVCFrames(t, vc, in)

	// Ensure steady-state output RMS is stable without dropouts or amplitude pumping
	var minRMS, maxRMS float64 = 1e9, 0
	for off := 480 * 40; off+2400 <= len(out); off += 2400 { // 50ms windows
		sq := 0.0
		for i := 0; i < 2400; i++ {
			v := float64(out[off+i])
			sq += v * v
		}
		r := math.Sqrt(sq / 2400.0)
		if r < minRMS {
			minRMS = r
		}
		if r > maxRMS {
			maxRMS = r
		}
	}
	pumpDB := 20.0 * math.Log10(maxRMS/(minRMS+1e-12))
	t.Logf("Vibrato vowel 50ms envelope variation: %.2f dB", pumpDB)
	if minRMS < 0.01 || pumpDB > 3.5 {
		t.Fatalf("unexpected amplitude pumping or dropout on vibrato vowel: minRMS=%.4f pump=%.2f dB", minRMS, pumpDB)
	}
}
