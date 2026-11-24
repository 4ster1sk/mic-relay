package codec

import (
	"math"
	"testing"

	"mic-relay/internal/config"
	"mic-relay/internal/protocol"
)

func makeSine(n int, freq float64) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = 0.5 * float32(math.Sin(2*math.Pi*freq*float64(i)/float64(protocol.SampleRate)))
	}
	return out
}

func TestPCM16RoundTrip(t *testing.T) {
	enc := NewPCM16Encoder()
	dec := NewPCM16Decoder()

	in := makeSine(protocol.FrameSamples10ms, 440)
	pkt, err := enc.Encode(in, nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(pkt) != protocol.FrameSamples10ms*2 {
		t.Fatalf("unexpected pcm payload len: %d", len(pkt))
	}
	out, err := dec.Decode(pkt, nil)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len mismatch: %d vs %d", len(out), len(in))
	}
	for i := range in {
		if math.Abs(float64(in[i]-out[i])) > 0.001 {
			t.Fatalf("sample %d diverged: in=%f out=%f", i, in[i], out[i])
		}
	}
}

func TestOpusRoundTrip(t *testing.T) {
	for _, mode := range []config.CodecMode{config.CodecModeOpus96, config.CodecModeOpus48} {
		enc, err := NewEncoderForMode(mode)
		if err != nil {
			t.Fatalf("NewEncoderForMode(%s): %v", mode, err)
		}
		dec, err := NewDecoderForID(enc.CodecID())
		if err != nil {
			t.Fatalf("NewDecoderForID: %v", err)
		}

		// Encode several 20ms frames to let CELT warm up
		var lastOut []float32
		for f := 0; f < 5; f++ {
			in := makeSine(protocol.FrameSamples20ms, 440)
			pkt, err := enc.Encode(in, nil)
			if err != nil {
				t.Fatalf("opus encode (%s) frame %d: %v", mode, f, err)
			}
			if len(pkt) == 0 {
				t.Fatalf("empty opus packet")
			}
			lastOut, err = dec.Decode(pkt, nil)
			if err != nil {
				t.Fatalf("opus decode (%s) frame %d: %v", mode, f, err)
			}
			if len(lastOut) != protocol.FrameSamples20ms {
				t.Fatalf("expected %d samples, got %d", protocol.FrameSamples20ms, len(lastOut))
			}
		}

		// Ensure decoded output has real audio energy (not silence)
		var energy float64
		for _, s := range lastOut {
			energy += float64(s * s)
		}
		rms := math.Sqrt(energy / float64(len(lastOut)))
		if rms < 0.1 {
			t.Fatalf("opus decoded rms too low (%s): %f", mode, rms)
		}
	}
}
