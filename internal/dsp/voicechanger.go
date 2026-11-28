package dsp

import (
	"math"
	"sync"
	"sync/atomic"
)

const (
	vcFFTSize          = 4096 // 11.72Hz/bin @ 48kHz (high-precision spectral resolution + 60ms linear-conv tail)
	vcWinSize          = 1200 // 25.0ms @ 48kHz (exact 5 * vcHopSize -> constant 2.5 COLA with zero hop ripple)
	vcHopSize          = 240  // 5.0ms @ 48kHz (exact half of 480-sample 10ms frame; 80% overlap)
	vcHalfBins         = vcFFTSize/2 + 1
	vcTEIters          = 3    // True Envelope iterations for tight harmonic-free upper hull
	vcMinCepOrd        = 48   // ~460Hz upper F0 bound @ 4096-pt FFT
	vcMaxCepOrd        = 220  // ~100Hz lower F0 bound @ 4096-pt FFT
	vcMaxMinPhaseQuef  = 1024 // 21.3ms max causal impulse length (strictly < 2896 zero-pad samples)
	vcTaperMinPhaseLen = 256
)

// VoiceChangerParams holds all tunable female voice conversion parameters.
type VoiceChangerParams struct {
	PitchSemitones  float64 // -12.0 .. +16.0
	FormantRatio    float64 // 0.75 .. 1.45 (overall vocal tract scaling)
	ThroatFormant   float64 // 0.80 .. 1.30 (F1 pharynx/throat slimness multiplier, 1.0 = neutral)
	BrightFormant   float64 // 0.80 .. 1.30 (F2/F3 oral vowel brightness multiplier, 1.0 = neutral)
	Breathiness     float64 // 0.00 .. 1.00 (voiced-gated minimum-phase formant-shaped aspiration)
	Softness        float64 // 0.00 .. 1.00 (LF glottal Open-Quotient H1/H2 warmth + de-buzz tilt)
	HighPassEnabled bool
	HighPassHz      float64 // 80.0 .. 280.0 Hz (4th-order Butterworth chest-cut HPF)
	DeEsser         float64 // 0.00 .. 1.00 (dynamic 4.5-10kHz sibilance attenuation)
}

// Clamp returns a copy of p with every field clamped to safe DSP bounds.
func (p VoiceChangerParams) Clamp() VoiceChangerParams {
	if p.PitchSemitones < -12 {
		p.PitchSemitones = -12
	} else if p.PitchSemitones > 16 {
		p.PitchSemitones = 16
	}
	if p.FormantRatio < 0.75 {
		p.FormantRatio = 0.75
	} else if p.FormantRatio > 1.45 {
		p.FormantRatio = 1.45
	}
	if p.ThroatFormant < 0.80 {
		p.ThroatFormant = 0.80
	} else if p.ThroatFormant > 1.30 {
		p.ThroatFormant = 1.30
	}
	if p.BrightFormant < 0.80 {
		p.BrightFormant = 0.80
	} else if p.BrightFormant > 1.30 {
		p.BrightFormant = 1.30
	}
	if p.Breathiness < 0 {
		p.Breathiness = 0
	} else if p.Breathiness > 1 {
		p.Breathiness = 1
	}
	if p.Softness < 0 {
		p.Softness = 0
	} else if p.Softness > 1 {
		p.Softness = 1
	}
	if p.HighPassHz < 80 {
		p.HighPassHz = 80
	} else if p.HighPassHz > 280 {
		p.HighPassHz = 280
	}
	if p.DeEsser < 0 {
		p.DeEsser = 0
	} else if p.DeEsser > 1 {
		p.DeEsser = 1
	}
	return p
}

type VoiceChangerPreset struct {
	Name string
	VoiceChangerParams
}

