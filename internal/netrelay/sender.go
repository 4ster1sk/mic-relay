package netrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"mic-relay/internal/codec"
	"mic-relay/internal/config"
	"mic-relay/internal/protocol"
)

type SenderStats struct {
	Connected       bool
	TargetAddr      string
	CodecLabel      string
	BitrateKbps     int
	RTTMs           float64
	RemoteLossPct   float64
	RemoteJitterMs  float64
	RemoteBufferMs  float64
	PacketsSent     uint64
}

// Sender encodes mono 48kHz float32 frames and streams them over UDP, with live codec switching
// and 1-second Ping/Pong RTT + remote health monitoring.
type Sender struct {
	mu          sync.Mutex
	instanceID  string
	conn        *net.UDPConn
	targetAddr  string
	enc         codec.Encoder
	codecMode   config.CodecMode
	frameAcc    []float32
	payloadBuf  []byte
	packetBuf   []byte
	seq         uint32
	running     atomic.Bool
	cancel      context.CancelFunc

	statsMu        sync.RWMutex
	stats          SenderStats
	lastPongAt     time.Time
}

func NewSender(instanceID string) *Sender {
	return &Sender{
		instanceID: instanceID,
		codecMode:  config.CodecModePCM,
		enc:        codec.NewPCM16Encoder(),
		frameAcc:   make([]float32, 0, protocol.FrameSamples20ms*2),
		payloadBuf: make([]byte, 2048),
		packetBuf:  make([]byte, 2048),
	}
}

func (s *Sender) SetCodecMode(mode config.CodecMode) error {
	enc, err := codec.NewEncoderForMode(mode)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.codecMode = mode
	s.enc = enc
	s.frameAcc = s.frameAcc[:0]
	s.mu.Unlock()

	s.statsMu.Lock()
	s.stats.CodecLabel = enc.CodecID().String()
	s.stats.BitrateKbps = enc.BitrateKbps()
	s.statsMu.Unlock()
	return nil
}

func (s *Sender) Start(host string, port int, mode config.CodecMode) error {
	s.Stop()
	if port <= 0 {
		port = protocol.DefaultAudioPort
	}
	if err := s.SetCodecMode(mode); err != nil {
		return err
	}
	addrStr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	raddr, err := net.ResolveUDPAddr("udp", addrStr)
	if err != nil {
		return fmt.Errorf("送信先アドレスの解決に失敗: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return fmt.Errorf("UDP 接続の開始に失敗: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.conn = conn
	s.targetAddr = addrStr
	s.frameAcc = s.frameAcc[:0]
	s.seq = 0
	s.cancel = cancel
	s.running.Store(true)
	encLabel := s.enc.CodecID().String()
	kbps := s.enc.BitrateKbps()
	s.mu.Unlock()

	s.statsMu.Lock()
	s.stats = SenderStats{
		Connected:   true,
		TargetAddr:  addrStr,
		CodecLabel:  encLabel,
		BitrateKbps: kbps,
	}
	s.statsMu.Unlock()

	go s.pingLoop(ctx, conn)
	go s.pongReadLoop(ctx, conn)
	return nil
}

func (s *Sender) Stop() {
	s.running.Store(false)
	s.mu.Lock()
	cancel := s.cancel
	conn := s.conn
	s.cancel = nil
	s.conn = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}

	s.statsMu.Lock()
	s.stats.Connected = false
	s.stats.RTTMs = 0
	s.statsMu.Unlock()
}

func (s *Sender) IsRunning() bool {
	return s.running.Load()
}

// SendFrame accepts 48kHz mono float32 samples (typically 480 samples = 10ms),
// accumulates to the active encoder's frame size (10ms PCM or 20ms Opus), and transmits UDP packets.
func (s *Sender) SendFrame(samples []float32) error {
	if !s.running.Load() || len(samples) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil || s.enc == nil {
		return nil
	}

	s.frameAcc = append(s.frameAcc, samples...)
	need := s.enc.FrameSamples()
	for len(s.frameAcc) >= need {
		chunk := s.frameAcc[:need]
		payload, err := s.enc.Encode(chunk, s.payloadBuf)
		if err != nil {
			// Drop this frame and advance
			copy(s.frameAcc, s.frameAcc[need:])
			s.frameAcc = s.frameAcc[:len(s.frameAcc)-need]
			return err
		}
		s.payloadBuf = payload[:cap(payload)]

		h := protocol.Header{
			Version:     protocol.Version,
			Type:        protocol.PacketAudio,
			Codec:       s.enc.CodecID(),
			Channels:    protocol.Channels,
			SampleRate:  protocol.SampleRate,
			Sequence:    s.seq,
			TimestampUs: uint64(time.Now().UnixMicro()),
		}
		s.seq++

		pkt, err := protocol.Marshal(s.packetBuf, h, payload)
		if err != nil {
			return err
		}
		s.packetBuf = pkt[:cap(pkt)]
		_, _ = s.conn.Write(pkt)

		s.statsMu.Lock()
		s.stats.PacketsSent++
		s.statsMu.Unlock()

		copy(s.frameAcc, s.frameAcc[need:])
		s.frameAcc = s.frameAcc[:len(s.frameAcc)-need]
	}
	return nil
}

func (s *Sender) pingLoop(ctx context.Context, conn *net.UDPConn) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	sendPing := func() {
		pkt, err := protocol.MarshalJSONPacket(protocol.PacketPing, protocol.PingPayload{
			InstanceID: s.instanceID,
			SentAtUs:   time.Now().UnixMicro(),
		})
		if err == nil {
			_, _ = conn.Write(pkt)
		}
	}
	sendPing()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sendPing()
		}
	}
}

func (s *Sender) pongReadLoop(ctx context.Context, conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		h, payload, err := protocol.Unmarshal(buf[:n])
		if err != nil || h.Type != protocol.PacketPong {
			continue
		}
		var pong protocol.PongPayload
		if err := json.Unmarshal(payload, &pong); err != nil {
			continue
		}
		rttMs := float64(time.Now().UnixMicro()-pong.EchoAtUs) / 1000.0
		if rttMs < 0 {
			rttMs = 0
		}
		s.statsMu.Lock()
		s.stats.RTTMs = rttMs
		s.stats.RemoteLossPct = pong.LossPercent
		s.stats.RemoteJitterMs = pong.JitterMs
		s.stats.RemoteBufferMs = pong.BufferMs
		s.lastPongAt = time.Now()
		s.statsMu.Unlock()
	}
}

func (s *Sender) Stats() SenderStats {
	s.statsMu.RLock()
	defer s.statsMu.RUnlock()
	st := s.stats
	if !s.lastPongAt.IsZero() && time.Since(s.lastPongAt) > 5*time.Second {
		st.RTTMs = 0
	}
	return st
}
