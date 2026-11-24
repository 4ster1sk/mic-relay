package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	Magic                = "MRAL"
	Version              = uint8(1)
	HeaderSize           = 26
	DefaultDiscoveryPort = 48100
	DefaultAudioPort     = 48101
	SampleRate           = 48000
	Channels             = 1
	FrameSamples10ms     = 480
	FrameSamples20ms     = 960
	MaxPayloadSize       = 4096
)

type PacketType uint8

const (
	PacketAudio        PacketType = 1
	PacketPing         PacketType = 2
	PacketPong         PacketType = 3
	PacketDiscoverReq  PacketType = 4
	PacketDiscoverResp PacketType = 5
)

type CodecID uint8

const (
	CodecPCM16 CodecID = 0
	CodecOpus  CodecID = 1
)

func (c CodecID) String() string {
	switch c {
	case CodecPCM16:
		return "PCM 16bit"
	case CodecOpus:
		return "Opus"
	default:
		return fmt.Sprintf("Unknown(%d)", c)
	}
}

type Header struct {
	Version     uint8
	Type        PacketType
	Codec       CodecID
	Channels    uint8
	SampleRate  uint32
	Sequence    uint32
	TimestampUs uint64
	PayloadLen  uint16
}

type DiscoverReq struct {
	InstanceID string `json:"instance_id"`
	OS         string `json:"os"`
}

type DiscoverResp struct {
	InstanceID  string `json:"instance_id"`
	DisplayName string `json:"display_name"`
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	AudioPort   int    `json:"audio_port"`
}

type PingPayload struct {
	InstanceID string `json:"instance_id"`
	SentAtUs   int64  `json:"sent_at_us"`
}

type PongPayload struct {
	InstanceID  string  `json:"instance_id"`
	DisplayName string  `json:"display_name"`
	OS          string  `json:"os"`
	EchoAtUs    int64   `json:"echo_at_us"`
	LossPercent float64 `json:"loss_percent"`
	JitterMs    float64 `json:"jitter_ms"`
	BufferMs    float64 `json:"buffer_ms"`
}

var (
	ErrTooShort      = errors.New("protocol: packet too short")
	ErrInvalidMagic  = errors.New("protocol: invalid magic")
	ErrBadVersion    = errors.New("protocol: unsupported version")
	ErrPayloadTooBig = errors.New("protocol: payload too large")
	ErrTruncated     = errors.New("protocol: payload truncated")
)

// Marshal encodes a header and payload into dst (or allocates if dst is too small).
func Marshal(dst []byte, h Header, payload []byte) ([]byte, error) {
	if len(payload) > MaxPayloadSize {
		return nil, ErrPayloadTooBig
	}
	total := HeaderSize + len(payload)
	if cap(dst) < total {
		dst = make([]byte, total)
	} else {
		dst = dst[:total]
	}
	if h.Version == 0 {
		h.Version = Version
	}
	copy(dst[0:4], Magic)
	dst[4] = h.Version
	dst[5] = uint8(h.Type)
	dst[6] = uint8(h.Codec)
	dst[7] = h.Channels
	binary.LittleEndian.PutUint32(dst[8:12], h.SampleRate)
	binary.LittleEndian.PutUint32(dst[12:16], h.Sequence)
	binary.LittleEndian.PutUint64(dst[16:24], h.TimestampUs)
	binary.LittleEndian.PutUint16(dst[24:26], uint16(len(payload)))
	copy(dst[HeaderSize:], payload)
	return dst, nil
}

// Unmarshal decodes a packet header and returns a slice referencing the payload.
func Unmarshal(data []byte) (Header, []byte, error) {
	if len(data) < HeaderSize {
		return Header{}, nil, ErrTooShort
	}
	if string(data[0:4]) != Magic {
		return Header{}, nil, ErrInvalidMagic
	}
	if data[4] != Version {
		return Header{}, nil, ErrBadVersion
	}
	payloadLen := binary.LittleEndian.Uint16(data[24:26])
	if int(payloadLen) > MaxPayloadSize {
		return Header{}, nil, ErrPayloadTooBig
	}
	if len(data) < HeaderSize+int(payloadLen) {
		return Header{}, nil, ErrTruncated
	}
	h := Header{
		Version:     data[4],
		Type:        PacketType(data[5]),
		Codec:       CodecID(data[6]),
		Channels:    data[7],
		SampleRate:  binary.LittleEndian.Uint32(data[8:12]),
		Sequence:    binary.LittleEndian.Uint32(data[12:16]),
		TimestampUs: binary.LittleEndian.Uint64(data[16:24]),
		PayloadLen:  payloadLen,
	}
	return h, data[HeaderSize : HeaderSize+int(payloadLen)], nil
}

func MarshalJSONPacket(pktType PacketType, v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Marshal(nil, Header{
		Version: Version,
		Type:    pktType,
	}, b)
}
