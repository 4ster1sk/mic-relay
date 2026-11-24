package dsp

import (
	"math"
	"sync"
	"sync/atomic"
)

const (
	vcFFTSize  = 2048
	vcHopSize  = 240 // 5ms @ 48kHz (exact divisor of 480-sample 10ms frame; ~88% overlap)
	vcHalfBins = vcFFTSize/2 + 1
	vcCepOrder = 52 // cepstral liftering order for 2048-pt FFT @ 48kHz (~1.1ms quefrency)
	vcCepTaper = 10
	vcTEIters  = 4 // True Envelope iterations
)

type VoiceChangerPreset struct {
	Name            string
	PitchSemitones  float64
	FormantRatio    float64
	Softness        float64
	HighPassEnabled bool
}

var (
	PresetFemaleNatural = VoiceChangerPreset{
		Name:            "女声（ナチュラル）",
		PitchSemitones:  4.5,
		FormantRatio:    1.16,
		Softness:        0.45,
		HighPassEnabled: true,
	}
	PresetFemaleHigh = VoiceChangerPreset{
		Name:            "女声（高め・アニメ調）",
		PitchSemitones:  7.0,
		FormantRatio:    1.24,
		Softness:        0.55,
		HighPassEnabled: true,
	}
	PresetAndrogynous = VoiceChangerPreset{
		Name:            "中性・少年声",
		PitchSemitones:  2.5,
		FormantRatio:    1.08,
		Softness:        0.25,
		HighPassEnabled: false,
	}
)

// VoiceChanger performs real-time Pure-Go female voice conversion (DSP v2) using:
//   - 2048-pt STFT (23.4Hz bin resolution @ 48kHz)
//   - Identity Phase Locking (Laroche & Dolson) to eliminate robotic phasiness
//   - True Envelope (iterated cepstral upper-hull) for crisp vowel formants
//   - Non-linear formant warping (protects >5kHz sibilants from harshness)
//   - Voiced/Unvoiced (V/UV) blending to preserve crisp consonants
//   - Chest-resonance HPF + softness spectral shaping
type VoiceChanger struct {
	mu             sync.Mutex
	sampleRate     float64
	enabled        atomic.Bool
	wasEnabled     bool
	pitchSemitones float64
	formantRatio   float64
	softness       float64
	hpEnabled      bool

	fft     *FFT
	window  []float64
	olaNorm []float64 // periodic overlap-add normalization (length vcHopSize)

	inRing []float64 // length vcFFTSize
	outOLA []float64 // length vcFFTSize
	hopIn  []float64
	hopOut []float64

	// Preallocated scratch buffers (zero allocations in Process)
	re         []float64
	im         []float64
	cepRe      []float64
	cepIm      []float64
	logMag     []float64
	envLog     []float64
	mag        []float64
	anaPhase   []float64
	env        []float64
	prevPhase  []float64
	synPhase   []float64
	synMag     []float64
	synOutRe   []float64
	synOutIm   []float64
	peaks      []int
	voicedEWMA float64
	dryCopy    []float32

	// 2nd-order Butterworth HPF @ 145Hz state
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
		softness:       PresetFemaleNatural.Softness,
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
		logMag:         make([]float64, vcHalfBins),
		envLog:         make([]float64, vcHalfBins),
		mag:            make([]float64, vcHalfBins),
		anaPhase:       make([]float64, vcHalfBins),
		env:            make([]float64, vcHalfBins),
		prevPhase:      make([]float64, vcHalfBins),
		synPhase:       make([]float64, vcHalfBins),
		synMag:         make([]float64, vcHalfBins),
		synOutRe:       make([]float64, vcHalfBins),
		synOutIm:       make([]float64, vcHalfBins),
		peaks:          make([]int, 0, 256),
		voicedEWMA:     1.0,
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

	vc.initHighPass(145.0)
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

func (vc *VoiceChanger) SetParams(pitchSemitones, formantRatio, softness float64, hpEnabled bool) {
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
	if softness < 0 {
		softness = 0
	}
	if softness > 1 {
		softness = 1
	}
	vc.mu.Lock()
	vc.pitchSemitones = pitchSemitones
	vc.formantRatio = formantRatio
	vc.softness = softness
	vc.hpEnabled = hpEnabled
	vc.mu.Unlock()
}

func (vc *VoiceChanger) Params() (pitchSemitones, formantRatio, softness float64, hpEnabled bool) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.pitchSemitones, vc.formantRatio, vc.softness, vc.hpEnabled
}

