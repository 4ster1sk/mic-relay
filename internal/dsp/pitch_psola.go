package dsp

import "math"

const (
	// Target lookahead latency for pitch-synchronous epoch tracking & H/N alignment:
	// 864 samples = 18.0ms @ 48kHz (covers >1.2 full pitch periods down to 68Hz).
	psolaTargetLag = 864
	psolaBufCap    = 4096

	// Linkwitz-Riley 4th-order (24dB/oct) Harmonic/Aperiodic crossover frequency.
	// Voiced harmonics & formants F1-F4 sit below 5.0kHz; sibilants & crisp breath sit above 5.0kHz.
	hnCrossoverHz = 5000.0

	// 2x-decimated (24kHz) band-limited full-resolution YIN pitch tracker parameters.
	yinDecimBufLen = 1024 // 42.7ms @ 24kHz
	yinDecimWinLen = 360  // 15.0ms integration window @ 24kHz
	yinMinHz       = 68.0
	yinMaxHz       = 480.0
	yinCMNDThresh  = 0.16
)

// lr4Crossover implements a complementary 4th-order Linkwitz-Riley crossover (LP4 + HP4 = all-pass 0.0dB).
type lr4Crossover struct {
	lp1, lp2 biquadState
	hp1, hp2 biquadState
}

func newLR4Crossover(sampleRate, cutoffHz float64) lr4Crossover {
	const qButterworth = 0.7071067811865476 // 1 / sqrt(2)
	var c lr4Crossover
	c.lp1.setLowPass(sampleRate, cutoffHz, qButterworth)
	c.lp2.setLowPass(sampleRate, cutoffHz, qButterworth)
	c.hp1.setHighPass(sampleRate, cutoffHz, qButterworth)
	c.hp2.setHighPass(sampleRate, cutoffHz, qButterworth)
	return c
}

func (c *lr4Crossover) reset() {
	c.lp1.reset()
	c.lp2.reset()
	c.hp1.reset()
	c.hp2.reset()
}

func (c *lr4Crossover) split(x float64) (low, high float64) {
	low = c.lp2.process(c.lp1.process(x))
	high = c.hp2.process(c.hp1.process(x))
	return low, high
}

// yinPitchTracker estimates fundamental frequency (F0) and periodicity on a band-limited
// (55..1150Hz) 2x-decimated stream with stride-1 lag accuracy, parabolic sub-sample
// refinement, and median-3 outlier rejection to eliminate octave jumps.
type yinPitchTracker struct {
	sampleRate float64
	decimRate  float64
	minTau     int
	maxTau     int

	// Pre-filtering before 2x decimation (removes DC rumble + F2/F3 formants & hiss)
	hpX1, hpY1 float64
	hpCoeff    float64
	preLPF     biquadState
	decimPhase int

	buf  []float64
	diff []float64
	cmnd []float64

	medHist [3]float64
	medIdx  int
	medInit bool

	f0Hz        float64
	periodicity float64
}

func newYINPitchTracker(sampleRate float64) *yinPitchTracker {
	decimRate := sampleRate * 0.5
	minTau := int(math.Floor(decimRate / yinMaxHz))
	maxTau := int(math.Ceil(decimRate / yinMinHz))
	if minTau < 12 {
		minTau = 12
	}
	if maxTau > yinDecimBufLen-yinDecimWinLen-8 {
		maxTau = yinDecimBufLen - yinDecimWinLen - 8
	}

	// 1st-order HPF @ 55Hz
	rc := 1.0 / (2.0 * math.Pi * 55.0)
	dt := 1.0 / sampleRate
	hpCoeff := rc / (rc + dt)

	pt := &yinPitchTracker{
		sampleRate: sampleRate,
		decimRate:  decimRate,
		minTau:     minTau,
		maxTau:     maxTau,
		hpCoeff:    hpCoeff,
		buf:        make([]float64, yinDecimBufLen),
		diff:       make([]float64, maxTau+4),
		cmnd:       make([]float64, maxTau+4),
		f0Hz:       135.0,
	}
	pt.preLPF.setLowPass(sampleRate, 1150.0, 0.7071067811865476)
	pt.Reset()
	return pt
}

