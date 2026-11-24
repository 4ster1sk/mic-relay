package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

	"mic-relay/internal/audio"
	"mic-relay/internal/config"
	"mic-relay/internal/dsp"
	"mic-relay/internal/jitter"
	"mic-relay/internal/netrelay"
	"mic-relay/internal/protocol"
	"mic-relay/internal/uiutil"
)

func generateInstanceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("inst-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func main() {
	cfgPath, err := config.DefaultPath()
	if err != nil {
		cfgPath = "mic-relay-config.json"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Printf("config load warning: %v", err)
	}

	instanceID := generateInstanceID()

	txDSP := dsp.NewProcessor(protocol.SampleRate)
	txDSP.SetGainDB(cfg.TX.GainDB)
	txDSP.SetNoiseGate(cfg.TX.NoiseGateEnabled, cfg.TX.GateThresholdDB)

	rxDSP := dsp.NewProcessor(protocol.SampleRate)
	rxDSP.SetVolumePercent(cfg.RX.OutputVolumePercent)

	denoiser, err := dsp.NewDenoiser(protocol.SampleRate, cfg.TX.NoiseSuppression)
	if err != nil {
		log.Printf("rnnoise init warning (noise suppression disabled): %v", err)
	}

	voiceChanger := dsp.NewVoiceChanger(protocol.SampleRate)
	voiceChanger.SetParams(
		cfg.TX.VoiceChanger.PitchSemitones,
		cfg.TX.VoiceChanger.FormantRatio,
		cfg.TX.VoiceChanger.HighPassEnabled,
	)
	voiceChanger.SetEnabled(cfg.TX.VoiceChanger.Enabled)

	jb := jitter.NewBuffer(protocol.SampleRate, cfg.RX.TargetBufferMs)

	a := app.NewWithID("dev.antigravity.micrelay")
	a.Settings().SetTheme(uiutil.NewAppTheme())
	a.SetIcon(uiutil.AppIcon())

	osLabel := "Linux / PipeWire"
	if runtime.GOOS == "windows" {
		osLabel = "Windows / VB-Cable"
	}
	w := a.NewWindow(fmt.Sprintf("Mic Relay — 双方向マイク中継 (%s)", osLabel))
	w.Resize(fyne.NewSize(700, 710))
	w.SetIcon(uiutil.AppIcon())

	ra := &RelayApp{
		fyneApp:    a,
		win:        w,
		cfgPath:    cfgPath,
		cfg:        cfg,
		instanceID: instanceID,
		txDSP:         txDSP,
		rxDSP:         rxDSP,
		denoiser:      denoiser,
		voiceChanger:  voiceChanger,
		jb:            jb,
		capturer:      audio.NewCapturer(),
		player:        audio.NewPlayer(),
		monitorPlayer: audio.NewPlayer(),
		monitorBuf:    newMonitorRing(),
		virtualMic:    audio.NewVirtualMic(),
		sender:     netrelay.NewSender(instanceID),
		receiver:   netrelay.NewReceiver(instanceID, cfg.RX.DisplayName, jb),
		discovery:  netrelay.NewDiscoveryResponder(instanceID, cfg.RX.DisplayName, cfg.RX.ListenPort),
		stopTicker: make(chan struct{}),
	}

	ra.buildUI()
	ra.setupTrayAndClose()

	// Start live microphone monitoring so the input VU meter updates in real time even before TX starts
	go func() {
		if err := ra.startMicMonitor(); err != nil {
			log.Printf("initial mic monitor warning: %v", err)
		}
	}()

	// Start periodic UI refresh (20Hz for smooth VU meters & live stats)
	go ra.uiRefreshLoop()

	// Kick off initial LAN/Tailscale discovery
	go ra.refreshPeers()

	w.ShowAndRun()
	ra.shutdown()
}
