package dsp

import (
	"math"
	"sync"
)

const (
	MinDB         = -60.0
	muteRampSteps = 240 // 5ms @ 48kHz
)

// Processor applies gain, smooth mute, optional noise gate, soft limiter, and tracks VU levels.
type Processor struct {
	mu               sync.Mutex
	sampleRate       float64
	gainDB           float64
	gainLinear       float64
	muted            bool
	muteEnv          float64 // 1.0 = unmuted, 0.0 = muted
	gateEnabled      bool
	gateThresholdDB  float64
	gateThresholdLin float64
	gateEnv          float64
	gateHoldLeft     int
	limiterEnabled   bool

	rmsDB      float32
	peakDB     float32
	peakHoldDB float32
	holdTicks  int
}

func NewProcessor(sampleRate int) *Processor {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	p := &Processor{
		sampleRate:       float64(sampleRate),
		gainLinear:       1.0,
		muteEnv:          1.0,
		gateThresholdDB:  -45.0,
		gateThresholdLin: DBToLinear(-45.0),
		gateEnv:          1.0,
		limiterEnabled:   true,
		rmsDB:            MinDB,
		peakDB:           MinDB,
		peakHoldDB:       MinDB,
	}
	return p
}

func DBToLinear(db float64) float64 {
	return math.Pow(10.0, db/20.0)
}

func LinearToDB(v float64) float64 {
	if v <= 1e-6 {
		return MinDB
	}
	db := 20.0 * math.Log10(v)
	if db < MinDB {
		return MinDB
	}
	if db > 0 {
		return 0
	}
	return db
}

func (p *Processor) SetGainDB(db float64) {
	if db < -24 {
		db = -24
	}
	if db > 24 {
		db = 24
	}
	p.mu.Lock()
	p.gainDB = db
	p.gainLinear = DBToLinear(db)
	p.mu.Unlock()
}

func (p *Processor) SetVolumePercent(pct float64) {
	if pct < 0 {
		pct = 0
	}
	if pct > 200 {
		pct = 200
	}
	p.mu.Lock()
	p.gainLinear = pct / 100.0
	if p.gainLinear > 0 {
		p.gainDB = 20.0 * math.Log10(p.gainLinear)
	} else {
		p.gainDB = MinDB
	}
	p.mu.Unlock()
}

func (p *Processor) SetMuted(m bool) {
	p.mu.Lock()
	p.muted = m
	p.mu.Unlock()
}

func (p *Processor) IsMuted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.muted
}

func (p *Processor) SetNoiseGate(enabled bool, thresholdDB float64) {
	if thresholdDB < -60 {
		thresholdDB = -60
	}
	if thresholdDB > -5 {
		thresholdDB = -5
	}
	p.mu.Lock()
	p.gateEnabled = enabled
	p.gateThresholdDB = thresholdDB
	p.gateThresholdLin = DBToLinear(thresholdDB)
	p.mu.Unlock()
}

func (p *Processor) SetLimiter(enabled bool) {
	p.mu.Lock()
	p.limiterEnabled = enabled
	p.mu.Unlock()
}

// Process modifies samples in-place and updates VU meter statistics.
func (p *Processor) Process(samples []float32) {
	if len(samples) == 0 {
		return
	}
	p.mu.Lock()
	gain := p.gainLinear
	muted := p.muted
	muteEnv := p.muteEnv
	gateOn := p.gateEnabled
	gateThresh := p.gateThresholdLin
	gateEnv := p.gateEnv
	gateHold := p.gateHoldLeft
	limiterOn := p.limiterEnabled
	sr := p.sampleRate
	p.mu.Unlock()

	muteStep := 1.0 / float64(muteRampSteps)
	gateAttack := 1.0 / (0.005 * sr)  // 5ms attack
	gateRelease := 1.0 / (0.120 * sr) // 120ms release
	holdSamples := int(0.150 * sr)    // 150ms hold

	var sumSq float64
	var peak float64

	for i, s := range samples {
		v := float64(s) * gain

		// Noise gate
		if gateOn {
			absV := math.Abs(v)
			if absV >= gateThresh {
				gateHold = holdSamples
				gateEnv += gateAttack
				if gateEnv > 1.0 {
					gateEnv = 1.0
				}
			} else if gateHold > 0 {
				gateHold--
			} else {
				gateEnv -= gateRelease
				if gateEnv < 0.0 {
					gateEnv = 0.0
				}
			}
			v *= gateEnv
		} else {
			gateEnv = 1.0
		}

		// Smooth mute ramp
		if muted {
			muteEnv -= muteStep
			if muteEnv < 0.0 {
				muteEnv = 0.0
			}
		} else {
			muteEnv += muteStep
			if muteEnv > 1.0 {
				muteEnv = 1.0
			}
		}
		v *= muteEnv

		// Soft limiter (transparent below 0.85, soft tanh saturation above)
		if limiterOn {
			const knee = 0.85
			absV := math.Abs(v)
			if absV > knee {
				excess := (absV - knee) / (1.0 - knee)
				limited := knee + (1.0-knee)*math.Tanh(excess)
				if v < 0 {
					v = -limited
				} else {
					v = limited
				}
			}
		}
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}

		samples[i] = float32(v)
		sumSq += v * v
		absOut := math.Abs(v)
		if absOut > peak {
			peak = absOut
		}
	}

	rms := math.Sqrt(sumSq / float64(len(samples)))
	rmsDB := float32(LinearToDB(rms))
	peakDB := float32(LinearToDB(peak))

	p.mu.Lock()
	p.muteEnv = muteEnv
	p.gateEnv = gateEnv
	p.gateHoldLeft = gateHold
	p.rmsDB = rmsDB
	p.peakDB = peakDB
	if peakDB >= p.peakHoldDB {
		p.peakHoldDB = peakDB
		p.holdTicks = 30 // ~300ms hold @ 10ms frames
	} else if p.holdTicks > 0 {
		p.holdTicks--
	} else {
		p.peakHoldDB -= 1.2
		if p.peakHoldDB < peakDB {
			p.peakHoldDB = peakDB
		}
		if p.peakHoldDB < MinDB {
			p.peakHoldDB = MinDB
		}
	}
	p.mu.Unlock()
}

// Levels returns (rmsDB, peakDB, peakHoldDB) in the range [-60, 0].
func (p *Processor) Levels() (rmsDB, peakDB, peakHoldDB float32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rmsDB, p.peakDB, p.peakHoldDB
}

// ResetLevels resets meter indicators to silence.
func (p *Processor) ResetLevels() {
	p.mu.Lock()
	p.rmsDB = MinDB
	p.peakDB = MinDB
	p.peakHoldDB = MinDB
	p.mu.Unlock()
}