func (pt *yinPitchTracker) Reset() {
	clear(pt.buf)
	pt.hpX1, pt.hpY1 = 0, 0
	pt.preLPF.reset()
	pt.decimPhase = 0
	pt.f0Hz = 135.0
	pt.periodicity = 0.0
	pt.medHist = [3]float64{135.0, 135.0, 135.0}
	pt.medIdx = 0
	pt.medInit = false
}

// Push feeds full-rate samples and evaluates F0 + periodicity centered around psolaTargetLag
// from the newest sample so V/UV and F0 align with the exact synthesis point.
func (pt *yinPitchTracker) Push(samples []float64) (f0Hz, periodicity float64) {
	// Filter and 2x-decimate into pt.buf
	for _, x := range samples {
		hp := pt.hpCoeff * (pt.hpY1 + x - pt.hpX1)
		pt.hpX1 = x
		pt.hpY1 = hp
		lp := pt.preLPF.process(hp)
		pt.decimPhase ^= 1
		if pt.decimPhase == 1 {
			copy(pt.buf, pt.buf[1:])
			pt.buf[yinDecimBufLen-1] = lp
		}
	}

	// Center analysis across the synthesis point (psolaTargetLag/2 samples back from end)
	// while keeping [start, start + winLen + maxTau] strictly inside [0, yinDecimBufLen).
	start := yinDecimBufLen - yinDecimWinLen - pt.maxTau - 2
	if start < 0 {
		start = 0
	}

	energy := 0.0
	for i := 0; i < yinDecimWinLen; i++ {
		v := pt.buf[start+i]
		energy += v * v
	}
	rms := math.Sqrt(energy / float64(yinDecimWinLen))
	if rms < 1.0e-4 {
		pt.periodicity *= 0.55
		return pt.f0Hz, pt.periodicity
	}

	// Full stride-1 YIN difference & CMND at 24kHz
	pt.cmnd[0] = 1.0
	cumSum := 0.0
	for tau := 1; tau <= pt.maxTau+1; tau++ {
		d := 0.0
		b0 := pt.buf[start : start+yinDecimWinLen]
		b1 := pt.buf[start+tau : start+tau+yinDecimWinLen]
		for i := 0; i < yinDecimWinLen; i++ {
			delta := b0[i] - b1[i]
			d += delta * delta
		}
		pt.diff[tau] = d
		cumSum += d
		if cumSum > 1e-14 {
			pt.cmnd[tau] = d * float64(tau) / cumSum
		} else {
			pt.cmnd[tau] = 1.0
		}
	}

	// Absolute threshold search with local minimum walk
	bestTau := -1
	bestVal := 2.0
	for tau := pt.minTau; tau <= pt.maxTau; tau++ {
		v := pt.cmnd[tau]
		if v < yinCMNDThresh {
			for tau+1 <= pt.maxTau && pt.cmnd[tau+1] <= v {
				tau++
				v = pt.cmnd[tau]
			}
			bestTau = tau
			bestVal = v
			break
		}
		if v < bestVal {
			bestVal = v
			bestTau = tau
		}
	}

	if bestTau < pt.minTau {
		pt.periodicity *= 0.65
		return pt.f0Hz, pt.periodicity
	}

	// Parabolic interpolation on raw difference function around bestTau
	refinedTau := float64(bestTau)
	if bestTau > 1 && bestTau <= pt.maxTau {
		s0 := pt.diff[bestTau-1]
		s1 := pt.diff[bestTau]
		s2 := pt.diff[bestTau+1]
		denom := 2.0 * (s0 - 2.0*s1 + s2)
		if math.Abs(denom) > 1e-14 {
			off := (s0 - s2) / denom
			if off > -0.5 && off < 0.5 {
				refinedTau += off
			}
		}
	}

	instPer := (0.44 - bestVal) / 0.30
	if instPer < 0 {
		instPer = 0
	} else if instPer > 1 {
		instPer = 1
	}
	pt.periodicity = 0.55*pt.periodicity + 0.45*instPer

	if instPer > 0.30 && refinedTau > 1.0 {
		instF0 := pt.decimRate / refinedTau
		if instF0 >= yinMinHz && instF0 <= yinMaxHz {
			if !pt.medInit {
				pt.medHist = [3]float64{instF0, instF0, instF0}
				pt.medInit = true
				pt.f0Hz = instF0
			} else {
				pt.medHist[pt.medIdx] = instF0
				pt.medIdx = (pt.medIdx + 1) % 3
				medF0 := median3(pt.medHist[0], pt.medHist[1], pt.medHist[2])
				if math.Abs(medF0-pt.f0Hz) > pt.f0Hz*0.42 && instPer < 0.78 {
					pt.f0Hz = 0.82*pt.f0Hz + 0.18*medF0
				} else {
					pt.f0Hz = 0.55*pt.f0Hz + 0.45*medF0
				}
			}
		}
	}
	return pt.f0Hz, pt.periodicity
}

