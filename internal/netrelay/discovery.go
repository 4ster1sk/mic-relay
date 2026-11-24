package netrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"mic-relay/internal/protocol"
)

type DiscoveredPeer struct {
	Kind        string // "LAN", "Tailscale", "Preset"
	InstanceID  string
	DisplayName string
	Hostname    string
	OS          string
	Host        string
	Port        int
	RTTMs       float64 // <= 0 means unknown
	LastSeen    time.Time
}

// DiscoveryResponder listens on UDP port 48100 and replies to LAN discovery probes.
type DiscoveryResponder struct {
	mu          sync.RWMutex
	instanceID  string
	displayName string
	audioPort   int
	conn        *net.UDPConn
	cancel      context.CancelFunc
}

func NewDiscoveryResponder(instanceID, displayName string, audioPort int) *DiscoveryResponder {
	return &DiscoveryResponder{
		instanceID:  instanceID,
		displayName: displayName,
		audioPort:   audioPort,
	}
}

func (d *DiscoveryResponder) UpdateInfo(displayName string, audioPort int) {
	d.mu.Lock()
	d.displayName = displayName
	d.audioPort = audioPort
	d.mu.Unlock()
}

func (d *DiscoveryResponder) Start(port int) error {
	d.Stop()
	if port <= 0 {
		port = protocol.DefaultDiscoveryPort
	}
	addr := &net.UDPAddr{IP: net.IPv4zero, Port: port}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.mu.Lock()
	d.conn = conn
	d.cancel = cancel
	d.mu.Unlock()

	hostname, _ := os.Hostname()
	go func() {
		buf := make([]byte, 2048)
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
			if err != nil || h.Type != protocol.PacketDiscoverReq {
				continue
			}
			var req protocol.DiscoverReq
			if err := json.Unmarshal(payload, &req); err != nil {
				continue
			}
			d.mu.RLock()
			myID := d.instanceID
			disp := d.displayName
			aport := d.audioPort
			d.mu.RUnlock()

			// Ignore self discovery
			if req.InstanceID != "" && req.InstanceID == myID {
				continue
			}

			resp := protocol.DiscoverResp{
				InstanceID:  myID,
				DisplayName: disp,
				Hostname:    hostname,
				OS:          runtime.GOOS,
				AudioPort:   aport,
			}
			pkt, err := protocol.MarshalJSONPacket(protocol.PacketDiscoverResp, resp)
			if err != nil {
				continue
			}
			_, _ = conn.WriteToUDP(pkt, remote)
		}
	}()
	return nil
}

func (d *DiscoveryResponder) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	conn := d.conn
	d.cancel = nil
	d.conn = nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
}

// DiscoverLAN broadcasts a discovery request on the LAN and collects responses until timeout.
func DiscoverLAN(ctx context.Context, instanceID string, discoveryPort int, timeout time.Duration) ([]DiscoveredPeer, error) {
	if discoveryPort <= 0 {
		discoveryPort = protocol.DefaultDiscoveryPort
	}
	if timeout <= 0 {
		timeout = 1200 * time.Millisecond
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	reqPkt, err := protocol.MarshalJSONPacket(protocol.PacketDiscoverReq, protocol.DiscoverReq{
		InstanceID: instanceID,
		OS:         runtime.GOOS,
	})
	if err != nil {
		return nil, err
	}

	// Send to global broadcast + subnet broadcasts for multi-NIC reliability
	targets := broadcastAddrs(discoveryPort)
	for _, dst := range targets {
		_, _ = conn.WriteToUDP(reqPkt, dst)
	}

	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetReadDeadline(deadline)

	seen := make(map[string]DiscoveredPeer)
	buf := make([]byte, 2048)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		h, payload, err := protocol.Unmarshal(buf[:n])
		if err != nil || h.Type != protocol.PacketDiscoverResp {
			continue
		}
		var resp protocol.DiscoverResp
		if err := json.Unmarshal(payload, &resp); err != nil {
			continue
		}
		if resp.InstanceID != "" && resp.InstanceID == instanceID {
			continue
		}
		key := fmt.Sprintf("%s:%d", remote.IP.String(), resp.AudioPort)
		name := resp.DisplayName
		if name == "" {
			name = resp.Hostname
		}
		seen[key] = DiscoveredPeer{
			Kind:        "LAN",
			InstanceID:  resp.InstanceID,
			DisplayName: name,
			Hostname:    resp.Hostname,
			OS:          resp.OS,
			Host:        remote.IP.String(),
			Port:        resp.AudioPort,
			LastSeen:    time.Now(),
		}
	}

	out := make([]DiscoveredPeer, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	return out, nil
}

func broadcastAddrs(port int) []*net.UDPAddr {
	addrs := []*net.UDPAddr{{IP: net.IPv4bcast, Port: port}}
	ifaces, err := net.Interfaces()
	if err != nil {
		return addrs
	}
	seen := map[string]bool{net.IPv4bcast.String(): true}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagBroadcast == 0 {
			continue
		}
		ifAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifAddrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || len(ipNet.Mask) != 4 {
				continue
			}
			bcast := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				bcast[i] = ip4[i] | ^ipNet.Mask[i]
			}
			s := bcast.String()
			if !seen[s] {
				seen[s] = true
				addrs = append(addrs, &net.UDPAddr{IP: bcast, Port: port})
			}
		}
	}
	return addrs
}

// ProbePeer sends a single UDP Ping to host:port and waits for Pong, returning RTT and peer metadata.
func ProbePeer(ctx context.Context, instanceID, host string, port int, timeout time.Duration) (protocol.PongPayload, float64, error) {
	if port <= 0 {
		port = protocol.DefaultAudioPort
	}
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	raddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		return protocol.PongPayload{}, 0, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return protocol.PongPayload{}, 0, err
	}
	defer conn.Close()

	sentAt := time.Now()
	pkt, err := protocol.MarshalJSONPacket(protocol.PacketPing, protocol.PingPayload{
		InstanceID: instanceID,
		SentAtUs:   sentAt.UnixMicro(),
	})
	if err != nil {
		return protocol.PongPayload{}, 0, err
	}
	if _, err := conn.Write(pkt); err != nil {
		return protocol.PongPayload{}, 0, err
	}

	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetReadDeadline(deadline)

	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		return protocol.PongPayload{}, 0, err
	}
	h, payload, err := protocol.Unmarshal(buf[:n])
	if err != nil || h.Type != protocol.PacketPong {
		return protocol.PongPayload{}, 0, fmt.Errorf("unexpected response")
	}
	var pong protocol.PongPayload
	if err := json.Unmarshal(payload, &pong); err != nil {
		return protocol.PongPayload{}, 0, err
	}
	rttMs := float64(time.Since(sentAt).Microseconds()) / 1000.0
	return pong, rttMs, nil
}
