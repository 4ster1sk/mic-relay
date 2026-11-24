package jitter

import (
	"math"
	"testing"
)

func TestJitterBufferReorderAndPacketLoss(t *testing.T) {
	jb := NewBuffer(48000, 30)

	makeFrame := func(val float32) []float32 {
		f := make([]float32, 480)
		for i := range f {
			f[i] = val
		}
		return f
	}

	// Push out-of-order and drop seq 2
	jb.Push(0, 0, makeFrame(0.2))
	jb.Push(3, 30000, makeFrame(0.5))
	jb.Push(1, 10000, makeFrame(0.3))
	jb.Push(4, 40000, makeFrame(0.6))
	jb.Push(5, 50000, makeFrame(0.7))
	jb.Push(6, 60000, makeFrame(0.8))

	out := make([]float32, 480)
	jb.Read(out)
	for i, s := range out {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("invalid sample %d: %f", i, s)
		}
	}
	st := jb.Stats()
	if st.PacketsReceived != 6 {
		t.Fatalf("expected 6 packets received, got %d", st.PacketsReceived)
	}
	if st.PacketsLost == 0 {
		t.Fatalf("expected lost packet seq 2 to be detected")
	}
}

func TestJitterBufferClockDriftCompensation(t *testing.T) {
	// Simulate 15 minutes of +0.2% faster sender clock (48096 Hz vs 48000 Hz)
	jb := NewBuffer(48000, 30)
	frame := make([]float32, 480)
	for i := range frame {
		frame[i] = 0.25 * float32(math.Sin(float64(i)*0.05))
	}
	out := make([]float32, 480)

	var seq uint32
	// Prime
	for i := 0; i < 4; i++ {
		jb.Push(seq, uint64(seq)*10000, frame)
		seq++
	}

	// Every 500 frames (5s), push 1 extra frame (+0.2% clock drift) for 20000 frames (~3.3 min simulated)
	for tick := 0; tick < 20000; tick++ {
		jb.Push(seq, uint64(seq)*10000, frame)
		seq++
		if tick > 0 && tick%500 == 0 {
			jb.Push(seq, uint64(seq)*10000, frame)
			seq++
		}
		jb.Read(out)
	}

	st := jb.Stats()
	if st.Overruns > 0 || st.Underruns > 0 {
		t.Fatalf("drift compensation failed: overruns=%d underruns=%d buf=%.1fms drift=%.3f%%",
			st.Overruns, st.Underruns, st.BufferedMs, st.DriftPercent)
	}
	if st.BufferedMs < 10 || st.BufferedMs > 60 {
		t.Fatalf("buffer drifted outside safe bounds: %.1f ms", st.BufferedMs)
	}
}