func median3(a, b, c float64) float64 {
	if a > b {
		a, b = b, a
	}
	if b > c {
		b = c
	}
	if a > b {
		return a
	}
	return b
}

// gciEpoch stores a tracked input Glottal Closure Instant (GCI) in buffer coordinates.
type gciEpoch struct {
	pos    float64 // sub-sample GCI position in lowBuf
	period float64 // local input pitch period T0 in samples
	corr   float64 // cycle-to-cycle waveform correlation [0, 1]
}

// synGrain represents an output synthesis pitch grain with SWI (Synchronous Waveform
// Interpolation) across two bracketing phase-locked input epochs (mA, mB).
type synGrain struct {
	outPos   float64 // output pitch mark center
	mA       float64 // left bracketing input GCI
	mB       float64 // right bracketing input GCI
	mu       float64 // interpolation weight in [0, 1] between mA and mB
	inPeriod float64 // local input T0 for pitch-down single-cycle guard
	resolved bool
}

// psolaEngine performs Dual-Band Harmonic/Aperiodic Decomposition + Epoch-Synchronous
// Waveform-Interpolated TD-PSOLA (SWI-PSOLA):
//   - Complementary LR4 crossover splits voiced harmonics (<5.0kHz) from crisp sibilants/air (>5.0kHz)
//   - Voiced harmonics undergo GCI-locked SWI-PSOLA pitch shifting (alpha) + time-domain
//     vocal-tract scaling (beta), completely eliminating WSOLA phase cancellation & grain buzz
//   - Unvoiced consonants (/s, sh, k, t, p, h/) and >5.0kHz air bypass pitch-grain repetition
//     via an exact delay-matched path, preserving razor-sharp transients and natural sibilants
type psolaEngine struct {
	sampleRate float64
	xover      lr4Crossover

	// Fundamental excitation filter (60..850Hz) for consistent GCI peak polarity locking
	excHPX1    float64
	excHPY1    float64
	excHPCoeff float64
	excLPF     biquadState
	excDelay   int

	lowBuf   []float64
	highBuf  []float64
	excBuf   []float64
	writePos int

	epochs   []gciEpoch
	wasVoiced bool

	g0          synGrain
	g1          synGrain
	xfadeLen    float64
	prevVoiced  float64
	smoothAlpha float64
	smoothBeta  float64
	paramInit   bool
}

func newPSOLAEngine(sampleRate float64) *psolaEngine {
	rc := 1.0 / (2.0 * math.Pi * 60.0)
	dt := 1.0 / sampleRate
	hpCoeff := rc / (rc + dt)

	// Group delay of 2nd-order Butterworth LPF @ 850Hz in fundamental band: sqrt(2)/w0
	w0 := 2.0 * math.Pi * 850.0 / sampleRate
	excDelay := int(math.Round(math.Sqrt2 / w0))

	pe := &psolaEngine{
		sampleRate:  sampleRate,
		xover:       newLR4Crossover(sampleRate, hnCrossoverHz),
		excHPCoeff:  hpCoeff,
		excDelay:    excDelay,
		lowBuf:      make([]float64, psolaBufCap),
		highBuf:     make([]float64, psolaBufCap),
		excBuf:      make([]float64, psolaBufCap),
		epochs:      make([]gciEpoch, 0, 64),
		smoothAlpha: 1.0,
		smoothBeta:  1.0,
	}
	pe.excLPF.setLowPass(sampleRate, 850.0, 0.7071067811865476)
	pe.Reset()
	return pe
}

