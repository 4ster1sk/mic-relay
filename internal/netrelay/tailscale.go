package netrelay

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"mic-relay/internal/protocol"
)

type tailscaleStatus struct {
	Peer map[string]struct {
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		OS           string   `json:"OS"`
		Online       bool     `json:"Online"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Peer"`
}

// DiscoverTailscalePeers queries `tailscale status --json` if installed and returns online peers.
// Returns nil without error if tailscale CLI is not installed or unavailable.
func DiscoverTailscalePeers(ctx context.Context, defaultPort int) []DiscoveredPeer {
	if defaultPort <= 0 {
		defaultPort = protocol.DefaultAudioPort
	}
	tsBin, err := exec.LookPath("tailscale")
	if err != nil {
		return nil
	}
	tctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	out, err := exec.CommandContext(tctx, tsBin, "status", "--json").Output()
	if err != nil {
		return nil
	}
	return parseTailscaleStatusJSON(out, defaultPort)
}

func parseTailscaleStatusJSON(data []byte, defaultPort int) []DiscoveredPeer {
	var st tailscaleStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil
	}
	var peers []DiscoveredPeer
	for _, p := range st.Peer {
		if !p.Online || len(p.TailscaleIPs) == 0 {
			continue
		}
		// Prefer IPv4 (100.x.y.z)
		ip := p.TailscaleIPs[0]
		for _, cand := range p.TailscaleIPs {
			if !strings.Contains(cand, ":") {
				ip = cand
				break
			}
		}
		dnsName := strings.TrimSuffix(p.DNSName, ".")
		display := dnsName
		if display == "" {
			display = p.HostName
		}
		peers = append(peers, DiscoveredPeer{
			Kind:        "Tailscale",
			DisplayName: display,
			Hostname:    p.HostName,
			OS:          strings.ToLower(p.OS),
			Host:        ip,
			Port:        defaultPort,
			LastSeen:    time.Now(),
		})
	}
	return peers
}
