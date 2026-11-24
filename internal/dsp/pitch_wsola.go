package dsp

import "math"

const (
	wsolaGrainLen   = 720  // 15ms @ 48kHz
	wsolaHopLen     = 360  // 7.5ms (50% Hann overlap -> exact 1.0 sum)
	wsolaCorrLen    = 360  // cross-correlation template length
	wsolaSearchBack = 560  // backward search covers pitch periods down to ~70Hz
	wsolaSearchFwd  = 120  // forward search margin
	wsolaTargetLag  = 840  // steady-state latency: wsolaGrainLen + wsolaSearchFwd = 17.5ms
	wsolaInBufCap   = 4096 // sliding input buffer
	wsolaStrBufCap  = 4096 // sliding stretched buffer

	yinBufLen   = 1536 // 32ms analysis buffer for YIN pitch tracker
	yinWinLen   = 720  // 15ms integration window
	yinMinHz    = 70.0
	yinMaxHz    = 450.0
	yinStepI    = 4 // coarse sample stride (12kHz effective rate)
	yinStepTau  = 2 // coarse lag stride
	yinThresh   = 0.18
)

// pitchTracker estimates real-time fundamental frequency (F0) and periodicity-based
// voicedness using a coarse-to-fine YIN difference function (zero allocations).
type pitchTracker struct {
	sampleRate float64
	minTau     int
	maxTau     int
	buf        []float64
	diff       []float64
	cmnd       []float64

	f0Hz   float64
	voiced float64
}

func newPitchTracker(sampleRate float64) *pitchTracker {
	minTau := int(sampleRate / yinMaxHz)
	maxTau := int(sampleRate / yinMinHz)
	if minTau < 16 {
		minTau = 16
	}
	if maxTau > yinBufLen-yinWinLen-4 {
		maxTau = yinBufLen - yinWinLen - 4
	}
	return &pitchTracker{
		sampleRate: sampleRate,
		minTau:     minTau,
		maxTau:     maxTau,
		buf:        make([]float64, yinBufLen),
		diff:       make([]float64, maxTau+4),
		cmnd:       make([]float64, maxTau+4),
		f0Hz:       140.0,
		voiced:     0.0,
	}
}

func (pt *pitchTracker) Reset() {
	clear(pt.buf)
	pt.f0Hz = 140.0
	pt.voiced = 0.0
}