func (pe *psolaEngine) Reset() {
	pe.xover.reset()
	pe.excHPX1, pe.excHPY1 = 0, 0
	pe.excLPF.reset()
	clear(pe.lowBuf)
	clear(pe.highBuf)
	clear(pe.excBuf)

	// Prime writePos so [0 .. writePos) has history behind outStart = writePos - psolaTargetLag
	pe.writePos = psolaTargetLag + 768
	initT0 := pe.sampleRate / 135.0

	pe.epochs = pe.epochs[:0]
	for pos := float64(128); pos <= float64(pe.writePos-320); pos += initT0 {
		pe.epochs = append(pe.epochs, gciEpoch{pos: pos, period: initT0, corr: 0})
	}
	outStart := float64(pe.writePos - psolaTargetLag)
	pe.g0 = synGrain{
		outPos:   outStart - initT0*0.5,
		mA:       outStart - initT0*0.5,
		mB:       outStart - initT0*0.5,
		mu:       0,
		inPeriod: initT0,
		resolved: true,
	}
	pe.g1 = synGrain{
		outPos:   outStart + initT0*0.5,
		inPeriod: initT0,
		resolved: false,
	}
	pe.xfadeLen = 0.65 * initT0
	pe.wasVoiced = false
	pe.prevVoiced = 0.0
	pe.smoothAlpha = 1.0
	pe.smoothBeta = 1.0
	pe.paramInit = false
}

func (pe *psolaEngine) shiftCoords(shift int) {
	fShift := float64(shift)
	copy(pe.lowBuf, pe.lowBuf[shift:pe.writePos])
	copy(pe.highBuf, pe.highBuf[shift:pe.writePos])
	copy(pe.excBuf, pe.excBuf[shift:pe.writePos])
	pe.writePos -= shift

	keepFrom := 0
	for i := range pe.epochs {
		pe.epochs[i].pos -= fShift
		if pe.epochs[i].pos < 64.0 && i+2 < len(pe.epochs) {
			keepFrom = i
		}
	}
	if keepFrom > 0 {
		copy(pe.epochs, pe.epochs[keepFrom:])
		pe.epochs = pe.epochs[:len(pe.epochs)-keepFrom]
	}

	pe.g0.outPos -= fShift
	pe.g0.mA -= fShift
	pe.g0.mB -= fShift
	pe.g1.outPos -= fShift
	if pe.g1.resolved {
		pe.g1.mA -= fShift
		pe.g1.mB -= fShift
	}
}