func (vc *VoiceChanger) resetLocked() {
	clear(vc.inRing)
	clear(vc.outOLA)
	vc.hopIn = vc.hopIn[:0]
	vc.hopOut = vc.hopOut[:0]
	clear(vc.prevPhase)
	clear(vc.synPhase)
	vc.voicedEWMA = 1.0
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
	softness := vc.softness
	hpOn := vc.hpEnabled

	for _, s := range samples {
		vc.hopIn = append(vc.hopIn, float64(s))
		if len(vc.hopIn) >= vcHopSize {
			vc.processHopLocked(vc.hopIn[:vcHopSize], pitchRatio, formantRatio, softness)
			copy(vc.hopIn, vc.hopIn[vcHopSize:])
			vc.hopIn = vc.hopIn[:len(vc.hopIn)-vcHopSize]
		}
	}

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
			for i := 0; i < n; i++ {
				alpha := float32(i) * invN
				samples[i] = vc.dryCopy[i]*(1-alpha) + samples[i]*alpha
			}
			vc.wasEnabled = true
		} else {
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

// computeTrueEnvelopeLocked estimates the upper spectral envelope of harmonic peaks
// via iterated cepstral liftering (True Envelope), avoiding the muffled valleys of plain cepstrum.
func (vc *VoiceChanger) computeTrueEnvelopeLocked() {
	copy(vc.envLog, vc.logMag)

	for iter := 0; iter < vcTEIters; iter++ {
		for k := 0; k < vcHalfBins; k++ {
			v := vc.envLog[k]
			vc.cepRe[k] = v
			vc.cepIm[k] = 0
			if k > 0 && k < vcHalfBins-1 {
				vc.cepRe[vcFFTSize-k] = v
				vc.cepIm[vcFFTSize-k] = 0
			}
		}
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
			smoothed := vc.cepRe[k]
			if iter < vcTEIters-1 && vc.logMag[k] > smoothed {
				vc.envLog[k] = vc.logMag[k]
			} else {
				vc.envLog[k] = smoothed
			}
		}
	}

	for k := 0; k < vcHalfBins; k++ {
		e := math.Exp(vc.envLog[k])
		if e < 1e-6 {
			e = 1e-6
		}
		vc.env[k] = e
	}
}

// nonLinearFormantSrc maps a target frequency bin to its source envelope coordinate,
// shifting F1/F2/F3 (0..3800Hz) strongly while smoothly returning to 1.0x above 6000Hz
// so sibilants ("s", "sh") stay natural rather than turning shrill/metallic.
func (vc *VoiceChanger) nonLinearFormantSrc(bin int, formantRatio float64) float64 {
	binHz := vc.sampleRate / float64(vcFFTSize)
	freq := float64(bin) * binHz

	const lowKnee = 3800.0
	const highKnee = 6200.0
	var effRatio float64
	switch {
	case freq <= lowKnee:
		effRatio = formantRatio
	case freq >= highKnee:
		effRatio = 1.0
	default:
		t := (freq - lowKnee) / (highKnee - lowKnee)
		// Smooth cosine transition
		blend := 0.5 * (1.0 + math.Cos(math.Pi*t))
		effRatio = 1.0 + (formantRatio-1.0)*blend
	}
	return float64(bin) / effRatio
}

