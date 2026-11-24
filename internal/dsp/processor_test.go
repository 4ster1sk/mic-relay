package dsp

import (
	"math"
	"testing"
)

func TestProcessorGainLimiterMuteAndGate(t *testing.T) {
	p := NewProcessor(48000)

	// 1. Limiter keeps extreme gain within [-1.0, 1.0]
	p.SetGainDB(24)
	buf := make([]float32, 480)
	for i := range buf {
		buf[i] = 0.8 * float32(math.Sin(float64(i)*0.1))
	}
	p.Process(buf)
	for i, s := range buf {
		if s > 1.0 || s < -1.0 || math.IsNaN(float64(s)) {
			t.Fatalf("sample %d out of range: %f", i, s)
		}
	}
	rms, peak, _ := p.Levels()
	if rms <= MinDB || peak <= MinDB {
		t.Fatalf("expected non-silent levels, got rms=%f peak=%f", rms, peak)
	}

	// 2. Smooth mute fades to 0
	p.SetGainDB(0)
	p.SetMuted(true)
	for i := range buf {
		buf[i] = 0.5
	}
	p.Process(buf)
	if math.Abs(float64(buf[len(buf)-1])) > 1e-4 {
		t.Fatalf("expected muted end sample ~0, got %f", buf[len(buf)-1])
	}

	// 3. Noise gate closes on very quiet signal
	p.SetMuted(false)
	p.SetNoiseGate(true, -20)
	quiet := make([]float32, 480*30) // 300ms quiet
	for i := range quiet {
		quiet[i] = 0.005 // ~-46 dB
	}
	p.Process(quiet)
	if math.Abs(float64(quiet[len(quiet)-1])) > 0.001 {
		t.Fatalf("expected noise gate to attenuate quiet signal, got %f", quiet[len(quiet)-1])
	}
}
