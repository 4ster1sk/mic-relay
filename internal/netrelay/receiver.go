package netrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"mic-relay/internal/codec"
	"mic-relay/internal/jitter"
	"mic-relay/internal/protocol"
)

type ReceiverStats struct {
	Listening   bool
	ListenPort  int
	Active      bool
	RemoteAddr  string
	CodecLabel  string
	Jitter      jitter.Stats
}

// Receiver listens on UDP 0.0.0.0:port for audio and ping packets, automatically switches decoders
// when the sender changes codec (PCM <-> Opus), and feeds decoded PCM into a jitter buffer.
type Receiver struct {
	mu          sync.Mutex
	instanceID  string
	displayName string
	listenPort  int
	conn        *net.UDPConn
	jb          *jitter.Buffer
	running     atomic.Bool
	cancel      context.CancelFunc
	wg          sync.WaitGroup

	statsMu    sync.RWMutex
	remoteAddr string
	lastCodec  protocol.CodecID
	lastRxTime time.Time
}

func NewReceiver(instanceID, displayName string, jb *jitter.Buffer) *Receiver {
	return &Receiver{
		instanceID:  instanceID,
		displayName: displayName,
		jb:          jb,
	}
}

func (r *Receiver) SetDisplayName(name string) {
	r.mu.Lock()
	r.displayName = name
	r.mu.Unlock()
}

func (r *Receiver) Start(port int) error {
	r.Stop()
	if port <= 0 {
		port = protocol.DefaultAudioPort
	}
	addr := &net.UDPAddr{Port: port}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("UDP ポート %d の待受に失敗: %w", port, err)
	}
	_ = conn.SetReadBuffer(256 * 1024)

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.conn = conn
	r.listenPort = conn.LocalAddr().(*net.UDPAddr).Port
	r.cancel = cancel
	r.running.Store(true)
	r.mu.Unlock()

	r.jb.Reset()
	r.wg.Add(1)
	go r.readLoop(ctx, conn)
	return nil
}

func (r *Receiver) Stop() {
	r.running.Store(false)
	r.mu.Lock()
	cancel := r.cancel
	conn := r.conn
	r.cancel = nil
	r.conn = nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	r.wg.Wait()
	r.jb.Reset()
}

func (r *Receiver) IsRunning() bool {
	return r.running.Load()
}

func (r *Receiver) ListenPort() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listenPort
}

func (r *Receiver) readLoop(ctx context.Context, conn *net.UDPConn) {
	defer r.wg.Done()

	decoders := make(map[protocol.CodecID]codec.Decoder)
	pcmScratch := make([]float32, protocol.FrameSamples20ms*2)
	getDecoder := func(id protocol.CodecID) (codec.Decoder, error) {
		if dec, ok := decoders[id]; ok {
			return dec, nil
		}
		dec, err := codec.NewDecoderForID(id)
		if err != nil {
			return nil, err
		}
		decoders[id] = dec
		return dec, nil
	}

	buf := make([]byte, protocol.HeaderSize+protocol.MaxPayloadSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		h, payload, err := protocol.Unmarshal(buf[:n])
		if err != nil {
			continue
		}

		switch h.Type {
		case protocol.PacketAudio:
			dec, err := getDecoder(h.Codec)
			if err != nil {
				continue
			}
			samples, err := dec.Decode(payload, pcmScratch)
			if err != nil || len(samples) == 0 {
				continue
			}
			pcmScratch = samples[:cap(samples)]
			r.jb.Push(h.Sequence, h.TimestampUs, samples)

			r.statsMu.Lock()
			r.remoteAddr = remote.IP.String()
			r.lastCodec = h.Codec
			r.lastRxTime = time.Now()
			r.statsMu.Unlock()

		case protocol.PacketPing:
			var ping protocol.PingPayload
			if err := json.Unmarshal(payload, &ping); err != nil {
				continue
			}
			jstats := r.jb.Stats()
			r.mu.Lock()
			disp := r.displayName
			myID := r.instanceID
			r.mu.Unlock()
			pong := protocol.PongPayload{
				InstanceID:  myID,
				DisplayName: disp,
				OS:          runtime.GOOS,
				EchoAtUs:    ping.SentAtUs,
				LossPercent: jstats.LossPercent,
				JitterMs:    jstats.JitterMs,
				BufferMs:    jstats.BufferedMs,
			}
			pkt, err := protocol.MarshalJSONPacket(protocol.PacketPong, pong)
			if err == nil {
				_, _ = conn.WriteToUDP(pkt, remote)
			}
		}
	}
}

func (r *Receiver) Stats() ReceiverStats {
	r.mu.Lock()
	port := r.listenPort
	r.mu.Unlock()

	r.statsMu.RLock()
	remote := r.remoteAddr
	lastCodec := r.lastCodec
	lastRx := r.lastRxTime
	r.statsMu.RUnlock()

	active := !lastRx.IsZero() && time.Since(lastRx) < 2*time.Second
	codecLabel := "--"
	if active {
		codecLabel = lastCodec.String()
	}

	return ReceiverStats{
		Listening:  r.running.Load(),
		ListenPort: port,
		Active:     active,
		RemoteAddr: remote,
		CodecLabel: codecLabel,
		Jitter:     r.jb.Stats(),
	}
}