// softnessGain applies subtle female vocal tract shaping:
// gently trims male chest resonance (~180Hz), softens harsh 1.6-2.8kHz buzz,
// and adds light airy presence around 5-7kHz.
func (vc *VoiceChanger) softnessGain(bin int, softness float64) float64 {
	if softness <= 0.001 {
		return 1.0
	}
	freq := float64(bin) * vc.sampleRate / float64(vcFFTSize)
	gainDB := 0.0

	// 1. Gentle chest boom attenuation around 180Hz
	if freq > 80 && freq < 340 {
		d := (freq - 180.0) / 90.0
		gainDB -= 3.5 * softness * math.Exp(-0.5*d*d)
	}
	// 2. Soften harsh upper-mid vocal buzz (1.6-2.8kHz)
	if freq > 1000 && freq < 3600 {
		d := (freq - 2100.0) / 650.0
		gainDB -= 2.0 * softness * math.Exp(-0.5*d*d)
	}
	// 3. Light feminine air/breathiness around 5.2kHz
	if freq > 3500 && freq < 8500 {
		d := (freq - 5400.0) / 1400.0
		gainDB += 2.2 * softness * math.Exp(-0.5*d*d)
	}
	return math.Pow(10.0, gainDB/20.0)
}

func (vc *VoiceChanger) processHopLocked(inHop []float64, pitchRatio, formantRatio, softness float64) {
	copy(vc.inRing, vc.inRing[vcHopSize:])
	copy(vc.inRing[vcFFTSize-vcHopSize:], inHop)

	// Windowed forward FFT
	for i := 0; i < vcFFTSize; i++ {
		vc.re[i] = vc.inRing[i] * vc.window[i]
		vc.im[i] = 0
	}
	vc.fft.Transform(vc.re, vc.im, false)

	// Magnitudes, phases, and V/UV energy ratio (low voiced band 90-1400Hz vs high fricative band 3200-9000Hz)
	binHz := vc.sampleRate / float64(vcFFTSize)
	lowStart := int(90.0 / binHz)
	lowEnd := int(1400.0 / binHz)
	highStart := int(3200.0 / binHz)
	highEnd := int(9000.0 / binHz)
	if highEnd >= vcHalfBins {
		highEnd = vcHalfBins - 1
	}

	var lowEnergy, highEnergy, maxMag float64
	for k := 0; k < vcHalfBins; k++ {
		m := math.Hypot(vc.re[k], vc.im[k])
		vc.mag[k] = m
		vc.anaPhase[k] = math.Atan2(vc.im[k], vc.re[k])
		vc.logMag[k] = math.Log(math.Max(m, 1e-7))
		if m > maxMag {
			maxMag = m
		}
		e := m * m
		if k >= lowStart && k <= lowEnd {
			lowEnergy += e
		} else if k >= highStart && k <= highEnd {
			highEnergy += e
		}
	}

	// Smoothly estimate voicedness in [0, 1]
	voicedInstant := 1.0
	if lowEnergy+highEnergy > 1e-10 {
		ratio := lowEnergy / (lowEnergy + highEnergy)
		// Voiced vowels have >0.55 low-band energy; unvoiced "s/sh/t" have <0.25
		voicedInstant = (ratio - 0.20) / 0.40
		if voicedInstant < 0 {
			voicedInstant = 0
		} else if voicedInstant > 1 {
			voicedInstant = 1
		}
	}
	vc.voicedEWMA = 0.7*vc.voicedEWMA + 0.3*voicedInstant
	voiced := vc.voicedEWMA

	// Extract True Envelope (upper harmonic hull)
	vc.computeTrueEnvelopeLocked()

	// Detect spectral peaks for Identity Phase Locking
	vc.peaks = vc.peaks[:0]
	peakFloor := maxMag * 1e-4
	for k := 2; k < vcHalfBins-2; k++ {
		mk := vc.mag[k]
		if mk > peakFloor && mk > vc.mag[k-1] && mk >= vc.mag[k+1] {
			vc.peaks = append(vc.peaks, k)
		}
	}

	// Synthesize pitch-shifted excitation with Identity Phase Locking (region-of-influence)
	clear(vc.synOutRe)
	clear(vc.synOutIm)
	clear(vc.synMag)

	expectedPhaseInc := 2.0 * math.Pi * float64(vcHopSize) / float64(vcFFTSize)
	// Effective pitch ratio blends gently toward 1.0 on purely unvoiced consonants
	effPitch := 1.0 + (pitchRatio-1.0)*voiced

	for idx, p := range vc.peaks {
		// Region of influence: midpoints to neighboring peaks
		regStart := 1
		if idx > 0 {
			regStart = (vc.peaks[idx-1] + p) / 2
		}
		regEnd := vcHalfBins - 2
		if idx+1 < len(vc.peaks) {
			regEnd = (p + vc.peaks[idx+1]) / 2
		}
		// Limit region width so wide valleys don't bleed across formants
		if p-regStart > 12 {
			regStart = p - 12
		}
		if regEnd-p > 12 {
			regEnd = p + 12
		}

		// Advance peak phase using instantaneous frequency
		delta := vc.anaPhase[p] - vc.prevPhase[p]
		expected := float64(p) * expectedPhaseInc
		trueFreq := expected + wrapPhase(delta-expected)

		dstP := int(math.Round(float64(p) * effPitch))
		if dstP < 1 || dstP >= vcHalfBins-1 {
			continue
		}
		vc.synPhase[dstP] = wrapPhase(vc.synPhase[dstP] + trueFreq*effPitch)
		rot := vc.synPhase[dstP] - vc.anaPhase[p]

		// Copy entire harmonic lobe with locked relative phase (Identity Phase Locking)
		for k := regStart; k <= regEnd; k++ {
			dst := dstP + (k - p)
			if dst < 1 || dst >= vcHalfBins-1 {
				continue
			}
			exc := vc.mag[k] / vc.env[k]
			lockedPhase := vc.anaPhase[k] + rot
			vc.synOutRe[dst] += exc * math.Cos(lockedPhase)
			vc.synOutIm[dst] += exc * math.Sin(lockedPhase)
		}
	}

	// Update prevPhase for next hop
	copy(vc.prevPhase, vc.anaPhase)

	// Apply non-linear formant-warped envelope + softness EQ and reconstruct full complex spectrum
	vc.re[0] = 0
	vc.im[0] = 0
	vc.re[vcHalfBins-1] = 0
	vc.im[vcHalfBins-1] = 0

	for j := 1; j < vcHalfBins-1; j++ {
		srcF := vc.nonLinearFormantSrc(j, formantRatio)
		var warpedEnv float64
		if srcF >= float64(vcHalfBins-1) {
			warpedEnv = vc.env[vcHalfBins-1] * 0.5
		} else {
			idx := int(srcF)
			frac := srcF - float64(idx)
			warpedEnv = vc.env[idx]*(1.0-frac) + vc.env[idx+1]*frac
		}

		scale := warpedEnv * vc.softnessGain(j, softness)

		// Blend unvoiced high-frequency texture smoothly to preserve crisp consonants
		cr := vc.synOutRe[j] * scale
		ci := vc.synOutIm[j] * scale
		if voiced < 0.85 {
			unvoicedWeight := (1.0 - voiced) * 0.65
			// Keep original unvoiced phase/magnitude with gentle softness shaping
			rawScale := vc.softnessGain(j, softness)
			cr = cr*(1.0-unvoicedWeight) + vc.re[j]*rawScale*unvoicedWeight
			ci = ci*(1.0-unvoicedWeight) + vc.im[j]*rawScale*unvoicedWeight
		}

		vc.re[j] = cr
		vc.im[j] = ci
		vc.re[vcFFTSize-j] = cr
		vc.im[vcFFTSize-j] = -ci
	}

	// Inverse FFT + synthesis window + overlap-add
	vc.fft.Transform(vc.re, vc.im, true)
	for i := 0; i < vcFFTSize; i++ {
		vc.outOLA[i] += vc.re[i] * vc.window[i]
	}

	for i := 0; i < vcHopSize; i++ {
		vc.hopOut = append(vc.hopOut, vc.outOLA[i]*vc.olaNorm[i])
	}
	copy(vc.outOLA, vc.outOLA[vcHopSize:])
	clear(vc.outOLA[vcFFTSize-vcHopSize:])
}