var (
	PresetFemaleNatural = VoiceChangerPreset{
		Name: "女声（ナチュラル）",
		VoiceChangerParams: VoiceChangerParams{
			PitchSemitones:  7.5,
			FormantRatio:    1.18,
			ThroatFormant:   1.06,
			BrightFormant:   1.06,
			Breathiness:     0.22,
			Softness:        0.55,
			HighPassEnabled: true,
			HighPassHz:      110.0,
			DeEsser:         0.35,
		},
	}
	PresetFemaleHigh = VoiceChangerPreset{
		Name: "女声（高め・カワボ）",
		VoiceChangerParams: VoiceChangerParams{
			PitchSemitones:  10.0,
			FormantRatio:    1.24,
			ThroatFormant:   1.10,
			BrightFormant:   1.12,
			Breathiness:     0.32,
			Softness:        0.65,
			HighPassEnabled: true,
			HighPassHz:      125.0,
			DeEsser:         0.45,
		},
	}
	PresetFemaleWhisper = VoiceChangerPreset{
		Name: "女声（お姉さん・ウィスパー）",
		VoiceChangerParams: VoiceChangerParams{
			PitchSemitones:  6.0,
			FormantRatio:    1.15,
			ThroatFormant:   1.00,
			BrightFormant:   1.08,
			Breathiness:     0.42,
			Softness:        0.70,
			HighPassEnabled: true,
			HighPassHz:      100.0,
			DeEsser:         0.40,
		},
	}
	PresetAndrogynous = VoiceChangerPreset{
		Name: "中性・少年声",
		VoiceChangerParams: VoiceChangerParams{
			PitchSemitones:  3.5,
			FormantRatio:    1.09,
			ThroatFormant:   1.03,
			BrightFormant:   1.02,
			Breathiness:     0.10,
			Softness:        0.30,
			HighPassEnabled: false,
			HighPassHz:      120.0,
			DeEsser:         0.20,
		},
	}
	PresetMax = VoiceChangerPreset{
		Name: "全パラメータ最大 (MAX)",
		VoiceChangerParams: VoiceChangerParams{
			PitchSemitones:  16.0,
			FormantRatio:    1.45,
			ThroatFormant:   1.30,
			BrightFormant:   1.30,
			Breathiness:     1.0,
			Softness:        1.0,
			HighPassEnabled: true,
			HighPassHz:      280.0,
			DeEsser:         1.0,
		},
	}
)

type biquadState struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64
	y1, y2     float64
}

func (b *biquadState) setLowPass(sampleRate, cutoffHz, q float64) {
	w0 := 2.0 * math.Pi * cutoffHz / sampleRate
	cosW := math.Cos(w0)
	sinW := math.Sin(w0)
	alpha := sinW / (2.0 * q)

	b0 := (1.0 - cosW) * 0.5
	b1 := 1.0 - cosW
	b2 := (1.0 - cosW) * 0.5
	a0 := 1.0 + alpha
	a1 := -2.0 * cosW
	a2 := 1.0 - alpha

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
}

func (b *biquadState) setHighPass(sampleRate, cutoffHz, q float64) {
	w0 := 2.0 * math.Pi * cutoffHz / sampleRate
	cosW := math.Cos(w0)
	sinW := math.Sin(w0)
	alpha := sinW / (2.0 * q)

	b0 := (1.0 + cosW) * 0.5
	b1 := -(1.0 + cosW)
	b2 := (1.0 + cosW) * 0.5
	a0 := 1.0 + alpha
	a1 := -2.0 * cosW
	a2 := 1.0 - alpha

	b.b0 = b0 / a0
	b.b1 = b1 / a0
	b.b2 = b2 / a0
	b.a1 = a1 / a0
	b.a2 = a2 / a0
}

func (b *biquadState) reset() {
	b.x1, b.x2, b.y1, b.y2 = 0, 0, 0, 0
}

func (b *biquadState) process(x float64) float64 {
	y := b.b0*x + b.b1*b.x1 + b.b2*b.x2 - b.a1*b.y1 - b.a2*b.y2
	b.x2 = b.x1
	b.x1 = x
	b.y2 = b.y1
	b.y1 = y
	return y
}

// VoiceChanger performs real-time Pure-Go female voice conversion (DSP v4):
//   - Band-limited 24kHz full-resolution stride-1 YIN pitch & periodicity tracker with median-3 anti-jump filter
//   - Complementary 4th-order Linkwitz-Riley (LR4) Harmonic/Aperiodic decomposition:
//     * Voiced harmonics (<5.0kHz) undergo GCI-locked Synchronous Waveform Interpolation TD-PSOLA
//       (SWI-PSOLA) with causal time-domain vocal-tract scaling
//     * Unvoiced consonants (/s, sh, k, t, p, h/) and >5.0kHz air bypass pitch-grain repetition,
//       eliminating consonant flanging, metallic buzz, and transient smearing
//   - Causal Minimum-Phase 1200-window / 4096-pt STFT vocal tract & glottal timbre processor:
//     * F0-adaptive True Envelope + temporal log-gain smoothing (eliminating 200Hz hop-rate ripple)
//     * Kolmogorov real-cepstrum Hilbert minimum-phase filter synthesis (eliminating zero-phase pre-echo)
//     * Anatomical F1 (pharynx) / F2-F3 (oral brightness) / F4 formant sculpting
//     * LF-model glottal Open-Quotient H1+H2 warmth, upper-mid de-buzz tilt, and minimum-phase aspiration
//     * Dynamic 4.5-10kHz sibilance de-esser & F0-guarded 4th-order Butterworth chest HPF
type VoiceChanger struct {
	mu         sync.Mutex
	sampleRate float64
	enabled    atomic.Bool
	wasEnabled bool
	params     VoiceChangerParams

	pitch   *yinPitchTracker
	psola   *psolaEngine
	fft     *FFT
	window  []float64
	olaNorm float64

	inRing    []float64 // length vcWinSize
	noiseRing []float64 // length vcWinSize (continuous white noise for modulation-free breathiness)
	outOLA    []float64 // length vcFFTSize
	hopIn     []float64
	hopPSh    []float64 // scratch length vcHopSize for SWI-PSOLA output
	hopOut    []float64
	dryCopy   []float32

	// Preallocated 4096-pt STFT / True Envelope / Minimum-Phase cepstrum buffers (zero allocs)
	re         []float64
	im         []float64
	cepRe      []float64
	cepIm      []float64
	noiseRe    []float64
	noiseIm    []float64
	logMag     []float64
	envLog     []float64
	mag        []float64
	env        []float64
	warpedEnv  []float64
	smoothLogG []float64
	logGInit   bool

	voicedEWMA  float64
	deEssGainDB float64
	rngState    uint64

	// 4th-order Butterworth HPF (2 cascaded biquads with Q1=0.5412, Q2=1.3066)
	hpCutoffHz float64
	hpSmoothHz float64
	hp1        biquadState
	hp2        biquadState
}

