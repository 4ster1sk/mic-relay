package dsp

import (
	"math"
	"sync"
	"sync/atomic"
)

const (
	vcFFTSize   = 2048
	vcWinSize   = 1024 // 21.3ms Hann window zero-padded into 2048pt FFT (linear convolution + low latency)
	vcPadLeft   = 256  // 5.3ms left guard padding for zero-phase filter impulse response
	vcHopSize   = 240  // 5ms @ 48kHz (exact divisor of 480-sample 10ms frame; ~76.6% overlap)
	vcHalfBins  = vcFFTSize/2 + 1
	vcTEIters   = 3 // True Envelope iterations (fast + crisp convergence)
	vcMinCepOrd = 48
	vcMaxCepOrd = 130
)

// VoiceChangerParams holds all tunable female voice conversion parameters (DSP v3).
type VoiceChangerParams struct {
	PitchSemitones  float64 // -12.0 .. +16.0
	FormantRatio    float64 // 0.75 .. 1.45 (overall vocal tract scaling)
	ThroatFormant   float64 // 0.80 .. 1.30 (F1 throat slimness multiplier, 1.0 = neutral)
	BrightFormant   float64 // 0.80 .. 1.30 (F2/F3 vowel brightness multiplier, 1.0 = neutral)
	Breathiness     float64 // 0.00 .. 1.00 (voiced-gated formant-shaped aspiration noise)
	Softness        float64 // 0.00 .. 1.00 (glottal Open Quotient warmth + spectral tilt)
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

// VoiceChanger performs real-time Pure-Go female voice conversion (DSP v3) using:
//   - Coarse-to-fine YIN pitch + periodicity tracker (F0_in, voicedness)
//   - Time-domain WSOLA + anti-aliased Catmull-Rom cubic resampling pitch shifter
//     (strictly harmonic, eliminating STFT phase-vocoder metallic/robotic artifacts)
//   - Zero-phase 1024-window / 2048-pt zero-padded STFT vocal tract processor:
//     * F0-adaptive True Envelope extraction
//     * Strictly monotonic anchor-point F1 (throat) / F2 (brightness) independent formant warping
//     * Voiced-gated, formant-shaped breathiness / aspiration synthesis
//     * Glottal Open Quotient softness (H1 warmth + spectral tilt + silky air)
//     * Dynamic 4.5-10kHz sibilance de-esser
//   - Variable 4th-order (24dB/oct) Butterworth chest-resonance HPF
type VoiceChanger struct {
	mu         sync.Mutex
	sampleRate float64
	enabled    atomic.Bool
	wasEnabled bool
	params     VoiceChangerParams

	pitch  *pitchTracker
	wsola  *wsolaShifter
	fft    *FFT
	window []float64
	olaNorm []float64

	inRing    []float64 // length vcWinSize
	noiseRing []float64 // length vcWinSize (continuous time-domain white noise for breathiness)
	outOLA    []float64 // length vcFFTSize
	hopIn     []float64
	hopPSh    []float64 // scratch length vcHopSize for WSOLA output
	hopOut    []float64
	dryCopy   []float32

	// Preallocated STFT / True Envelope scratch buffers (zero allocations in Process)
	re     []float64
	im     []float64
	cepRe  []float64
	cepIm  []float64
	logMag []float64
	envLog []float64
	mag    []float64
	env    []float64

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
		pitch:      newPitchTracker(sr),
		wsola:      newWSOLAShifter(sr),
		fft:        NewFFT(vcFFTSize),
		window:     make([]float64, vcWinSize),
		olaNorm:    make([]float64, vcHopSize),
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
		logMag:     make([]float64, vcHalfBins),
		envLog:     make([]float64, vcHalfBins),
		mag:        make([]float64, vcHalfBins),
		env:        make([]float64, vcHalfBins),
		voicedEWMA: 0.0,
		rngState:   0x9e3779b97f4a7c15,
		hpSmoothHz: def.HighPassHz,
	}

	// Hann analysis window (length vcWinSize = 1024)
	for i := 0; i < vcWinSize; i++ {
		vc.window[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(vcWinSize)))
	}
	// Exact periodic overlap-add normalization for analysis-window-only linear-convolution OLA
	// Frame places window[k] at outOLA[vcPadLeft + k], shifting left by vcHopSize each hop.
	padMod := vcPadLeft % vcHopSize
	for pos := 0; pos < vcHopSize; pos++ {
		startK := (pos - padMod + vcHopSize) % vcHopSize
		sum := 0.0
		for k := startK; k < vcWinSize; k += vcHopSize {
			sum += vc.window[k]
		}
		if sum < 1e-6 {
			sum = 1.0
		}
		vc.olaNorm[pos] = 1.0 / sum
	}

	vc.updateHPLocked(def.HighPassHz)
	return vc
}

