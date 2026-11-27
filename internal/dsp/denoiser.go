package dsp

import (
	"log"
	"math"
	"sync"
	"sync/atomic"

	rnnoise "github.com/VoiceBlender/rnnoise-go"
	"github.com/VoiceBlender/rnnoise-go/model"
)

var (
	sharedModelOnce sync.Once
	sharedModel     *rnnoise.Model
	sharedModelErr  error
)

func loadRNNoiseModel() (*rnnoise.Model, error) {
	sharedModelOnce.Do(func() {
		sharedModel, sharedModelErr = model.Load()
	})
	return sharedModel, sharedModelErr
}

// Denoiser wraps Pure-Go RNNoise (48kHz / 480 samples = 10ms frames) with lock-free ON/OFF switching,
// click-free crossfading on state transitions, lazy model loading, and atomic VAD probability reporting.
type Denoiser struct {
	mu         sync.Mutex
	sampleRate int
	inner      *rnnoise.Denoiser
	initFailed bool
	enabled    atomic.Bool
	wasEnabled bool
	vadBits    atomic.Uint32
	inBuf      []float32
	outBuf     []float32
	frameSize  int
}

// NewDenoiser creates a Denoiser whose RNNoise neural-network weights are loaded
// lazily on the first enabled Process call so RX-only / idle sessions avoid
// allocating the model in RAM.
func NewDenoiser(sampleRate int, enabled bool) (*Denoiser, error) {
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	fs := sampleRate / 100 // 10ms frame (480 samples @ 48kHz)
	if fs <= 0 {
		fs = 480
	}
	d := &Denoiser{
		sampleRate: sampleRate,
		wasEnabled: enabled,
		frameSize:  fs,
	}
	d.enabled.Store(enabled)
	return d, nil
}

func (d *Denoiser) ensureInitLocked() bool {
	if d.inner != nil {
		return true
	}
	if d.initFailed {
		return false
	}
	m, err := loadRNNoiseModel()
	if err != nil {
		log.Printf("rnnoise model load warning: %v", err)
		d.initFailed = true
		return false
	}
	inner, err := rnnoise.New(rnnoise.Options{
		SampleRate: d.sampleRate,
		Model:      m,
		Fast:       true,
	})
	if err != nil {
		log.Printf("rnnoise init warning: %v", err)
		d.initFailed = true
		return false
	}
	d.inner = inner
	d.frameSize = inner.FrameSize()
	d.inBuf = make([]float32, d.frameSize)
	d.outBuf = make([]float32, d.frameSize)
	return true
}

func (d *Denoiser) FrameSize() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.frameSize
}

func (d *Denoiser) SetEnabled(on bool) {
	d.enabled.Store(on)
}

func (d *Denoiser) IsEnabled() bool {
	return d.enabled.Load()
}

// VAD returns the latest voice activity probability in [0.0, 1.0].
func (d *Denoiser) VAD() float32 {
	return math.Float32frombits(d.vadBits.Load())
}

// Process modifies samples in-place (unit scale [-1, 1]).
// Input length must be a multiple of FrameSize() (480 samples @ 48kHz).
func (d *Denoiser) Process(samples []float32) {
	if d == nil || len(samples) == 0 {
		return
	}
	wantOn := d.enabled.Load()

	d.mu.Lock()
	defer d.mu.Unlock()

	if !wantOn && !d.wasEnabled {
		d.vadBits.Store(math.Float32bits(0))
		return
	}
	if !wantOn && d.inner == nil {
		// Disabled before model was ever loaded; no crossfade or model load needed.
		d.wasEnabled = false
		d.vadBits.Store(math.Float32bits(0))
		return
	}
	if !d.ensureInitLocked() {
		d.wasEnabled = false
		d.vadBits.Store(math.Float32bits(0))
		return
	}

	fs := d.frameSize
	const scale = 32768.0
	const invScale = 1.0 / 32768.0

	for offset := 0; offset+fs <= len(samples); offset += fs {
		if !wantOn && !d.wasEnabled {
			d.vadBits.Store(math.Float32bits(0))
			break
		}
		chunk := samples[offset : offset+fs]

		// Scale unit float32 [-1, 1] to RNNoise int16 magnitude [-32768, 32767]
		for i := 0; i < fs; i++ {
			d.inBuf[i] = chunk[i] * scale
		}
		vad, err := d.inner.Process(d.outBuf, d.inBuf)
		if err != nil {
			continue
		}
		d.vadBits.Store(math.Float32bits(vad))

		switch {
		case wantOn && d.wasEnabled:
			// Normal denoised output
			for i := 0; i < fs; i++ {
				chunk[i] = d.outBuf[i] * invScale
			}
		case wantOn && !d.wasEnabled:
			// Crossfade OFF -> ON across this 10ms frame
			for i := 0; i < fs; i++ {
				alpha := float32(i) / float32(fs)
				denoised := d.outBuf[i] * invScale
				chunk[i] = chunk[i]*(1-alpha) + denoised*alpha
			}
			d.wasEnabled = true
		case !wantOn && d.wasEnabled:
			// Crossfade ON -> OFF across this 10ms frame
			for i := 0; i < fs; i++ {
				alpha := float32(i) / float32(fs)
				denoised := d.outBuf[i] * invScale
				chunk[i] = denoised*(1-alpha) + chunk[i]*alpha
			}
			d.wasEnabled = false
			d.inner.Reset()
			d.vadBits.Store(math.Float32bits(0))
		}
	}
}