// advanceInputEpochs tracks glottal closure instants (GCIs) up to safeLimit using
// excitation peak locking on voiced onset and full-rate stride-1 NCC waveform locking
// across sustained/transitioning speech.
func (pe *psolaEngine) advanceInputEpochs(t0In float64, voiced bool) {
	if t0In < 80.0 {
		t0In = 80.0
	} else if t0In > 720.0 {
		t0In = 720.0
	}

	halfCorr := int(0.42 * t0In)
	if halfCorr < 36 {
		halfCorr = 36
	} else if halfCorr > 220 {
		halfCorr = 220
	}
	maxSearch := int(0.24 * t0In)
	if maxSearch < 12 {
		maxSearch = 12
	} else if maxSearch > 150 {
		maxSearch = 150
	}

	safeLimit := float64(pe.writePos - halfCorr - maxSearch - 16)
	if len(pe.epochs) == 0 {
		pe.epochs = append(pe.epochs, gciEpoch{pos: safeLimit - t0In, period: t0In, corr: 0})
	}

	for {
		last := pe.epochs[len(pe.epochs)-1]
		nom := last.pos + t0In
		if nom > safeLimit {
			break
		}

		if !voiced {
			pe.epochs = append(pe.epochs, gciEpoch{pos: nom, period: t0In, corr: 0})
			pe.wasVoiced = false
			continue
		}

		// Voiced onset: snap onto strongest positive glottal excitation peak (group-delay compensated)
		if !pe.wasVoiced {
			nomInt := int(math.Round(nom))
			halfSpan := int(0.48 * t0In)
			lo := nomInt - halfSpan
			hi := nomInt + halfSpan
			if lo < 16 {
				lo = 16
			}
			if hi+pe.excDelay >= pe.writePos-4 {
				hi = pe.writePos - pe.excDelay - 5
			}
			bestIdx := nomInt
			bestExc := -1e30
			for idx := lo; idx <= hi; idx++ {
				v := pe.excBuf[idx+pe.excDelay]
				if v > bestExc {
					bestExc = v
					bestIdx = idx
				}
			}
			pe.epochs = append(pe.epochs, gciEpoch{pos: float64(bestIdx), period: t0In, corr: 0.85})
			pe.wasVoiced = true
			continue
		}

		// Sustained voiced speech: full-rate stride-1 NCC lock against previous GCI grain
		prevInt := int(math.Round(last.pos))
		nomInt := int(math.Round(nom))
		lo := nomInt - maxSearch
		hi := nomInt + maxSearch
		if prevInt-halfCorr < 4 || hi+halfCorr >= pe.writePos-2 || lo-halfCorr < 4 {
			pe.epochs = append(pe.epochs, gciEpoch{pos: nom, period: t0In, corr: 0})
			continue
		}

		refSlice := pe.lowBuf[prevInt-halfCorr : prevInt+halfCorr]
		corrLen := 2 * halfCorr
		refE := 0.0
		excMax := 1e-9
		for i := 0; i < corrLen; i++ {
			r := refSlice[i]
			refE += r * r
		}
		for idx := lo; idx <= hi; idx++ {
			if e := math.Abs(pe.excBuf[idx+pe.excDelay]); e > excMax {
				excMax = e
			}
		}

		if refE < 1e-11 {
			pe.epochs = append(pe.epochs, gciEpoch{pos: nom, period: t0In, corr: 0})
			continue
		}

		bestCand := nomInt
		bestScore := -2.0
		bestRawNCC := 0.0
		var nccAtPrev, nccAtBest, nccAtNext float64
		invSpan := 1.0 / float64(maxSearch+1)

		for cand := lo; cand <= hi; cand++ {
			candSlice := pe.lowBuf[cand-halfCorr : cand+halfCorr]
			dot := 0.0
			candE := 0.0
			for i := 0; i < corrLen; i++ {
				r := refSlice[i]
				c := candSlice[i]
				dot += r * c
				candE += c * c
			}
			ncc := dot / (math.Sqrt(refE*candE) + 1e-14)
			dist := float64(cand-nomInt) * invSpan
			excBonus := 0.03 * (pe.excBuf[cand+pe.excDelay] / excMax)
			score := ncc - 0.06*dist*dist + excBonus
			if score > bestScore {
				bestScore = score
				bestCand = cand
				bestRawNCC = ncc
			}
		}

		// Sub-sample parabolic refinement on raw stride-1 NCC around bestCand
		refinedPos := float64(bestCand)
		if bestCand > lo && bestCand < hi {
			evalNCC := func(c int) float64 {
				cs := pe.lowBuf[c-halfCorr : c+halfCorr]
				dot, ce := 0.0, 0.0
				for i := 0; i < corrLen; i++ {
					r := refSlice[i]
					v := cs[i]
					dot += r * v
					ce += v * v
				}
				return dot / (math.Sqrt(refE*ce) + 1e-14)
			}
			nccAtPrev = evalNCC(bestCand - 1)
			nccAtBest = bestRawNCC
			nccAtNext = evalNCC(bestCand + 1)
			denom := 2.0 * (nccAtPrev - 2.0*nccAtBest + nccAtNext)
			if denom < -1e-12 {
				off := (nccAtPrev - nccAtNext) / denom
				if off > -0.5 && off < 0.5 {
					refinedPos += off
				}
			}
		}

		actualPeriod := refinedPos - last.pos
		if actualPeriod < 0.5*t0In || actualPeriod > 1.6*t0In {
			actualPeriod = t0In
		}
		pe.epochs = append(pe.epochs, gciEpoch{
			pos:    refinedPos,
			period: actualPeriod,
			corr:   bestRawNCC,
		})
	}
}

