package dsp

import (
	"math"
	"sync"
	"sync/atomic"
)

const (
	vcFFTSize   = 1024
	vcHopSize   = 240 // 5ms @ 48kHz (exact divisor of 480-sample 10ms frame)
	vcHalfBins  = vcFFTSize/2 + 1
	vcCepOrder  = 38 // cepstral liftering order for smooth vocal tract envelope @ 48kHz
	vcCepTaper  = 6
)

type VoiceChangerPreset struct {
	Name            string
	PitchSemitones  float64
	FormantRatio    float64
	HighPassEnabled bool
}

var (
	PresetFemaleNatural = VoiceChangerPreset{
		Name:            "女声（ナチュラル）",
		PitchSemitones:  5.0,
		FormantRatio:    1.18,
		HighPassEnabled: true,
	}
	PresetFemaleHigh = VoiceChangerPreset{
		Name:            "女声（高め・アニメ調）",
		PitchSemitones:  8.0,
		FormantRatio:    1.28,
		HighPassEnabled: true,
	}
	PresetAndrogynous = VoiceChangerPreset{
		Name:            "中性・少年声",
		PitchSemitones:  3.0,
		FormantRatio:    1.10,
		HighPassEnabled: false,
	}
)

// VoiceChanger performs real-time Pure-Go pitch + independent formant shifting
// using a phase-vocoder STFT with cepstral spectral envelope warping.
type VoiceChanger struct {
	mu             sync.Mutex
	sampleRate     float64
	enabled        atomic.Bool
	wasEnabled     bool
	pitchSemitones float64
	formantRatio   float64
	hpEnabled      bool

	fft      *FFT
	window   []float64
	olaNorm  []float64 // periodic overlap-add normalization (length vcHopSize)

	inRing   []float64 // length vcFFTSize
	outOLA   []float64 // length vcFFTSize
	hopIn    []float64 // accumulates up to vcHopSize
	hopOut   []float64 // ready output samples

	// Preallocated FFT & vocoder scratch buffers (zero allocations in Process)
	re         []float64
	im         []float64
	cepRe      []float64
	cepIm      []float64
	mag        []float64
	env        []float64
	prevPhase  []float64
	synPhase   []float64
	synMag     []float64
	synFreq    []float64
	dryCopy    []float32

	// 2nd-order Butterworth HPF @ 130Hz state
	hpB0, hpB1, hpB2 float64
	hpA1, hpA2       float64
	hpX1, hpX2       float64
	hpY1, hpY2       float64
}

func NewVoiceChanger(sampleRate int) *VoiceChanger {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	vc := &VoiceChanger{
		sampleRate:     float64(sampleRate),
		pitchSemitones: PresetFemaleNatural.PitchSemitones,
		formantRatio:   PresetFemaleNatural.FormantRatio,
		hpEnabled:      PresetFemaleNatural.HighPassEnabled,
		fft:            NewFFT(vcFFTSize),
		window:         make([]float64, vcFFTSize),
		olaNorm:        make([]float64, vcHopSize),
		inRing:         make([]float64, vcFFTSize),
		outOLA:         make([]float64, vcFFTSize),
		hopIn:          make([]float64, 0, vcHopSize*2),
		hopOut:         make([]float64, 0, vcHopSize*4),
		re:             make([]float64, vcFFTSize),
		im:             make([]float64, vcFFTSize),
		cepRe:          make([]float64, vcFFTSize),
		cepIm:          make([]float64, vcFFTSize),
		mag:            make([]float64, vcHalfBins),
		env:            make([]float64, vcHalfBins),
		prevPhase:      make([]float64, vcHalfBins),
		synPhase:       make([]float64, vcHalfBins),
		synMag:         make([]float64, vcHalfBins),
		synFreq:        make([]float64, vcHalfBins),
		dryCopy:        make([]float32, 0, 960),
	}

	// Hann window
	for i := 0; i < vcFFTSize; i++ {
		vc.window[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(vcFFTSize)))
	}
	// Exact periodic overlap-add normalization for squared Hann window at hop=vcHopSize
	for pos := 0; pos < vcHopSize; pos++ {
		sum := 0.0
		for k := pos; k < vcFFTSize; k += vcHopSize {
			w := vc.window[k]
			sum += w * w
		}
		if sum < 1e-6 {
			sum = 1.0
		}
		vc.olaNorm[pos] = 1.0 / sum
	}

	vc.initHighPass(130.0)
	return vc
}

func (vc *VoiceChanger) initHighPass(cutoffHz float64) {
	w0 := 2.0 * math.Pi * cutoffHz / vc.sampleRate
	cosW := math.Cos(w0)
	sinW := math.Sin(w0)
	alpha := sinW / math.Sqrt2

	b0 := (1.0 + cosW) / 2.0
	b1 := -(1.0 + cosW)
	b2 := (1.0 + cosW) / 2.0
	a0 := 1.0 + alpha
	a1 := -2.0 * cosW
	a2 := 1.0 - alpha

	vc.hpB0 = b0 / a0
	vc.hpB1 = b1 / a0
	vc.hpB2 = b2 / a0
	vc.hpA1 = a1 / a0
	vc.hpA2 = a2 / a0
}