func NewVoiceChanger(sampleRate int) *VoiceChanger {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	sr := float64(sampleRate)
	def := PresetFemaleNatural.VoiceChangerParams.Clamp()

	vc := &VoiceChanger{
		sampleRate: sr,
		params:     def,
		pitch:      newYINPitchTracker(sr),
		psola:      newPSOLAEngine(sr),
		fft:        NewFFT(vcFFTSize),
		window:     make([]float64, vcWinSize),
		inRing:     make([]float64, vcWinSize),
		noiseRing:  make([]float64, vcWinSize),
		outOLA:     make([]float64, vcFFTSize),
		hopIn:      make([]float64, 0, vcHopSize*2),
		hopPSh:     make([]float64, vcHopSize),
		hopOut:     make([]float64, 0, vcHopSize*4),
		dryCopy:    make([]float32, 0, 960),
		re:         make([]float64, vcFFTSize),
		im:         make([]float64, vcFFTSize),
		cepRe:      make([]float64, vcFFTSize),
		cepIm:      make([]float64, vcFFTSize),
		noiseRe:    make([]float64, vcFFTSize),
		noiseIm:    make([]float64, vcFFTSize),
		logMag:     make([]float64, vcHalfBins),
		envLog:     make([]float64, vcHalfBins),
		mag:        make([]float64, vcHalfBins),
		env:        make([]float64, vcHalfBins),
		warpedEnv:  make([]float64, vcHalfBins),
		smoothLogG: make([]float64, vcHalfBins),
		rngState:   0x9e3779b97f4a7c15,
		hpSmoothHz: def.HighPassHz,
	}

	// Periodic Hann window of length vcWinSize = 1200 = 5 * vcHopSize (240):
	// Sum of shifted windows at step 240 is identically 2.5 at every sample index.
	for i := 0; i < vcWinSize; i++ {
		vc.window[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(vcWinSize)))
	}
	vc.olaNorm = 1.0 / (0.5 * float64(vcWinSize/vcHopSize))

	vc.updateHPLocked(def.HighPassHz)
	return vc
}

func (vc *VoiceChanger) updateHPLocked(cutoffHz float64) {
	if math.Abs(cutoffHz-vc.hpCutoffHz) < 0.5 {
		return
	}
	vc.hpCutoffHz = cutoffHz
	const (
		q1 = 0.5411961001461970
		q2 = 1.3065629648763766
	)
	vc.hp1.setHighPass(vc.sampleRate, cutoffHz, q1)
	vc.hp2.setHighPass(vc.sampleRate, cutoffHz, q2)
}

func (vc *VoiceChanger) SetEnabled(on bool) {
	vc.enabled.Store(on)
}

func (vc *VoiceChanger) IsEnabled() bool {
	return vc.enabled.Load()
}

// SetConfig updates all voice changer parameters atomically under lock.
func (vc *VoiceChanger) SetConfig(p VoiceChangerParams) {
	p = p.Clamp()
	vc.mu.Lock()
	hpChanged := math.Abs(p.HighPassHz-vc.params.HighPassHz) >= 0.5
	vc.params = p
	if hpChanged {
		vc.hpSmoothHz = p.HighPassHz
		vc.updateHPLocked(p.HighPassHz)
	}
	vc.mu.Unlock()
}

// Config returns the current clamped VoiceChangerParams.
func (vc *VoiceChanger) Config() VoiceChangerParams {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.params
}

// SetParams is a compatibility helper updating the 4 core parameters while keeping v3/v4 parameters intact.
func (vc *VoiceChanger) SetParams(pitchSemitones, formantRatio, softness float64, hpEnabled bool) {
	vc.mu.Lock()
	p := vc.params
	p.PitchSemitones = pitchSemitones
	p.FormantRatio = formantRatio
	p.Softness = softness
	p.HighPassEnabled = hpEnabled
	p = p.Clamp()
	hpChanged := math.Abs(p.HighPassHz-vc.params.HighPassHz) >= 0.5
	vc.params = p
	if hpChanged {
		vc.hpSmoothHz = p.HighPassHz
		vc.updateHPLocked(p.HighPassHz)
	}
	vc.mu.Unlock()
}

