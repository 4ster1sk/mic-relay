package codec

import (
	"fmt"
	"math"

	"github.com/tphakala/go-opus/opus"

	"mic-relay/internal/protocol"
)

type OpusEncoder struct {
	enc         *opus.Encoder
	bitrateBps  int
	int16Scratch []int16
}

func NewOpusEncoder(bitrateBps int) (*OpusEncoder, error) {
	if bitrateBps <= 0 {
		bitrateBps = 96000
	}
	enc, err := opus.NewEncoder(opus.EncoderConfig{
		SampleRate:     protocol.SampleRate,
		Channels:       protocol.Channels,
		Bitrate:        bitrateBps,
		ConstrainedVBR: true,
		Complexity:     6,
	})
	if err != nil {
		return nil, fmt.Errorf("opus encoder init: %w", err)
	}
	return &OpusEncoder{
		enc:          enc,
		bitrateBps:   bitrateBps,
		int16Scratch: make([]int16, protocol.FrameSamples20ms),
	}, nil
}

func (e *OpusEncoder) CodecID() protocol.CodecID { return protocol.CodecOpus }
func (e *OpusEncoder) FrameSamples() int         { return protocol.FrameSamples20ms }
func (e *OpusEncoder) BitrateKbps() int          { return e.bitrateBps / 1000 }

func (e *OpusEncoder) Encode(pcm []float32, dst []byte) ([]byte, error) {
	if len(pcm) != protocol.FrameSamples20ms {
		return nil, fmt.Errorf("opus encode: expected %d samples, got %d", protocol.FrameSamples20ms, len(pcm))
	}
	if cap(dst) < 1275 {
		dst = make([]byte, 1275)
	} else {
		dst = dst[:1275]
	}
	for i, s := range pcm {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		e.int16Scratch[i] = int16(math.Round(float64(s) * 32767.0))
	}
	n, err := e.enc.Encode(e.int16Scratch, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

type OpusDecoder struct {
	dec          *opus.Decoder
	int16Scratch []int16
}

func NewOpusDecoder() (*OpusDecoder, error) {
	dec, err := opus.NewDecoder(protocol.SampleRate, protocol.Channels)
	if err != nil {
		return nil, fmt.Errorf("opus decoder init: %w", err)
	}
	return &OpusDecoder{
		dec:          dec,
		int16Scratch: make([]int16, protocol.FrameSamples20ms*2),
	}, nil
}

func (d *OpusDecoder) CodecID() protocol.CodecID { return protocol.CodecOpus }

func (d *OpusDecoder) Decode(payload []byte, dst []float32) ([]float32, error) {
	n, err := d.dec.Decode(payload, d.int16Scratch)
	if err != nil {
		return nil, err
	}
	if cap(dst) < n {
		dst = make([]float32, n)
	} else {
		dst = dst[:n]
	}
	const inv = 1.0 / 32768.0
	for i := 0; i < n; i++ {
		dst[i] = float32(d.int16Scratch[i]) * inv
	}
	return dst, nil
}