func (vc *VoiceChanger) SetEnabled(on bool) {
	vc.enabled.Store(on)
}

func (vc *VoiceChanger) IsEnabled() bool {
	return vc.enabled.Load()
}

func (vc *VoiceChanger) SetParams(pitchSemitones, formantRatio float64, hpEnabled bool) {
	if pitchSemitones < -12 {
		pitchSemitones = -12
	}
	if pitchSemitones > 12 {
		pitchSemitones = 12
	}
	if formantRatio < 0.75 {
		formantRatio = 0.75
	}
	if formantRatio > 1.45 {
		formantRatio = 1.45
	}
	vc.mu.Lock()
	vc.pitchSemitones = pitchSemitones
	vc.formantRatio = formantRatio
	vc.hpEnabled = hpEnabled
	vc.mu.Unlock()
}

func (vc *VoiceChanger) Params() (pitchSemitones, formantRatio float64, hpEnabled bool) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.pitchSemitones, vc.formantRatio, vc.hpEnabled
}

func (vc *VoiceChanger) resetLocked() {
	clear(vc.inRing)
	clear(vc.outOLA)
	vc.hopIn = vc.hopIn[:0]
	vc.hopOut = vc.hopOut[:0]
	clear(vc.prevPhase)
	clear(vc.synPhase)
	vc.hpX1, vc.hpX2, vc.hpY1, vc.hpY2 = 0, 0, 0, 0
}

// Process modifies mono 48kHz float32 samples in-place.
func (vc *VoiceChanger) Process(samples []float32) {
	if vc == nil || len(samples) == 0 {
		return
	}
	wantOn := vc.enabled.Load()

	vc.mu.Lock()
	defer vc.mu.Unlock()

	if !wantOn && !vc.wasEnabled {
		return
	}

	// Preserve dry signal if crossfading ON <-> OFF
	needFade := wantOn != vc.wasEnabled
	if needFade {
		if cap(vc.dryCopy) < len(samples) {
			vc.dryCopy = make([]float32, len(samples))
		} else {
			vc.dryCopy = vc.dryCopy[:len(samples)]
		}
		copy(vc.dryCopy, samples)
	}

	pitchRatio := math.Pow(2.0, vc.pitchSemitones/12.0)
	formantRatio := vc.formantRatio
	hpOn := vc.hpEnabled

	// Feed samples through hop-based STFT processor
	for _, s := range samples {
		vc.hopIn = append(vc.hopIn, float64(s))
		if len(vc.hopIn) >= vcHopSize {
			vc.processHopLocked(vc.hopIn[:vcHopSize], pitchRatio, formantRatio)
			copy(vc.hopIn, vc.hopIn[vcHopSize:])
			vc.hopIn = vc.hopIn[:len(vc.hopIn)-vcHopSize]
		}
	}

	// Pull processed samples
	n := len(samples)
	if len(vc.hopOut) >= n {
		for i := 0; i < n; i++ {
			y := vc.hopOut[i]
			if hpOn {
				y = vc.applyHPLocked(y)
			}
			samples[i] = float32(y)
		}
		copy(vc.hopOut, vc.hopOut[n:])
		vc.hopOut = vc.hopOut[:len(vc.hopOut)-n]
	} else {
		// Pad any initial startup latency with silence
		offset := n - len(vc.hopOut)
		for i := 0; i < offset; i++ {
			samples[i] = 0
		}
		for i := 0; i < len(vc.hopOut); i++ {
			y := vc.hopOut[i]
			if hpOn {
				y = vc.applyHPLocked(y)
			}
			samples[offset+i] = float32(y)
		}
		vc.hopOut = vc.hopOut[:0]
	}

	if needFade {
		invN := float32(1.0) / float32(n)
		if wantOn {
			// OFF -> ON crossfade
			for i := 0; i < n; i++ {
				alpha := float32(i) * invN
				samples[i] = vc.dryCopy[i]*(1-alpha) + samples[i]*alpha
			}
			vc.wasEnabled = true
		} else {
			// ON -> OFF crossfade
			for i := 0; i < n; i++ {
				alpha := float32(i) * invN
				samples[i] = samples[i]*(1-alpha) + vc.dryCopy[i]*alpha
			}
			vc.wasEnabled = false
			vc.resetLocked()
		}
	}
}

func (vc *VoiceChanger) applyHPLocked(x float64) float64 {
	y := vc.hpB0*x + vc.hpB1*vc.hpX1 + vc.hpB2*vc.hpX2 - vc.hpA1*vc.hpY1 - vc.hpA2*vc.hpY2
	vc.hpX2 = vc.hpX1
	vc.hpX1 = x
	vc.hpY2 = vc.hpY1
	vc.hpY1 = y
	return y
}