// resolveGrain maps an output pitch mark outPos onto bracketing input GCIs (mA, mB)
// for phase-locked Synchronous Waveform Interpolation (SWI).
func (pe *psolaEngine) resolveGrain(g *synGrain, passthrough bool) {
	if passthrough {
		g.mA = g.outPos
		g.mB = g.outPos
		g.mu = 0
		g.resolved = true
		return
	}
	n := len(pe.epochs)
	if n == 0 {
		g.mA = g.outPos
		g.mB = g.outPos
		g.mu = 0
		g.resolved = true
		return
	}
	if g.outPos <= pe.epochs[0].pos {
		e := pe.epochs[0]
		g.mA = e.pos
		g.mB = e.pos
		g.mu = 0
		g.inPeriod = e.period
		g.resolved = true
		return
	}
	if g.outPos >= pe.epochs[n-1].pos {
		e := pe.epochs[n-1]
		g.mA = e.pos
		g.mB = e.pos
		g.mu = 0
		g.inPeriod = e.period
		g.resolved = true
		return
	}

	// Binary search for bracketing input epochs eA.pos <= outPos <= eB.pos
	lo, hi := 0, n-1
	for hi-lo > 1 {
		mid := (lo + hi) >> 1
		if pe.epochs[mid].pos <= g.outPos {
			lo = mid
		} else {
			hi = mid
		}
	}
	eA := pe.epochs[lo]
	eB := pe.epochs[hi]
	span := eB.pos - eA.pos
	mu := 0.0
	if span > 1e-6 {
		mu = (g.outPos - eA.pos) / span
	}

	// Only interpolate across cycles when adjacent GCIs are strongly phase-coherent;
	// otherwise snap cleanly to nearest GCI so vowel transitions never double.
	if eB.corr >= 0.72 && span < 1.45*eA.period {
		g.mA = eA.pos
		g.mB = eB.pos
		g.mu = mu
		g.inPeriod = (1.0-mu)*eA.period + mu*eB.period
	} else if mu < 0.5 {
		g.mA = eA.pos
		g.mB = eA.pos
		g.mu = 0
		g.inPeriod = eA.period
	} else {
		g.mA = eB.pos
		g.mB = eB.pos
		g.mu = 0
		g.inPeriod = eB.period
	}
	g.resolved = true
}

// readCubic samples buf at fractional index pos using 4-point Catmull-Rom Hermite interpolation.
func readCubic(buf []float64, pos float64, limit int) float64 {
	idx := int(math.Floor(pos))
	f := pos - float64(idx)
	if idx < 1 {
		idx = 1
		f = 0
	} else if idx+2 >= limit {
		idx = limit - 3
		if idx < 1 {
			return 0
		}
		f = 0
	}
	y0 := buf[idx-1]
	y1 := buf[idx]
	y2 := buf[idx+1]
	y3 := buf[idx+2]

	c0 := y1
	c1 := 0.5 * (y2 - y0)
	c2 := y0 - 2.5*y1 + 2.0*y2 - 0.5*y3
	c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
	return ((c3*f+c2)*f+c1)*f + c0
}

func (pe *psolaEngine) evalGrainSample(g *synGrain, tau, beta float64) float64 {
	delta := beta * tau
	vA := readCubic(pe.lowBuf, g.mA+delta, pe.writePos)
	if g.mu <= 1e-5 {
		return vA
	}
	vB := readCubic(pe.lowBuf, g.mB+delta, pe.writePos)
	return vA*(1.0-g.mu) + vB*g.mu
}