func (vc *VoiceChanger) updateHPLocked(cutoffHz float64) {
	if math.Abs(cutoffHz-vc.hpCutoffHz) < 0.5 {
		return
	}
	vc.hpCutoffHz = cutoffHz
	// Exact 4th-order Butterworth Q factors: 1/(2*cos(pi/8)) and 1/(2*cos(3*pi/8))
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

// SetParams is a compatibility helper updating the 4 core v2 parameters while keeping v3 parameters intact.
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
	vc.wsola.Reset()
	clear(vc.inRing)
	clear(vc.noiseRing)
	clear(vc.outOLA)
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
	// Map to [0, 1)
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

// computeTrueEnvelopeLocked estimates the smooth upper spectral envelope of harmonic
// peaks via iterated cepstral liftering with F0-adaptive quefrency order.
func (vc *VoiceChanger) computeTrueEnvelopeLocked(cepOrder int) {
	if cepOrder < vcMinCepOrd {
		cepOrder = vcMinCepOrd
	} else if cepOrder > vcMaxCepOrd {
		cepOrder = vcMaxCepOrd
	}
	taper := cepOrder / 4
	if taper < 10 {
		taper = 10
	} else if taper > 28 {
		taper = 28
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
				// Smooth raised-cosine liftering taper
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

// formantAnchors represents a strictly monotonic source<->target frequency warp table
// allowing independent F1 (throat) and F2/F3 (brightness) formant control while
// preserving natural sibilants above ~6.4kHz.
type formantAnchors struct {
	src [6]float64
	dst [6]float64
}

func buildFormantAnchors(sampleRate float64, p VoiceChangerParams) formantAnchors {
	nyq := sampleRate * 0.5
	rF1 := p.FormantRatio * p.ThroatFormant
	if rF1 < 0.70 {
		rF1 = 0.70
	} else if rF1 > 1.65 {
		rF1 = 1.65
	}
	rF2 := p.FormantRatio * p.BrightFormant
	if rF2 < 0.70 {
		rF2 = 0.70
	} else if rF2 > 1.65 {
		rF2 = 1.65
	}
	rF3 := 1.0 + 0.72*(rF2-1.0)

	t1 := 600.0 * rF1
	t2 := 2200.0 * rF2
	if t2 < t1+420.0 {
		t2 = t1 + 420.0
	}
	t3 := 3600.0 * rF3
	if t3 < t2+520.0 {
		t3 = t2 + 520.0
	}
	t4 := 6400.0
	if t4 < t3+650.0 {
		t4 = t3 + 650.0
	}
	if t4 > nyq-1000.0 {
		t4 = nyq - 1000.0
	}

	return formantAnchors{
		src: [6]float64{0.0, 600.0, 2200.0, 3600.0, 6400.0, nyq},
		dst: [6]float64{0.0, t1, t2, t3, t4, nyq},
	}
}

// mapTargetToOrigFreq inverts the monotonic target->source formant mapping.
func (fa *formantAnchors) mapTargetToOrigFreq(targetFreq float64) float64 {
	if targetFreq <= 0 {
		return 0
	}
	for i := 0; i < 5; i++ {
		if targetFreq <= fa.dst[i+1] {
			span := fa.dst[i+1] - fa.dst[i]
			if span <= 1e-6 {
				return fa.src[i]
			}
			u := (targetFreq - fa.dst[i]) / span
			return fa.src[i] + u*(fa.src[i+1]-fa.src[i])
		}
	}
	return fa.src[5]
}

// softnessGain applies female glottal Open Quotient shaping:
//   - Gentle H1 fundamental warmth around f0Out
//   - Chest boom trim below ~220Hz
//   - Smooth spectral tilt reducing harsh male glottal buzz (1.2-3.4kHz)
//   - Light silky air presence (5.5-8.0kHz)
func (vc *VoiceChanger) softnessGain(freq, f0Out, voiced, softness float64) float64 {
	if softness <= 0.001 {
		return 1.0
	}
	gainDB := 0.0

	// 1. H1 fundamental warmth (reproduces softer female glottal closure)
	if voiced > 0.15 && f0Out >= 130.0 && f0Out <= 450.0 {
		w := 0.30 * f0Out
		if w < 45.0 {
			w = 45.0
		}
		d := (freq - f0Out) / w
		if d > -3.5 && d < 3.5 {
			gainDB += 3.2 * softness * voiced * math.Exp(-0.5*d*d)
		}
	}

	// 2. Trim residual male chest boom around 165Hz (avoid cutting f0Out itself)
	if freq > 70.0 && freq < 320.0 && freq < f0Out*0.88 {
		d := (freq - 165.0) / 75.0
		gainDB -= 3.5 * softness * math.Exp(-0.5*d*d)
	}

	// 3. De-buzz spectral tilt over harsh male upper-mid harmonics (1.2-3.4kHz)
	if freq > 800.0 && freq < 4400.0 {
		d := (freq - 2050.0) / 850.0
		gainDB -= 4.8 * softness * math.Exp(-0.5*d*d)
	}

	// 4. Silky high-frequency air presence around 6.2kHz
	if freq > 3800.0 && freq < 10000.0 {
		d := (freq - 6200.0) / 1600.0
		gainDB += 2.4 * softness * math.Exp(-0.5*d*d)
	}

	return math.Pow(10.0, gainDB/20.0)
}

func breathBandWeight(freq float64) float64 {
	if freq <= 1800.0 || freq >= 10500.0 {
		return 0.0
	}
	if freq < 3000.0 {
		t := (freq - 1800.0) / 1200.0
		return 0.5 * (1.0 - math.Cos(math.Pi*t))
	}
	if freq <= 7200.0 {
		return 1.0
	}
	t := (freq - 7200.0) / 3300.0
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
	// 1. Track F0 and periodicity-based voicedness on original input
	f0In, yinVoiced := vc.pitch.Push(inHop)

	// Measure simple input hop RMS for strict silence gating
	inRMS := 0.0
	for _, s := range inHop {
		inRMS += s * s
	}
	inRMS = math.Sqrt(inRMS / float64(len(inHop)))

	// Keep pitch ratio steady across consonant/vowel boundaries so CV transitions
	// never suffer from voicedness-induced pitch glides. High-frequency sibilant
	// placement is restored by the formant anchor table (t4 = 6.4kHz).
	targetAlpha := pitchRatio

	// 2. Time-domain WSOLA + Cubic Resampling pitch shift
	effAlpha := vc.wsola.ProcessHop(inHop, vc.hopPSh, targetAlpha)

	// Maintain continuous unit-variance time-domain white noise stream for click-free,
	// modulation-free formant-shaped breathiness synthesis.
	if p.Breathiness > 0.001 {
		const sqrt3 = 1.7320508075688772
		copy(vc.noiseRing, vc.noiseRing[vcHopSize:])
		base := vcWinSize - vcHopSize
		for i := 0; i < vcHopSize; i++ {
			vc.noiseRing[base+i] = (vc.nextRandUnit()*2.0 - 1.0) * sqrt3
		}
	}

	// 3. Push pitch-shifted hop into zero-padded STFT ring buffer
	copy(vc.inRing, vc.inRing[vcHopSize:])
	copy(vc.inRing[vcWinSize-vcHopSize:], vc.hopPSh)

	clear(vc.re)
	clear(vc.im)
	for i := 0; i < vcWinSize; i++ {
		vc.re[vcPadLeft+i] = vc.inRing[i] * vc.window[i]
	}
	vc.fft.Transform(vc.re, vc.im, false)

	// Magnitudes, log-magnitudes, and spectral V/UV energy check
	binHz := vc.sampleRate / float64(vcFFTSize)
	lowStart := int(100.0 / binHz)
	lowEnd := int(1600.0 / binHz)
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
	// Combine periodicity-based and spectral-balance voicedness
	combinedVoiced := 0.55*yinVoiced + 0.45*specVoiced
	if inRMS < 1.5e-4 {
		combinedVoiced = 0.0
	}
	vc.voicedEWMA = 0.65*vc.voicedEWMA + 0.35*combinedVoiced
	voiced := vc.voicedEWMA

	// 4. F0-adaptive True Envelope extraction on pitch-shifted spectrum
	f0Out := f0In * effAlpha
	if f0Out < 80.0 {
		f0Out = 80.0
	} else if f0Out > 650.0 {
		f0Out = 650.0
	}
	// Keep HPF safely below shifted fundamental (H1) during upward pitch shifting
	// so even deep voices with higher HPF settings retain warm fundamental energy.
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

	cepOrder := int(0.62 * (vc.sampleRate / f0Out))
	vc.computeTrueEnvelopeLocked(cepOrder)

	// 5. Monotonic anchor-point F1/F2 formant warping + Softness + Breathiness + De-Esser
	anchors := buildFormantAnchors(vc.sampleRate, p)

	// Compute mean excitation RMS in voiced core band (300-3000Hz) for breathiness scaling
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

	// Pass A: compute target formant + softness gains and measure post-warp sibilance for dynamic de-esser
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

	// Reuse vc.logMag to store per-bin warpedEnv and vc.envLog to store base linear gain (zero alloc)
	for j := 1; j < vcHalfBins-1; j++ {
		freq := float64(j) * binHz
		origFreq := anchors.mapTargetToOrigFreq(freq)
		shiftedSrcBin := (origFreq * effAlpha) / binHz

		var warpedEnv float64
		if shiftedSrcBin <= 0 {
			warpedEnv = vc.env[0]
		} else if shiftedSrcBin >= float64(vcHalfBins-1) {
			warpedEnv = vc.env[vcHalfBins-1] * 0.5
		} else {
			idx := int(shiftedSrcBin)
			frac := shiftedSrcBin - float64(idx)
			warpedEnv = vc.env[idx]*(1.0-frac) + vc.env[idx+1]*frac
		}
		vc.logMag[j] = warpedEnv
		if j >= excStart && j <= excEnd {
			coreEnvSum += warpedEnv
			coreEnvCount++
		}

		// Zero-phase formant correction gain clamped to [-18dB, +18dB]
		gFormant := warpedEnv / vc.env[j]
		if gFormant < 0.125 {
			gFormant = 0.125
		} else if gFormant > 8.0 {
			gFormant = 8.0
		}

		gSoft := vc.softnessGain(freq, f0Out, voiced, p.Softness)
		gTotal := gFormant * gSoft
		vc.envLog[j] = gTotal

		estMag := vc.mag[j] * gTotal
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

	// Update dynamic de-esser attenuation (fast attack, smooth release)
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
		vc.deEssGainDB = 0.45*vc.deEssGainDB + 0.55*targetCutDB // fast attack
	} else {
		vc.deEssGainDB = 0.82*vc.deEssGainDB + 0.18*targetCutDB // smooth release
	}

	// Pass B: apply gains, voiced-gated formant-shaped breathiness, and dynamic de-esser
	vc.re[0] = 0
	vc.im[0] = 0
	vc.re[vcHalfBins-1] = 0
	vc.im[vcHalfBins-1] = 0

	breathGate := (voiced - 0.12) / 0.18
	if breathGate < 0 {
		breathGate = 0
	} else if breathGate > 1 {
		breathGate = 1
	}
	doBreath := p.Breathiness > 0.001 && breathGate > 0 && inRMS > 2e-4 && excRMS > 1e-4
	if doBreath {
		// Reuse preallocated cepRe/cepIm (unused after computeTrueEnvelopeLocked) to FFT
		// the continuous Hann-windowed noise stream for linear-convolution OLA.
		clear(vc.cepRe)
		clear(vc.cepIm)
		for i := 0; i < vcWinSize; i++ {
			vc.cepRe[vcPadLeft+i] = vc.noiseRing[i] * vc.window[i]
		}
		vc.fft.Transform(vc.cepRe, vc.cepIm, false)
	}

	for j := 1; j < vcHalfBins-1; j++ {
		freq := float64(j) * binHz
		g := vc.envLog[j]
		cr := vc.re[j] * g
		ci := vc.im[j] * g

		// Voiced-gated formant-shaped breathiness (continuous windowed aspiration noise
		// shaped by the warped vocal tract; eliminates 200Hz hop-rate envelope modulation)
		if doBreath {
			bw := breathBandWeight(freq)
			if bw > 0 {
				// Slightly soften periodic buzz in breath band & inject formant-shaped silky air
				harmKeep := 1.0 - 0.16*p.Breathiness*voiced*breathGate*bw
				cr *= harmKeep
				ci *= harmKeep

				// Blend warped formant envelope with vowel core floor so airy shimmer works
				// even when the dry mic rolls off above 4kHz.
				breathEnv := 0.70*vc.logMag[j] + 0.10*coreEnvAvg
				gBreath := 0.050 * p.Breathiness * voiced * breathGate * bw * excRMS * breathEnv
				cr += vc.cepRe[j] * gBreath
				ci += vc.cepIm[j] * gBreath
			}
		}

		// Dynamic sibilance de-esser
		if vc.deEssGainDB < -0.05 {
			dw := deEssBandWeight(freq)
			if dw > 0 {
				dg := math.Pow(10.0, (vc.deEssGainDB*dw)/20.0)
				cr *= dg
				ci *= dg
			}
		}

		vc.re[j] = cr
		vc.im[j] = ci
		vc.re[vcFFTSize-j] = cr
		vc.im[vcFFTSize-j] = -ci
	}

	// 6. Inverse FFT + exact linear-convolution overlap-add
	vc.fft.Transform(vc.re, vc.im, true)
	for i := 0; i < vcFFTSize; i++ {
		vc.outOLA[i] += vc.re[i]
	}

	for i := 0; i < vcHopSize; i++ {
		vc.hopOut = append(vc.hopOut, vc.outOLA[i]*vc.olaNorm[i])
	}
	copy(vc.outOLA, vc.outOLA[vcHopSize:])
	clear(vc.outOLA[vcFFTSize-vcHopSize:])
}
