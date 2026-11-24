package dsp

import (
	"math"
	"math/rand"
	"testing"
)

func TestDenoiserRNNoiseSuppressesWhiteNoiseAndTogglesCleanly(t *testing.T) {
	d, err := NewDenoiser(48000, false)
	if err != nil {
		t.Fatalf("NewDenoiser failed: %v", err)
	}
	if d.FrameSize() != 480 {
		t.Fatalf("expected 480 frame size, got %d", d.FrameSize())
	}

	// 1. When OFF, audio passes through completely unchanged
	orig := make([]float32, 480)
	for i := range orig {
		orig[i] = float32(math.Sin(float64(i)*0.05)) * 0.25
	}
	buf := append([]float32(nil), orig...)
	d.Process(buf)
	for i := range orig {
		if buf[i] != orig[i] {
			t.Fatalf("passthrough modified sample %d: got %f want %f", i, buf[i], orig[i])
		}
	}

	// 2. When ON, steady white noise is significantly attenuated
	d.SetEnabled(true)
	rng := rand.New(rand.NewSource(42))
	var inEnergy, outEnergy float64
	frame := make([]float32, 480)
	for f := 0; f < 40; f++ {
		for i := range frame {
			frame[i] = (rng.Float32()*2 - 1) * 0.1
			if f >= 10 {
				inEnergy += float64(frame[i] * frame[i])
			}
		}
		d.Process(frame)
		for i := range frame {
			if math.IsNaN(float64(frame[i])) || math.IsInf(float64(frame[i]), 0) {
				t.Fatalf("NaN/Inf detected at frame %d sample %d", f, i)
			}
			if f >= 10 {
				outEnergy += float64(frame[i] * frame[i])
			}
		}
	}
	if outEnergy >= inEnergy*0.5 {
		t.Fatalf("expected RNNoise to reduce white noise energy significantly: in=%f out=%f", inEnergy, outEnergy)
	}

	// 3. Toggle OFF -> no NaN/panic during crossfade
	d.SetEnabled(false)
	for f := 0; f < 3; f++ {
		d.Process(frame)
	}
	if d.VAD() != 0 {
		t.Fatalf("expected VAD=0 when disabled, got %f", d.VAD())
	}
}
