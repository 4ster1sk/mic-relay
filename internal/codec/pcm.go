package codec

import (
	"encoding/binary"
	"fmt"
	"math"

	"mic-relay/internal/protocol"
)

type PCM16Encoder struct{}

func NewPCM16Encoder() *PCM16Encoder { return &PCM16Encoder{} }

func (e *PCM16Encoder) CodecID() protocol.CodecID { return protocol.CodecPCM16 }
func (e *PCM16Encoder) FrameSamples() int         { return protocol.FrameSamples10ms }
func (e *PCM16Encoder) BitrateKbps() int          { return 768 }

func (e *PCM16Encoder) Encode(pcm []float32, dst []byte) ([]byte, error) {
	need := len(pcm) * 2
	if cap(dst) < need {
		dst = make([]byte, need)
	} else {
		dst = dst[:need]
	}
	for i, s := range pcm {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		v := int16(math.Round(float64(s) * 32767.0))
		binary.LittleEndian.PutUint16(dst[i*2:i*2+2], uint16(v))
	}
	return dst, nil
}

type PCM16Decoder struct{}

func NewPCM16Decoder() *PCM16Decoder { return &PCM16Decoder{} }

func (d *PCM16Decoder) CodecID() protocol.CodecID { return protocol.CodecPCM16 }

func (d *PCM16Decoder) Decode(payload []byte, dst []float32) ([]float32, error) {
	if len(payload)%2 != 0 {
		return nil, fmt.Errorf("pcm16: odd payload length %d", len(payload))
	}
	n := len(payload) / 2
	if cap(dst) < n {
		dst = make([]float32, n)
	} else {
		dst = dst[:n]
	}
	const inv = 1.0 / 32768.0
	for i := 0; i < n; i++ {
		v := int16(binary.LittleEndian.Uint16(payload[i*2 : i*2+2]))
		dst[i] = float32(v) * inv
	}
	return dst, nil
}
