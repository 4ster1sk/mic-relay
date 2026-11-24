package main

import (
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"mic-relay/internal/audio"
	"mic-relay/internal/config"
	"mic-relay/internal/dsp"
	"mic-relay/internal/jitter"
	"mic-relay/internal/netrelay"
	"mic-relay/internal/protocol"
	"mic-relay/internal/uiutil"
)

func TestGUILayoutAndLiveVUMeter(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	a.Settings().SetTheme(uiutil.NewAppTheme())

	w := a.NewWindow("Mic Relay Test")
	cfg := config.Default()
	cfg.ActiveTab = "tx"
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	txDSP := dsp.NewProcessor(protocol.SampleRate)
	rxDSP := dsp.NewProcessor(protocol.SampleRate)
	jb := jitter.NewBuffer(protocol.SampleRate, 30)

	ra := &RelayApp{
		fyneApp:    a,
		win:        w,
		cfgPath:    cfgPath,
		cfg:        cfg,
		instanceID: "test-inst",
		txDSP:      txDSP,
		rxDSP:      rxDSP,
		jb:         jb,
		capturer:   audio.NewCapturer(),
		player:     audio.NewPlayer(),
		virtualMic: audio.NewVirtualMic(),
		sender:     netrelay.NewSender("test-inst"),
		receiver:   netrelay.NewReceiver("test-inst", "test", jb),
		discovery:  netrelay.NewDiscoveryResponder("test-inst", "test", 48101),
		stopTicker: make(chan struct{}),
	}

	ra.buildUI()

	// Populate peer table rows
	ra.txPeersMu.Lock()
	ra.txPeerRows = []peerRow{
		{Kind: "LAN / Win", DisplayName: "DESKTOP-VRC", Host: "192.168.1.42", Port: 48101, RTTText: "2.1 ms"},
		{Kind: "TS / Win", DisplayName: "desktop-vrc.tail1a2b.ts.net", Host: "100.88.12.5", Port: 48101, RTTText: "14.5 ms"},
		{Kind: "Preset", DisplayName: "サブ Windows 機", Host: "192.168.1.50", Port: 48101, RTTText: "--", IsPreset: true, PresetIndex: 0},
	}
	ra.txPeersMu.Unlock()
	ra.txPeerTable.Refresh()

	// Simulate live mic audio BEFORE TX starts -> verify VU meter updates immediately
	frame := make([]float32, protocol.FrameSamples10ms)
	for i := range frame {
		frame[i] = 0.45 * float32(math.Sin(2*math.Pi*440*float64(i)/float64(protocol.SampleRate)))
	}
	ra.onCaptureFrame(frame)
	rms, _, peakHold := ra.txDSP.Levels()
	if rms <= dsp.MinDB+10 {
		t.Fatalf("expected live VU level to rise above silence, got %.1f dB", rms)
	}
	ra.txVUMeter.SetLevels(rms, peakHold)
	ra.txDBLabel.SetText("-10.0 dB")

	// Render at default 700x820 and resized 920x820
	w.Resize(fyne.NewSize(700, 820))

	if outDir := os.Getenv("MIC_RELAY_SCREENSHOT_DIR"); outDir != "" {
		_ = os.MkdirAll(outDir, 0o755)
		saveCapture := func(name string) {
			img := w.Canvas().Capture()
			f, err := os.Create(filepath.Join(outDir, name))
			if err == nil {
				_ = png.Encode(f, img)
				_ = f.Close()
			}
		}
		saveCapture("tx_700x820.png")

		w.Resize(fyne.NewSize(920, 820))
		saveCapture("tx_920x820.png")

		ra.tabs.SelectIndex(1)
		w.Resize(fyne.NewSize(700, 820))
		saveCapture("rx_700x820.png")
	}
}
