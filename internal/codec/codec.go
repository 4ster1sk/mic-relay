package codec

import (
	"fmt"

	"mic-relay/internal/config"
	"mic-relay/internal/protocol"
)

// Encoder converts mono 48kHz float32 samples into wire payload bytes.
type Encoder interface {
	CodecID() protocol.CodecID
	FrameSamples() int
	Encode(pcm []float32, dst []byte) ([]byte, error)
	BitrateKbps() int
}

// Decoder converts wire payload bytes back into mono 48kHz float32 samples.
type Decoder interface {
	CodecID() protocol.CodecID
	Decode(payload []byte, dst []float32) ([]float32, error)
}

func NewEncoderForMode(mode config.CodecMode) (Encoder, error) {
	switch mode {
	case config.CodecModePCM, "":
		return NewPCM16Encoder(), nil
	case config.CodecModeOpus96:
		return NewOpusEncoder(96000)
	case config.CodecModeOpus48:
		return NewOpusEncoder(48000)
	default:
		return nil, fmt.Errorf("unsupported codec mode: %s", mode)
	}
}

func NewDecoderForID(id protocol.CodecID) (Decoder, error) {
	switch id {
	case protocol.CodecPCM16:
		return NewPCM16Decoder(), nil
	case protocol.CodecOpus:
		return NewOpusDecoder()
	default:
		return nil, fmt.Errorf("unsupported codec id: %d", id)
	}
}