// ProcessHop decomposes inHop into harmonic (<5kHz) and aperiodic (>5kHz) bands,
// runs GCI-locked SWI-PSOLA pitch (targetAlpha) + vocal-tract scaling (targetFormant)
// on voiced harmonics while bypassing unvoiced consonants and >5kHz air without grain artifacts,
// and returns (effAlpha, effBeta, hopVoicedness).
func (pe *psolaEngine) ProcessHop(
	inHop, outHop []float64,
	f0In, yinPeriodicity, targetAlpha, targetFormant float64,
) (effAlpha, effBeta, voicedOut float64) {
	if targetAlpha < 0.5 {
		targetAlpha = 0.5
	} else if targetAlpha > 2.6 {
		targetAlpha = 2.6
	}

	// Safe time-domain formant ratio beta: when pitching up, let SWI-PSOLA perform
	// organic causal vocal-tract scaling directly inside each glottal grain while
	// staying strictly within a single pitch period (beta <= 0.88 * alpha).
	targetBeta := 1.0
	if targetAlpha > 1.06 && targetFormant > 1.0 {
		maxSafeBeta := 0.88 * targetAlpha
		if maxSafeBeta < 1.0 {
			maxSafeBeta = 1.0
		}
		targetBeta = targetFormant
		if targetBeta > maxSafeBeta {
			targetBeta = maxSafeBeta
		}
		if targetBeta > 1.38 {
			targetBeta = 1.38
		}
	}

	if !pe.paramInit {
		pe.smoothAlpha = targetAlpha
		pe.smoothBeta = targetBeta
		pe.paramInit = true
	} else {
		pe.smoothAlpha = 0.65*pe.smoothAlpha + 0.35*targetAlpha
		pe.smoothBeta = 0.65*pe.smoothBeta + 0.35*targetBeta
	}
	alpha := pe.smoothAlpha
	beta := pe.smoothBeta

	n := len(inHop)
	if pe.writePos+n >= psolaBufCap {
		shift := pe.writePos - (psolaTargetLag + 960)
		if shift > 0 {
			pe.shiftCoords(shift)
		}
	}

	// Split incoming hop via LR4 crossover & update fundamental excitation buffer
	for i, x := range inHop {
		lo, hi := pe.xover.split(x)
		pe.lowBuf[pe.writePos+i] = lo
		pe.highBuf[pe.writePos+i] = hi

		hp := pe.excHPCoeff * (pe.excHPY1 + lo - pe.excHPX1)
		pe.excHPX1 = lo
		pe.excHPY1 = hp
		pe.excBuf[pe.writePos+i] = pe.excLPF.process(hp)
	}
	pe.writePos += n

	outStart := pe.writePos - psolaTargetLag - n

	// Compute delay-aligned low/high band RMS at [outStart .. outStart+n) for V/UV consonant detection
	var lowSq, highSq float64
	for i := 0; i < n; i++ {
		lo := pe.lowBuf[outStart+i]
		hi := pe.highBuf[outStart+i]
		lowSq += lo * lo
		highSq += hi * hi
	}
	lowRMS := math.Sqrt(lowSq / float64(n))
	highRMS := math.Sqrt(highSq / float64(n))
	totRMS := math.Hypot(lowRMS, highRMS)

	// Sibilants (/s, sh, ts, ch/) and unvoiced plosives (/k, t, p/) have high >5kHz share and low YIN periodicity
	bandBalance := 0.0
	if lowRMS+highRMS > 1e-9 {
		bandBalance = (lowRMS - 0.85*highRMS) / (lowRMS + 0.85*highRMS)
		if bandBalance < 0 {
			bandBalance = 0
		}
	}
	targetVoiced := 0.0
	if totRMS > 1.2e-4 {
		rawV := (yinPeriodicity - 0.18) / 0.42
		if rawV < 0 {
			rawV = 0
		} else if rawV > 1 {
			rawV = 1
		}
		targetVoiced = rawV * (0.35 + 0.65*bandBalance)
		if yinPeriodicity > 0.72 && bandBalance > 0.25 {
			// Strongly periodic vowel rapidly locks to 1.0
			if targetVoiced < 0.92 {
				targetVoiced = 0.92
			}
		}
	}

	// Fast attack on vowel onset (avoids mixing F0_in + F0_out), smooth release into consonants
	var hopVoiced float64
	if targetVoiced >= pe.prevVoiced {
		hopVoiced = 0.25*pe.prevVoiced + 0.75*targetVoiced
	} else {
		hopVoiced = 0.55*pe.prevVoiced + 0.45*targetVoiced
	}

	if f0In < yinMinHz {
		f0In = yinMinHz
	} else if f0In > yinMaxHz {
		f0In = yinMaxHz
	}
	t0In := pe.sampleRate / f0In
	pe.advanceInputEpochs(t0In, hopVoiced > 0.15)

	tOut := t0In / alpha
	if tOut < 56.0 {
		tOut = 56.0
	} else if tOut > 960.0 {
		tOut = 960.0
	}

	isNeutral := math.Abs(alpha-1.0) < 1e-3 && math.Abs(beta-1.0) < 1e-3

	invN := 1.0 / float64(n)
	for i := 0; i < n; i++ {
		idx := outStart + i
		t := float64(idx)

		// Advance output pitch-mark interval [g0.outPos, g1.outPos)
		for t >= pe.g1.outPos {
			if !pe.g1.resolved {
				pe.resolveGrain(&pe.g1, isNeutral)
			}
			pe.g0 = pe.g1
			nextOut := pe.g0.outPos + tOut
			// Guard against sudden large jumps after Reset
			if nextOut <= t-2.0*tOut {
				nextOut = t + 0.5*tOut
			}
			pe.g1 = synGrain{
				outPos:   nextOut,
				inPeriod: t0In,
				resolved: false,
			}
			// Wide raised-cosine crossfade (70% of output cycle, capped safely inside single input cycle)
			xf := 0.70 * tOut
			maxSafeXF := 0.78 * (t0In / beta)
			if xf > maxSafeXF {
				xf = maxSafeXF
			}
			if xf > 340.0 {
				xf = 340.0
			}
			if xf < 24.0 {
				xf = 24.0
			}
			pe.xfadeLen = xf
		}

		if isNeutral {
			// Exact all-pass LR4 reconstruction when pitch=0 and beta=1
			outHop[i] = pe.lowBuf[idx] + pe.highBuf[idx]
			continue
		}

		// Sample-accurate interpolated voicedness weight across the hop
		frac := float64(i+1) * invN
		vW := pe.prevVoiced + (hopVoiced-pe.prevVoiced)*frac

		var lowShifted float64
		if vW > 1e-4 {
			tau0 := t - pe.g0.outPos
			s0 := pe.evalGrainSample(&pe.g0, tau0, beta)

			// Pitch-down guard: smoothly taper grain 0 before reaching next input glottal pulse
			maxSingle := 0.90 * (pe.g0.inPeriod / beta)
			if tOut > maxSingle && tau0 > 0.55*maxSingle {
				if tau0 >= maxSingle {
					s0 = 0
				} else {
					u := (tau0 - 0.55*maxSingle) / (0.45 * maxSingle)
					s0 *= 0.5 * (1.0 + math.Cos(math.Pi*u))
				}
			}

			xfStart := pe.g1.outPos - pe.xfadeLen
			if t >= xfStart {
				if !pe.g1.resolved {
					pe.resolveGrain(&pe.g1, false)
				}
				u := (t - xfStart) / pe.xfadeLen
				if u < 0 {
					u = 0
				} else if u > 1 {
					u = 1
				}
				w0 := 0.5 * (1.0 + math.Cos(math.Pi*u))
				w1 := 1.0 - w0
				s1 := pe.evalGrainSample(&pe.g1, t-pe.g1.outPos, beta)
				lowShifted = w0*s0 + w1*s1
			} else {
				lowShifted = s0
			}
		}

		// Dual-band mixed-excitation recombination:
		//   - Low/Mid (<5.0kHz): blends SWI-PSOLA voiced harmonics with unshifted consonant transients
		//   - High (>5.0kHz): always unshifted crisp sibilance & natural aspiration air
		lowOut := vW*lowShifted + (1.0-vW)*pe.lowBuf[idx]
		outHop[i] = lowOut + pe.highBuf[idx]
	}

	pe.prevVoiced = hopVoiced
	return alpha, beta, hopVoiced
}