func wrapPhase(p float64) float64 {
	p = math.Mod(p+math.Pi, 2.0*math.Pi)
	if p < 0 {
		p += 2.0 * math.Pi
	}
	return p - math.Pi
}

func (vc *VoiceChanger) processHopLocked(inHop []float64, pitchRatio, formantRatio float64) {
	// Shift input ring left by vcHopSize and append new hop
	copy(vc.inRing, vc.inRing[vcHopSize:])
	copy(vc.inRing[vcFFTSize-vcHopSize:], inHop)

	// Windowed forward FFT
	for i := 0; i < vcFFTSize; i++ {
		vc.re[i] = vc.inRing[i] * vc.window[i]
		vc.im[i] = 0
	}
	vc.fft.Transform(vc.re, vc.im, false)

	// Compute magnitudes & log-magnitudes for cepstral envelope
	for k := 0; k < vcHalfBins; k++ {
		m := math.Hypot(vc.re[k], vc.im[k])
		vc.mag[k] = m
		lm := math.Log(math.Max(m, 1e-7))
		vc.cepRe[k] = lm
		vc.cepIm[k] = 0
		if k > 0 && k < vcHalfBins-1 {
			vc.cepRe[vcFFTSize-k] = lm
			vc.cepIm[vcFFTSize-k] = 0
		}
	}

	// Real cepstrum liftering to extract smooth vocal-tract spectral envelope
	vc.fft.Transform(vc.cepRe, vc.cepIm, true)
	for q := 0; q < vcFFTSize; q++ {
		dist := q
		if dist > vcFFTSize/2 {
			dist = vcFFTSize - dist
		}
		if dist > vcCepOrder {
			vc.cepRe[q] = 0
			vc.cepIm[q] = 0
		} else if dist > vcCepOrder-vcCepTaper {
			t := float64(vcCepOrder-dist) / float64(vcCepTaper)
			vc.cepRe[q] *= t
			vc.cepIm[q] = 0
		} else {
			vc.cepIm[q] = 0
		}
	}
	vc.fft.Transform(vc.cepRe, vc.cepIm, false)
	for k := 0; k < vcHalfBins; k++ {
		e := math.Exp(vc.cepRe[k])
		if e < 1e-6 {
			e = 1e-6
		}
		vc.env[k] = e
	}

	// Phase vocoder pitch shift on flattened excitation spectrum
	clear(vc.synMag)
	clear(vc.synFreq)
	expectedPhaseInc := 2.0 * math.Pi * float64(vcHopSize) / float64(vcFFTSize)

	for k := 0; k < vcHalfBins; k++ {
		phase := math.Atan2(vc.im[k], vc.re[k])
		delta := phase - vc.prevPhase[k]
		vc.prevPhase[k] = phase

		expected := float64(k) * expectedPhaseInc
		dev := wrapPhase(delta - expected)
		trueFreq := expected + dev

		dst := int(math.Round(float64(k) * pitchRatio))
		if dst >= 0 && dst < vcHalfBins {
			exc := vc.mag[k] / vc.env[k]
			// Keep dominant excitation component per destination bin to avoid phase cancellation
			if exc > vc.synMag[dst] {
				vc.synMag[dst] = exc
				vc.synFreq[dst] = trueFreq * pitchRatio
			}
		}
	}

	// Apply independent formant-warped spectral envelope & reconstruct complex spectrum
	for j := 0; j < vcHalfBins; j++ {
		vc.synPhase[j] = wrapPhase(vc.synPhase[j] + vc.synFreq[j])

		srcF := float64(j) / formantRatio
		var warpedEnv float64
		if srcF >= float64(vcHalfBins-1) {
			warpedEnv = vc.env[vcHalfBins-1] * 0.5
		} else {
			idx := int(srcF)
			frac := srcF - float64(idx)
			warpedEnv = vc.env[idx]*(1.0-frac) + vc.env[idx+1]*frac
		}

		finalMag := vc.synMag[j] * warpedEnv
		cr := finalMag * math.Cos(vc.synPhase[j])
		ci := finalMag * math.Sin(vc.synPhase[j])
		vc.re[j] = cr
		vc.im[j] = ci
		if j > 0 && j < vcHalfBins-1 {
			vc.re[vcFFTSize-j] = cr
			vc.im[vcFFTSize-j] = -ci
		}
	}
	vc.im[0] = 0
	vc.im[vcHalfBins-1] = 0

	// Inverse FFT + synthesis window + overlap-add
	vc.fft.Transform(vc.re, vc.im, true)
	for i := 0; i < vcFFTSize; i++ {
		vc.outOLA[i] += vc.re[i] * vc.window[i]
	}

	// Emit normalized hop and shift OLA accumulator
	for i := 0; i < vcHopSize; i++ {
		vc.hopOut = append(vc.hopOut, vc.outOLA[i]*vc.olaNorm[i])
	}
	copy(vc.outOLA, vc.outOLA[vcHopSize:])
	clear(vc.outOLA[vcFFTSize-vcHopSize:])
}