// Params returns the 4 legacy core parameters.
func (vc *VoiceChanger) Params() (pitchSemitones, formantRatio, softness float64, hpEnabled bool) {
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return vc.params.PitchSemitones, vc.params.FormantRatio, vc.params.Softness, vc.params.HighPassEnabled
}

func (vc *VoiceChanger) resetLocked() {
	vc.pitch.Reset()
	vc.psola.Reset()
	clear(vc.inRing)
	clear(vc.noiseRing)
	clear(vc.outOLA)
	clear(vc.smoothLogG)
	vc.logGInit = false
	vc.hopIn = vc.hopIn[:0]
	vc.hopOut = vc.hopOut[:0]
	vc.voicedEWMA = 0.0
	vc.deEssGainDB = 0.0
	vc.hpSmoothHz = vc.params.HighPassHz
	vc.updateHPLocked(vc.params.HighPassHz)
	vc.hp1.reset()
	vc.hp2.reset()
}

func (vc *VoiceChanger) nextRandUnit() float64 {
	x := vc.rngState
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	vc.rngState = x
	return float64(x>>11) * (1.0 / (1 << 53))
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

	params := vc.params
	pitchRatio := math.Pow(2.0, params.PitchSemitones/12.0)

	for _, s := range samples {
		vc.hopIn = append(vc.hopIn, float64(s))
		if len(vc.hopIn) >= vcHopSize {
			vc.processHopLocked(vc.hopIn[:vcHopSize], pitchRatio, params)
			copy(vc.hopIn, vc.hopIn[vcHopSize:])
			vc.hopIn = vc.hopIn[:len(vc.hopIn)-vcHopSize]
		}
	}

	n := len(samples)
	if len(vc.hopOut) >= n {
		for i := 0; i < n; i++ {
			y := vc.hopOut[i]
			if params.HighPassEnabled {
				y = vc.hp2.process(vc.hp1.process(y))
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
			if params.HighPassEnabled {
				y = vc.hp2.process(vc.hp1.process(y))
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

// computeTrueEnvelopeLocked estimates the harmonic-free smooth upper spectral envelope
// via iterated cepstral liftering with F0-adaptive quefrency order (< 0.48 * T0).
func (vc *VoiceChanger) computeTrueEnvelopeLocked(cepOrder int) {
	if cepOrder < vcMinCepOrd {
		cepOrder = vcMinCepOrd
	} else if cepOrder > vcMaxCepOrd {
		cepOrder = vcMaxCepOrd
	}
	taper := cepOrder / 3
	if taper < 14 {
		taper = 14
	} else if taper > 48 {
		taper = 48
	}

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
			if dist > cepOrder {
				vc.cepRe[q] = 0
				vc.cepIm[q] = 0
			} else if dist > cepOrder-taper {
				t := float64(dist-(cepOrder-taper)) / float64(taper)
				w := 0.5 * (1.0 + math.Cos(math.Pi*t))
				vc.cepRe[q] *= w
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

// computeMinimumPhaseLocked converts real log-magnitude gains smoothLogG[0..vcHalfBins-1]
// into a strictly causal minimum-phase complex frequency response in (cepRe, cepIm)
// via Kolmogorov real-cepstrum Hilbert transform, completely eliminating pre-echo ringing.
func (vc *VoiceChanger) computeMinimumPhaseLocked() {
	vc.cepRe[0] = vc.smoothLogG[0]
	vc.cepIm[0] = 0
	vc.cepRe[vcHalfBins-1] = vc.smoothLogG[vcHalfBins-1]
	vc.cepIm[vcHalfBins-1] = 0
	for k := 1; k < vcHalfBins-1; k++ {
		lg := vc.smoothLogG[k]
		vc.cepRe[k] = lg
		vc.cepIm[k] = 0
		vc.cepRe[vcFFTSize-k] = lg
		vc.cepIm[vcFFTSize-k] = 0
	}

	// Real cepstrum c[q] = IFFT(log |G[k]|)
	vc.fft.Transform(vc.cepRe, vc.cepIm, true)

	// Causal minimum-phase window:
	//   q == 0           -> 1x
	//   1 <= q <= maxQ   -> 2x (with smooth raised-cosine tail taper so impulse < zero-pad)
	//   q > maxQ         -> 0 (zeros all negative anti-causal quefrencies!)
	taperStart := vcMaxMinPhaseQuef - vcTaperMinPhaseLen
	vc.cepIm[0] = 0
	for q := 1; q <= vcMaxMinPhaseQuef; q++ {
		w := 2.0
		if q > taperStart {
			u := float64(q-taperStart) / float64(vcTaperMinPhaseLen)
			w *= 0.5 * (1.0 + math.Cos(math.Pi*u))
		}
		vc.cepRe[q] *= w
		vc.cepIm[q] = 0
	}
	clear(vc.cepRe[vcMaxMinPhaseQuef+1:])
	clear(vc.cepIm[vcMaxMinPhaseQuef+1:])

	// Forward FFT -> cepRe[k] = smoothed log|G[k]|, cepIm[k] = minimum-phase angle phi_min[k]
	vc.fft.Transform(vc.cepRe, vc.cepIm, false)
}

// formantAnchors represents a strictly monotonic anatomical source<->target frequency warp table:
//   - F1 pharynx band (~620Hz): scaled by FormantRatio * ThroatFormant
//   - F2 oral vowel band (~2150Hz): scaled by FormantRatio * BrightFormant
//   - F3 band (~3450Hz): scaled non-uniformly by 1 + 0.68*(rF2 - 1)
//   - F4 speaker-ring band (~4600Hz): gentle 1 + 0.28*(rF2 - 1) (prevents cartoon/plastic tone)
//   - Sibilant band (>= 6200Hz): anchored to 1.0x for natural consonant identity
type formantAnchors struct {
	src [7]float64
	dst [7]float64
}

func buildFormantAnchors(sampleRate float64, p VoiceChangerParams, warpMix float64) formantAnchors {
	nyq := sampleRate * 0.5
	rF1 := 1.0 + warpMix*(p.FormantRatio*p.ThroatFormant-1.0)
	if rF1 < 0.70 {
		rF1 = 0.70
	} else if rF1 > 1.65 {
		rF1 = 1.65
	}
	rF2 := 1.0 + warpMix*(p.FormantRatio*p.BrightFormant-1.0)
	if rF2 < 0.70 {
		rF2 = 0.70
	} else if rF2 > 1.65 {
		rF2 = 1.65
	}
	rF3 := 1.0 + 0.68*(rF2-1.0)
	rF4 := 1.0 + 0.28*(rF2-1.0)

	t1 := 620.0 * rF1
	t2 := 2150.0 * rF2
	if t2 < t1+420.0 {
		t2 = t1 + 420.0
	}
	t3 := 3450.0 * rF3
	if t3 < t2+480.0 {
		t3 = t2 + 480.0
	}
	t4 := 4650.0 * rF4
	if t4 < t3+450.0 {
		t4 = t3 + 450.0
	}
	t5 := 6300.0
	if t5 < t4+550.0 {
		t5 = t4 + 550.0
	}
	if t5 > nyq-1000.0 {
		t5 = nyq - 1000.0
	}

	return formantAnchors{
		src: [7]float64{0.0, 620.0, 2150.0, 3450.0, 4650.0, 6300.0, nyq},
		dst: [7]float64{0.0, t1, t2, t3, t4, t5, nyq},
	}
}

func (fa *formantAnchors) mapTargetToOrigFreq(targetFreq float64) float64 {
	if targetFreq <= 0 {
		return 0
	}
	for i := 0; i < 6; i++ {
		if targetFreq <= fa.dst[i+1] {
			span := fa.dst[i+1] - fa.dst[i]
			if span <= 1e-6 {
				return fa.src[i]
			}
			u := (targetFreq - fa.dst[i]) / span
			return fa.src[i] + u*(fa.src[i+1]-fa.src[i])
		}
	}
	return fa.src[6]
}

// preScaledFactor returns the effective vocal-tract scaling already applied to origFreq
// inside psolaEngine (effBeta below the 5.0kHz LR4 crossover on voiced harmonics, tapering to 1.0 above).
func preScaledFactor(origFreq, voiced, effBeta float64) float64 {
	if math.Abs(effBeta-1.0) < 1e-4 || voiced < 1e-4 || origFreq >= 5800.0 {
		return 1.0
	}
	bandW := 1.0
	if origFreq > 4200.0 {
		u := (origFreq - 4200.0) / 1600.0
		bandW = 0.5 * (1.0 + math.Cos(math.Pi*u))
	}
	return 1.0 + voiced*bandW*(effBeta-1.0)
}

// softnessGain models female LF glottal source characteristics (Open Quotient Rd shaping):
//   - H1 & H2 dominance (+warmth & relaxed head/chest mix so voice never sounds thin/pinched)
//   - Sub-fundamental chest boom trim below 0.88 * f0Out
//   - Smooth spectral tilt softening brassy male upper-mid glottal buzz (1.2-3.5kHz)
//   - Silky high-frequency air presence (5.5-8.5kHz)
func (vc *VoiceChanger) softnessGain(freq, f0Out, voiced, softness float64) float64 {
	if softness <= 0.001 {
		return 1.0
	}
	gainDB := 0.0

	// 1. H1 + gentle H2 dominance (reproduces higher female glottal Open Quotient)
	if voiced > 0.12 && f0Out >= 120.0 && f0Out <= 480.0 {
		w1 := 0.28 * f0Out
		if w1 < 42.0 {
			w1 = 42.0
		}
		d1 := (freq - f0Out) / w1
		if d1 > -3.2 && d1 < 3.2 {
			gainDB += 3.6 * softness * voiced * math.Exp(-0.5*d1*d1)
		}

		h2 := 2.0 * f0Out
		w2 := 0.24 * f0Out
		if w2 < 48.0 {
			w2 = 48.0
		}
		d2 := (freq - h2) / w2
		if d2 > -2.5 && d2 < 2.5 {
			gainDB += 1.4 * softness * voiced * math.Exp(-0.5*d2*d2)
		}
	}

	// 2. Trim residual male chest boom around 160Hz (strictly below shifted H1)
	if freq > 65.0 && freq < 320.0 && freq < f0Out*0.86 {
		d := (freq - 160.0) / 72.0
		gainDB -= 3.8 * softness * math.Exp(-0.5*d*d)
	}

	// 3. De-buzz spectral tilt over brassy male upper-mid harmonics (1.2-3.6kHz)
	if freq > 800.0 && freq < 4400.0 {
		d := (freq - 2100.0) / 860.0
		gainDB -= 5.0 * softness * math.Exp(-0.5*d*d)
	}

	// 4. Silky high-frequency air presence around 6.3kHz
	if freq > 3800.0 && freq < 10200.0 {
		d := (freq - 6300.0) / 1650.0
		gainDB += 2.3 * softness * math.Exp(-0.5*d*d)
	}

	return math.Pow(10.0, gainDB/20.0)
}

func breathBandWeight(freq float64) float64 {
	if freq <= 1800.0 || freq >= 10800.0 {
		return 0.0
	}
	if freq < 3000.0 {
		t := (freq - 1800.0) / 1200.0
		return 0.5 * (1.0 - math.Cos(math.Pi*t))
	}
	if freq <= 7400.0 {
		return 1.0
	}
	t := (freq - 7400.0) / 3400.0
	return 0.5 * (1.0 + math.Cos(math.Pi*t))
}

func deEssBandWeight(freq float64) float64 {
	if freq <= 4000.0 || freq >= 11500.0 {
		return 0.0
	}
	if freq < 5000.0 {
		t := (freq - 4000.0) / 1000.0
		return 0.5 * (1.0 - math.Cos(math.Pi*t))
	}
	if freq <= 9500.0 {
		return 1.0
	}
	t := (freq - 9500.0) / 2000.0
	return 0.5 * (1.0 + math.Cos(math.Pi*t))
}

func (vc *VoiceChanger) processHopLocked(inHop []float64, pitchRatio float64, p VoiceChangerParams) {
	// 1. Band-limited 24kHz full-resolution YIN F0 & periodicity tracking
	f0In, yinPer := vc.pitch.Push(inHop)

	// 2. Dual-band LR4 H/N split + GCI-locked SWI-PSOLA pitch & causal vocal-tract scaling
	effAlpha, effBeta, psolaVoiced := vc.psola.ProcessHop(
		inHop, vc.hopPSh, f0In, yinPer, pitchRatio, p.FormantRatio,
	)

	// Measure post-PSOLA hop RMS for strict silence gating
	hopRMS := 0.0
	for _, s := range vc.hopPSh {
		hopRMS += s * s
	}
	hopRMS = math.Sqrt(hopRMS / float64(len(vc.hopPSh)))

	// Continuous unit-variance time-domain white noise stream for modulation-free breathiness
	if p.Breathiness > 0.001 {
		const sqrt3 = 1.7320508075688772
		copy(vc.noiseRing, vc.noiseRing[vcHopSize:])
		base := vcWinSize - vcHopSize
		for i := 0; i < vcHopSize; i++ {
			vc.noiseRing[base+i] = (vc.nextRandUnit()*2.0 - 1.0) * sqrt3
		}
	}

	// 3. Push hop into causal zero-padded 4096-pt STFT ring buffer (1200-sample Hann window)
	copy(vc.inRing, vc.inRing[vcHopSize:])
	copy(vc.inRing[vcWinSize-vcHopSize:], vc.hopPSh)

	clear(vc.re)
	clear(vc.im)
	for i := 0; i < vcWinSize; i++ {
		vc.re[i] = vc.inRing[i] * vc.window[i]
	}
	vc.fft.Transform(vc.re, vc.im, false)

	binHz := vc.sampleRate / float64(vcFFTSize)
	lowStart := int(100.0 / binHz)
	lowEnd := int(1800.0 / binHz)
	highStart := int(3800.0 / binHz)
	highEnd := int(10000.0 / binHz)
	if highEnd >= vcHalfBins {
		highEnd = vcHalfBins - 1
	}

	var lowE, highE float64
	for k := 0; k < vcHalfBins; k++ {
		m := math.Hypot(vc.re[k], vc.im[k])
		vc.mag[k] = m
		vc.logMag[k] = math.Log(math.Max(m, 1e-7))
		e := m * m
		if k >= lowStart && k <= lowEnd {
			lowE += e
		} else if k >= highStart && k <= highEnd {
			highE += e
		}
	}

	specVoiced := 0.0
	if lowE+highE > 1e-10 {
		ratio := lowE / (lowE + highE)
		specVoiced = (ratio - 0.18) / 0.42
		if specVoiced < 0 {
			specVoiced = 0
		} else if specVoiced > 1 {
			specVoiced = 1
		}
	}
	combinedVoiced := 0.65*psolaVoiced + 0.35*specVoiced
	if hopRMS < 1.2e-4 {
		combinedVoiced = 0.0
	}
	vc.voicedEWMA = 0.60*vc.voicedEWMA + 0.40*combinedVoiced
	voiced := vc.voicedEWMA

	// 4. F0-adaptive True Envelope extraction on 4096-pt spectrum
	f0Out := f0In * effAlpha
	if f0Out < 80.0 {
		f0Out = 80.0
	} else if f0Out > 650.0 {
		f0Out = 650.0
	}

	// Keep 4th-order HPF safely below shifted H1 so deep voices retain natural fundamental warmth
	targetHPF := p.HighPassHz
	if effAlpha > 1.05 {
		maxSafeHPF := 0.78 * f0Out
		if maxSafeHPF < 80.0 {
			maxSafeHPF = 80.0
		}
		if targetHPF > maxSafeHPF {
			targetHPF = maxSafeHPF
		}
	}
	vc.hpSmoothHz = 0.85*vc.hpSmoothHz + 0.15*targetHPF
	if p.HighPassEnabled {
		vc.updateHPLocked(vc.hpSmoothHz)
	}

	cepOrder := int(0.46 * (vc.sampleRate / f0Out))
	vc.computeTrueEnvelopeLocked(cepOrder)

	// 5. Residual anatomical F1/F2/F3/F4 formant sculpting + LF glottal softness + dynamic de-esser
	// On unvoiced consonants, gently moderate formant warp (65% -> 100% with voicing) so plosives
	// acquire feminine oral brightness without thinning fricatives.
	warpMix := 0.65 + 0.35*voiced
	if math.Abs(effAlpha-1.0) < 1e-3 {
		warpMix = 1.0
	}
	anchors := buildFormantAnchors(vc.sampleRate, p, warpMix)

	excSumSq := 0.0
	excCount := 0
	excStart := int(300.0 / binHz)
	excEnd := int(3000.0 / binHz)
	for k := excStart; k <= excEnd && k < vcHalfBins-1; k++ {
		ex := vc.mag[k] / vc.env[k]
		excSumSq += ex * ex
		excCount++
	}
	excRMS := 0.0
	if excCount > 0 {
		excRMS = math.Sqrt(excSumSq / float64(excCount))
	}

	var postBodyE, postSibE float64
	var coreEnvSum float64
	coreEnvCount := 0
	bodyStart := int(200.0 / binHz)
	bodyEnd := int(3200.0 / binHz)
	sibStart := int(4500.0 / binHz)
	sibEnd := int(10000.0 / binHz)
	if sibEnd >= vcHalfBins-1 {
		sibEnd = vcHalfBins - 2
	}

	// Pass A: evaluate residual formant + softness gains & sibilance balance
	for j := 1; j < vcHalfBins-1; j++ {
		freq := float64(j) * binHz
		origFreq := anchors.mapTargetToOrigFreq(freq)
		preScale := preScaledFactor(origFreq, psolaVoiced, effBeta)
		shiftedSrcBin := (origFreq * preScale) / binHz

		var wEnv float64
		if shiftedSrcBin <= 0 {
			wEnv = vc.env[0]
		} else if shiftedSrcBin >= float64(vcHalfBins-1) {
			wEnv = vc.env[vcHalfBins-1] * 0.5
		} else {
			idx := int(shiftedSrcBin)
			frac := shiftedSrcBin - float64(idx)
			wEnv = vc.env[idx]*(1.0-frac) + vc.env[idx+1]*frac
		}
		vc.warpedEnv[j] = wEnv
		if j >= excStart && j <= excEnd {
			coreEnvSum += wEnv
			coreEnvCount++
		}

		gFormant := wEnv / vc.env[j]
		if gFormant < 0.125 {
			gFormant = 0.125
		} else if gFormant > 8.0 {
			gFormant = 8.0
		}

		gSoft := vc.softnessGain(freq, f0Out, voiced, p.Softness)
		gBase := gFormant * gSoft
		vc.envLog[j] = gBase

		estMag := vc.mag[j] * gBase
		e := estMag * estMag
		if j >= bodyStart && j <= bodyEnd {
			postBodyE += e
		} else if j >= sibStart && j <= sibEnd {
			postSibE += e
		}
	}
	coreEnvAvg := 0.0
	if coreEnvCount > 0 {
		coreEnvAvg = coreEnvSum / float64(coreEnvCount)
	}

	// Dynamic sibilance de-esser envelope
	targetCutDB := 0.0
	if p.DeEsser > 0.001 && postBodyE+postSibE > 1e-9 {
		sibRatio := postSibE / (postBodyE + postSibE)
		if sibRatio > 0.14 {
			excess := (sibRatio - 0.14) / 0.45
			if excess > 1.0 {
				excess = 1.0
			}
			targetCutDB = -12.0 * p.DeEsser * excess
		}
	}
	if targetCutDB < vc.deEssGainDB {
		vc.deEssGainDB = 0.45*vc.deEssGainDB + 0.55*targetCutDB
	} else {
		vc.deEssGainDB = 0.82*vc.deEssGainDB + 0.18*targetCutDB
	}

	// Build temporally-smoothed log-gain curve (kills 200Hz frame-to-frame envelope chatter)
	const (
		minLogG = -2.302585 // -20 dB
		maxLogG = +2.079442 // +18 dB
	)
	for j := 1; j < vcHalfBins-1; j++ {
		freq := float64(j) * binHz
		g := vc.envLog[j]
		if vc.deEssGainDB < -0.05 {
			dw := deEssBandWeight(freq)
			if dw > 0 {
				g *= math.Pow(10.0, (vc.deEssGainDB*dw)/20.0)
			}
		}
		lg := math.Log(math.Max(g, 1e-4))
		if lg < minLogG {
			lg = minLogG
		} else if lg > maxLogG {
			lg = maxLogG
		}
		if !vc.logGInit {
			vc.smoothLogG[j] = lg
		} else {
			vc.smoothLogG[j] = 0.52*vc.smoothLogG[j] + 0.48*lg
		}
	}
	vc.smoothLogG[0] = vc.smoothLogG[1]
	vc.smoothLogG[vcHalfBins-1] = vc.smoothLogG[vcHalfBins-2]
	vc.logGInit = true

	// Convert target magnitude curve into a causal Minimum-Phase complex filter (cepRe, cepIm)
	vc.computeMinimumPhaseLocked()

	// Optional continuous minimum-phase formant-shaped aspiration noise
	breathGate := (voiced - 0.12) / 0.18
	if breathGate < 0 {
		breathGate = 0
	} else if breathGate > 1 {
		breathGate = 1
	}
	doBreath := p.Breathiness > 0.001 && breathGate > 0 && hopRMS > 2e-4 && excRMS > 1e-4
	if doBreath {
		clear(vc.noiseRe)
		clear(vc.noiseIm)
		for i := 0; i < vcWinSize; i++ {
			vc.noiseRe[i] = vc.noiseRing[i] * vc.window[i]
		}
		vc.fft.Transform(vc.noiseRe, vc.noiseIm, false)
	}

	// Pass B: apply complex causal Minimum-Phase filter + mixed-excitation aspiration exchange
	vc.re[0] = 0
	vc.im[0] = 0
	vc.re[vcHalfBins-1] = 0
	vc.im[vcHalfBins-1] = 0

	for j := 1; j < vcHalfBins-1; j++ {
		freq := float64(j) * binHz
		magG := math.Exp(vc.cepRe[j])
		phi := vc.cepIm[j]
		sinP, cosP := math.Sincos(phi)
		hr := magG * cosP
		hi := magG * sinP

		xr := vc.re[j]
		xi := vc.im[j]
		cr := xr*hr - xi*hi
		ci := xr*hi + xi*hr

		if doBreath {
			bw := breathBandWeight(freq)
			if bw > 0 {
				// Exchange upper-band periodic buzz for minimum-phase vocal-tract aspiration air
				harmKeep := 1.0 - 0.18*p.Breathiness*voiced*breathGate*bw
				cr *= harmKeep
				ci *= harmKeep

				breathEnv := 0.72*vc.warpedEnv[j] + 0.10*coreEnvAvg
				gBreath := 0.052 * p.Breathiness * voiced * breathGate * bw * excRMS * breathEnv
				nr := vc.noiseRe[j]
				ni := vc.noiseIm[j]
				// Impart the same causal minimum-phase vocal tract phase (cosP, sinP) onto breath noise
				cr += (nr*cosP - ni*sinP) * gBreath
				ci += (nr*sinP + ni*cosP) * gBreath
			}
		}

		vc.re[j] = cr
		vc.im[j] = ci
		vc.re[vcFFTSize-j] = cr
		vc.im[vcFFTSize-j] = -ci
	}

	// 6. Inverse 4096-pt FFT + exact constant-overlap-add (COLA = 2.5)
	vc.fft.Transform(vc.re, vc.im, true)
	for i := 0; i < vcFFTSize; i++ {
		vc.outOLA[i] += vc.re[i]
	}

	for i := 0; i < vcHopSize; i++ {
		vc.hopOut = append(vc.hopOut, vc.outOLA[i]*vc.olaNorm)
	}
	copy(vc.outOLA, vc.outOLA[vcHopSize:])
	clear(vc.outOLA[vcFFTSize-vcHopSize:])
}
