package netrelay

import (
	"context"
	"math"
	"testing"
	"time"

	"mic-relay/internal/config"
	"mic-relay/internal/jitter"
	"mic-relay/internal/protocol"
)

func TestBidirectionalLoopbackWithDynamicCodecSwitch(t *testing.T) {
	jbA := jitter.NewBuffer(protocol.SampleRate, 30)
	jbB := jitter.NewBuffer(protocol.SampleRate, 30)

	rxA := NewReceiver("inst-A", "Linux-Node", jbA)
	rxB := NewReceiver("inst-B", "Windows-Node", jbB)

	// Bind on ephemeral ports on localhost
	if err := rxA.Start(0); err != nil {
		// Note: Start(0) uses DefaultAudioPort if <=0, so pick two high ports
		t.Fatalf("rxA start: %v", err)
	}
	rxA.Stop()

	if err := rxA.Start(48211); err != nil {
		t.Fatalf("rxA start: %v", err)
	}
	defer rxA.Stop()
	if err := rxB.Start(48212); err != nil {
		t.Fatalf("rxB start: %v", err)
	}
	defer rxB.Stop()

	txA := NewSender("inst-A")
	txB := NewSender("inst-B")
	if err := txA.Start("127.0.0.1", rxB.ListenPort(), config.CodecModePCM); err != nil {
		t.Fatalf("txA start: %v", err)
	}
	defer txA.Stop()
	if err := txB.Start("127.0.0.1", rxA.ListenPort(), config.CodecModeOpus96); err != nil {
		t.Fatalf("txB start: %v", err)
	}
	defer txB.Stop()

	// Probe B from A
	pong, rtt, err := ProbePeer(context.Background(), "inst-A", "127.0.0.1", rxB.ListenPort(), time.Second)
	if err != nil {
		t.Fatalf("ProbePeer failed: %v", err)
	}
	if pong.DisplayName != "Windows-Node" || rtt < 0 {
		t.Fatalf("unexpected pong: %+v rtt=%f", pong, rtt)
	}

	frame := make([]float32, protocol.FrameSamples10ms)
	for i := range frame {
		frame[i] = 0.4 * float32(math.Sin(2*math.Pi*440*float64(i)/float64(protocol.SampleRate)))
	}

	// Send PCM from A -> B and Opus from B -> A simultaneously
	for i := 0; i < 10; i++ {
		if err := txA.SendFrame(frame); err != nil {
			t.Fatalf("txA PCM send: %v", err)
		}
		if err := txB.SendFrame(frame); err != nil {
			t.Fatalf("txB Opus send: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Dynamically switch A -> B from PCM to Opus48 mid-stream
	if err := txA.SetCodecMode(config.CodecModeOpus48); err != nil {
		t.Fatalf("txA switch to Opus48: %v", err)
	}
	for i := 0; i < 10; i++ {
		if err := txA.SendFrame(frame); err != nil {
			t.Fatalf("txA Opus48 send: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(80 * time.Millisecond)

	stA := rxA.Stats()
	stB := rxB.Stats()
	if !stA.Active || stA.Jitter.PacketsReceived == 0 {
		t.Fatalf("expected rxA to receive Opus stream from B: %+v", stA)
	}
	if !stB.Active || stB.Jitter.PacketsReceived < 10 {
		t.Fatalf("expected rxB to receive PCM+Opus stream from A: %+v", stB)
	}
	if stB.CodecLabel != "Opus" {
		t.Fatalf("expected rxB to auto-switch to Opus, got %q", stB.CodecLabel)
	}

	// Verify audio readout from jitter buffer is non-silent
	out := make([]float32, 480)
	jbB.Read(out)
	var sum float64
	for _, s := range out {
		sum += math.Abs(float64(s))
	}
	if sum == 0 {
		t.Fatalf("expected non-zero decoded audio in rxB jitter buffer")
	}
}

func TestParseTailscaleStatusJSON(t *testing.T) {
	sample := []byte(`{
		"Peer": {
			"k1": {"HostName": "win-vrc", "DNSName": "win-vrc.tail1234.ts.net.", "OS": "windows", "Online": true, "TailscaleIPs": ["100.64.1.10", "fd7a::1"]},
			"k2": {"HostName": "offline-box", "DNSName": "offline.tail1234.ts.net.", "OS": "linux", "Online": false, "TailscaleIPs": ["100.64.1.11"]}
		}
	}`)
	peers := parseTailscaleStatusJSON(sample, 48101)
	if len(peers) != 1 {
		t.Fatalf("expected 1 online peer, got %d", len(peers))
	}
	if peers[0].Host != "100.64.1.10" || peers[0].OS != "windows" || peers[0].DisplayName != "win-vrc.tail1234.ts.net" {
		t.Fatalf("unexpected peer parsed: %+v", peers[0])
	}
}