// Push appends new samples and updates F0 and voicedness.
func (pt *pitchTracker) Push(samples []float64) (f0Hz, voiced float64) {
	n := len(samples)
	if n >= yinBufLen {
		copy(pt.buf, samples[n-yinBufLen:])
	} else {
		copy(pt.buf, pt.buf[n:])
		copy(pt.buf[yinBufLen-n:], samples)
	}

	// Quick energy check on the latest window
	start := yinBufLen - yinWinLen - pt.maxTau - 2
	if start < 0 {
		start = 0
	}
	energy := 0.0
	for i := 0; i < yinWinLen; i += 2 {
		v := pt.buf[start+i]
		energy += v * v
	}
	rms := math.Sqrt(energy / float64(yinWinLen/2))
	if rms < 1.5e-4 {
		pt.voiced *= 0.6
		return pt.f0Hz, pt.voiced
	}

	// Coarse YIN difference + cumulative mean normalized difference (CMND)
	cumSum := 0.0
	stepCount := 0
	for tau := yinStepTau; tau <= pt.maxTau; tau += yinStepTau {
		d := 0.0
		for i := 0; i < yinWinLen; i += yinStepI {
			delta := pt.buf[start+i] - pt.buf[start+i+tau]
			d += delta * delta
		}
		pt.diff[tau] = d
		cumSum += d
		stepCount++
		if cumSum > 1e-12 {
			pt.cmnd[tau] = d * float64(stepCount) / cumSum
		} else {
			pt.cmnd[tau] = 1.0
		}
	}

	// Find first local minimum below threshold in [minTau, maxTau], else global minimum
	bestTau := -1
	bestVal := 2.0
	firstTau := pt.minTau
	if firstTau%yinStepTau != 0 {
		firstTau += yinStepTau - (firstTau % yinStepTau)
	}
	for tau := firstTau; tau <= pt.maxTau-yinStepTau; tau += yinStepTau {
		v := pt.cmnd[tau]
		if v < yinThresh {
			// Walk to bottom of local valley
			for tau+yinStepTau <= pt.maxTau && pt.cmnd[tau+yinStepTau] < v {
				tau += yinStepTau
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
		pt.voiced *= 0.7
		return pt.f0Hz, pt.voiced
	}

	// Fine lag refinement around bestTau (stride-1 lag, stride-2 samples)
	lo := bestTau - 2
	if lo < pt.minTau {
		lo = pt.minTau
	}
	hi := bestTau + 2
	if hi > pt.maxTau {
		hi = pt.maxTau
	}
	fineBest := bestTau
	fineBestD := math.MaxFloat64
	var fineD [5]float64
	for tau := lo; tau <= hi; tau++ {
		d := 0.0
		for i := 0; i < yinWinLen; i += 2 {
			delta := pt.buf[start+i] - pt.buf[start+i+tau]
			d += delta * delta
		}
		fineD[tau-lo] = d
		if d < fineBestD {
			fineBestD = d
			fineBest = tau
		}
	}

	// Parabolic interpolation for sub-sample pitch accuracy
	refinedTau := float64(fineBest)
	idx := fineBest - lo
	if idx > 0 && idx < hi-lo {
		s0 := fineD[idx-1]
		s1 := fineD[idx]
		s2 := fineD[idx+1]
		denom := 2.0 * (s0 - 2.0*s1 + s2)
		if math.Abs(denom) > 1e-12 {
			offset := (s0 - s2) / denom
			if offset > -0.5 && offset < 0.5 {
				refinedTau += offset
			}
		}
	}

	// Map CMND valley depth to voicedness in [0, 1]
	instVoiced := (0.48 - bestVal) / 0.34
	if instVoiced < 0 {
		instVoiced = 0
	} else if instVoiced > 1 {
		instVoiced = 1
	}
	pt.voiced = 0.65*pt.voiced + 0.35*instVoiced

	if instVoiced > 0.35 && refinedTau > 1.0 {
		instF0 := pt.sampleRate / refinedTau
		if instF0 >= yinMinHz && instF0 <= yinMaxHz {
			// Guard against sudden octave jumps by smoothing moderately
			if math.Abs(instF0-pt.f0Hz) > pt.f0Hz*0.45 && instVoiced < 0.75 {
				pt.f0Hz = 0.85*pt.f0Hz + 0.15*instF0
			} else {
				pt.f0Hz = 0.65*pt.f0Hz + 0.35*instF0
			}
		}
	}
	return pt.f0Hz, pt.voiced
}

// wsolaShifter performs real-time time-domain pitch shifting via WSOLA time-scale
// modification followed by anti-aliased Catmull-Rom cubic resampling.
// Unlike STFT phase-vocoder peak shifting, all harmonics remain strictly integer
// multiples of F0_out with zero metallic/phasiness artifacts.
type wsolaShifter struct {
	sampleRate float64
	window     []float64

	inBuf      []float64
	writePos   int
	inCursor   float64
	prevSrcPos int

	olaBuf       []float64
	stretchedBuf []float64
	stretchedLen int
	resampPos    float64
	smoothAlpha  float64
	alphaInit    bool

	// Anti-aliasing 2nd-order Butterworth LPF state (active when pitchRatio > 1.05)
	lpfCutoff float64
	lpB0      float64
	lpB1      float64
	lpB2      float64
	lpA1      float64
	lpA2      float64
	lpX1      float64
	lpX2      float64
	lpY1      float64
	lpY2      float64
}

func newWSOLAShifter(sampleRate float64) *wsolaShifter {
	w := make([]float64, wsolaGrainLen)
	for i := 0; i < wsolaGrainLen; i++ {
		w[i] = 0.5 * (1.0 - math.Cos(2.0*math.Pi*float64(i)/float64(wsolaGrainLen)))
	}
	ws := &wsolaShifter{
		sampleRate:   sampleRate,
		window:       w,
		inBuf:        make([]float64, wsolaInBufCap),
		olaBuf:       make([]float64, wsolaGrainLen),
		stretchedBuf: make([]float64, wsolaStrBufCap),
		smoothAlpha:  1.0,
	}
	ws.Reset()
	return ws
}

func (ws *wsolaShifter) Reset() {
	clear(ws.inBuf)
	clear(ws.olaBuf)
	clear(ws.stretchedBuf)
	// Pre-prime history so searchBack and targetLag are immediately in bounds
	ws.writePos = wsolaSearchBack + wsolaTargetLag
	ws.inCursor = float64(wsolaSearchBack)
	ws.prevSrcPos = wsolaSearchBack - wsolaHopLen
	ws.stretchedLen = 2
	ws.resampPos = 1.0
	ws.smoothAlpha = 1.0
	ws.alphaInit = false
	ws.lpfCutoff = 0
	ws.lpX1, ws.lpX2, ws.lpY1, ws.lpY2 = 0, 0, 0, 0
}

func (ws *wsolaShifter) updateLPF(alpha float64) {
	if alpha <= 1.05 {
		ws.lpfCutoff = 0
		return
	}
	fc := 0.43 * ws.sampleRate / alpha
	if fc > 19500.0 {
		fc = 19500.0
	}
	if fc < 4000.0 {
		fc = 4000.0
	}
	if math.Abs(fc-ws.lpfCutoff) < 25.0 {
		return
	}
	ws.lpfCutoff = fc
	w0 := 2.0 * math.Pi * fc / ws.sampleRate
	cosW := math.Cos(w0)
	sinW := math.Sin(w0)
	qAlpha := sinW / math.Sqrt2

	b0 := (1.0 - cosW) * 0.5
	b1 := 1.0 - cosW
	b2 := (1.0 - cosW) * 0.5
	a0 := 1.0 + qAlpha
	a1 := -2.0 * cosW
	a2 := 1.0 - qAlpha

	ws.lpB0 = b0 / a0
	ws.lpB1 = b1 / a0
	ws.lpB2 = b2 / a0
	ws.lpA1 = a1 / a0
	ws.lpA2 = a2 / a0
}

func (ws *wsolaShifter) applyLPF(x float64) float64 {
	if ws.lpfCutoff <= 0 {
		ws.lpX2 = ws.lpX1
		ws.lpX1 = x
		ws.lpY2 = ws.lpY1
		ws.lpY1 = x
		return x
	}
	y := ws.lpB0*x + ws.lpB1*ws.lpX1 + ws.lpB2*ws.lpX2 - ws.lpA1*ws.lpY1 - ws.lpA2*ws.lpY2
	ws.lpX2 = ws.lpX1
	ws.lpX1 = x
	ws.lpY2 = ws.lpY1
	ws.lpY1 = y
	return y
}

// findBestCandidate performs a 2-stage coarse-to-fine normalized cross-correlation
// search to align the new grain's waveform phase with the natural continuation of
// the previous grain.
func (ws *wsolaShifter) findBestCandidate(nomPos, natPos int, alpha float64) int {
	minCand := nomPos - wsolaSearchBack
	if minCand < 0 {
		minCand = 0
	}
	maxCand := nomPos + wsolaSearchFwd
	maxAllowed := ws.writePos - wsolaGrainLen
	if maxCand > maxAllowed {
		maxCand = maxAllowed
	}
	if minCand >= maxCand {
		if minCand < 0 {
			return 0
		}
		if minCand > maxAllowed {
			return maxAllowed
		}
		return minCand
	}

	// Clamp natPos safely inside inBuf
	if natPos < 0 {
		natPos = 0
	}
	if natPos+wsolaCorrLen > ws.writePos {
		natPos = ws.writePos - wsolaCorrLen
	}

	// If pitch ratio is effectively 1.0 and natPos is within range, pass through exactly
	if math.Abs(alpha-1.0) < 1e-3 && natPos >= minCand && natPos <= maxCand {
		return natPos
	}

	// Compute reference template energy (stride 4 for coarse pass)
	refE := 0.0
	for i := 0; i < wsolaCorrLen; i += 4 {
		r := ws.inBuf[natPos+i]
		refE += r * r
	}
	if refE < 1e-10 {
		if nomPos < minCand {
			return minCand
		}
		if nomPos > maxCand {
			return maxCand
		}
		return nomPos
	}

	// Stage 1: Coarse search (stride 4 in lag, stride 4 in correlation)
	bestCoarse := nomPos
	if bestCoarse < minCand {
		bestCoarse = minCand
	}
	if bestCoarse > maxCand {
		bestCoarse = maxCand
	}
	bestScore := -2.0
	for cand := minCand; cand <= maxCand; cand += 4 {
		dot := 0.0
		candE := 0.0
		for i := 0; i < wsolaCorrLen; i += 4 {
			r := ws.inBuf[natPos+i]
			c := ws.inBuf[cand+i]
			dot += r * c
			candE += c * c
		}
		denom := math.Sqrt(refE*candE) + 1e-12
		score := dot / denom
		if score > bestScore {
			bestScore = score
			bestCoarse = cand
		}
	}

	// Stage 2: Fine search around bestCoarse (±4 samples, stride 2 in correlation)
	fineLo := bestCoarse - 4
	if fineLo < minCand {
		fineLo = minCand
	}
	fineHi := bestCoarse + 4
	if fineHi > maxCand {
		fineHi = maxCand
	}
	refEFine := 0.0
	for i := 0; i < wsolaCorrLen; i += 2 {
		r := ws.inBuf[natPos+i]
		refEFine += r * r
	}
	bestCand := bestCoarse
	bestScore = -2.0
	for cand := fineLo; cand <= fineHi; cand++ {
		dot := 0.0
		candE := 0.0
		for i := 0; i < wsolaCorrLen; i += 2 {
			r := ws.inBuf[natPos+i]
			c := ws.inBuf[cand+i]
			dot += r * c
			candE += c * c
		}
		denom := math.Sqrt(refEFine*candE) + 1e-12
		score := dot / denom
		if score > bestScore {
			bestScore = score
			bestCand = cand
		}
	}
	return bestCand
}

func (ws *wsolaShifter) synthesizeGrain(alpha float64) {
	// Keep inCursor locked to target latency to prevent any drift across pitch changes
	avail := float64(ws.stretchedLen) - ws.resampPos
	if avail < 0 {
		avail = 0
	}
	targetCursor := float64(ws.writePos-wsolaTargetLag) + avail/alpha
	err := targetCursor - ws.inCursor
	if math.Abs(err) > 240.0 {
		ws.inCursor += 0.25 * err
	} else {
		ws.inCursor += 0.02 * err
	}

	nomPos := int(math.Round(ws.inCursor))
	natPos := ws.prevSrcPos + wsolaHopLen
	bestCand := ws.findBestCandidate(nomPos, natPos, alpha)

	// Overlap-add Hann-windowed grain into olaBuf
	for i := 0; i < wsolaGrainLen; i++ {
		ws.olaBuf[i] += ws.inBuf[bestCand+i] * ws.window[i]
	}

	// Compact stretchedBuf if needed before appending
	if ws.stretchedLen+wsolaHopLen >= wsolaStrBufCap {
		drop := int(ws.resampPos) - 1
		if drop < 0 {
			drop = 0
		}
		if drop > ws.stretchedLen {
			drop = ws.stretchedLen
		}
		if drop > 0 {
			copy(ws.stretchedBuf, ws.stretchedBuf[drop:ws.stretchedLen])
			ws.stretchedLen -= drop
			ws.resampPos -= float64(drop)
		}
	}

	ws.updateLPF(alpha)
	for i := 0; i < wsolaHopLen; i++ {
		if ws.stretchedLen < wsolaStrBufCap {
			ws.stretchedBuf[ws.stretchedLen] = ws.applyLPF(ws.olaBuf[i])
			ws.stretchedLen++
		}
	}

	// Shift OLA buffer left by wsolaHopLen
	copy(ws.olaBuf[:wsolaHopLen], ws.olaBuf[wsolaHopLen:wsolaGrainLen])
	clear(ws.olaBuf[wsolaHopLen:wsolaGrainLen])

	ws.prevSrcPos = bestCand
	ws.inCursor += float64(wsolaHopLen) / alpha
}

// ProcessHop ingests len(inHop) samples, pitch-shifts them smoothly by targetAlpha,
// writes len(inHop) samples into outHop, and returns the exact effective pitch ratio used.
func (ws *wsolaShifter) ProcessHop(inHop, outHop []float64, targetAlpha float64) float64 {
	if targetAlpha < 0.5 {
		targetAlpha = 0.5
	} else if targetAlpha > 2.6 {
		targetAlpha = 2.6
	}
	// Initialize immediately on first hop after Reset, then smooth slider sweeps
	if !ws.alphaInit {
		ws.smoothAlpha = targetAlpha
		ws.alphaInit = true
	} else {
		ws.smoothAlpha = 0.65*ws.smoothAlpha + 0.35*targetAlpha
	}
	alpha := ws.smoothAlpha

	n := len(inHop)
	// Compact inBuf if nearing capacity
	if ws.writePos+n >= wsolaInBufCap {
		shift := ws.writePos - (wsolaTargetLag + wsolaSearchBack + 512)
		if shift > 0 {
			copy(ws.inBuf, ws.inBuf[shift:ws.writePos])
			ws.writePos -= shift
			ws.inCursor -= float64(shift)
			ws.prevSrcPos -= shift
		}
	}
	copy(ws.inBuf[ws.writePos:], inHop)
	ws.writePos += n

	// Ensure stretchedBuf has enough samples for cubic interpolation across n output samples
	neededEnd := int(math.Ceil(ws.resampPos+float64(n)*alpha)) + 3
	for ws.stretchedLen < neededEnd {
		ws.synthesizeGrain(alpha)
	}

	// Catmull-Rom 4-point cubic interpolation resampling
	for k := 0; k < n; k++ {
		pos := ws.resampPos
		idx := int(pos)
		f := pos - float64(idx)
		if idx < 1 {
			idx = 1
			f = 0
		}
		if idx+2 >= ws.stretchedLen {
			idx = ws.stretchedLen - 3
			f = 0
		}
		y0 := ws.stretchedBuf[idx-1]
		y1 := ws.stretchedBuf[idx]
		y2 := ws.stretchedBuf[idx+1]
		y3 := ws.stretchedBuf[idx+2]

		c0 := y1
		c1 := 0.5 * (y2 - y0)
		c2 := y0 - 2.5*y1 + 2.0*y2 - 0.5*y3
		c3 := 0.5*(y3-y0) + 1.5*(y1-y2)
		outHop[k] = ((c3*f+c2)*f+c1)*f + c0

		ws.resampPos += alpha
	}

	// Periodically compact stretchedBuf
	if ws.resampPos > 1024.0 {
		drop := int(ws.resampPos) - 1
		copy(ws.stretchedBuf, ws.stretchedBuf[drop:ws.stretchedLen])
		ws.stretchedLen -= drop
		ws.resampPos -= float64(drop)
	}

	return alpha
}
