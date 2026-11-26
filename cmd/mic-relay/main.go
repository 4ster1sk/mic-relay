package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
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

var appVersion = "0.1.1"

type cliOptions struct {
	minimized   bool
	startTX     bool
	startRX     bool
	showVersion bool
}

func parseCLIOptions(args []string, errOut io.Writer) (cliOptions, error) {
	var opts cliOptions
	fs := flag.NewFlagSet("mic-relay", flag.ContinueOnError)
	if errOut != nil {
		fs.SetOutput(errOut)
	}
	fs.BoolVar(&opts.minimized, "minimized", false, "起動時にウィンドウを表示せずシステムトレイに最小化して起動する")
	fs.BoolVar(&opts.startTX, "tx", false, "起動時に送信 (TX) を自動で開始する (-rx と同時指定不可)")
	fs.BoolVar(&opts.startRX, "rx", false, "起動時に受信待機 (RX) を自動で開始する (-tx と同時指定不可)")
	fs.BoolVar(&opts.showVersion, "version", false, "バージョンを表示して終了する")

	if err := fs.Parse(args); err != nil {
		return opts, err
	}
	if opts.startTX && opts.startRX {
		return opts, fmt.Errorf("-tx と -rx は同時に指定できません")
	}
	return opts, nil
}

func generateInstanceID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("inst-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func main() {
	opts, err := parseCLIOptions(os.Args[1:], os.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "mic-relay: %v\n", err)
		os.Exit(2)
	}
	if opts.showVersion {
		fmt.Printf("mic-relay %s\n", appVersion)
		return
	}

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
	voiceChanger.SetConfig(dsp.VoiceChangerParams{
		PitchSemitones:  cfg.TX.VoiceChanger.PitchSemitones,
		FormantRatio:    cfg.TX.VoiceChanger.FormantRatio,
		ThroatFormant:   cfg.TX.VoiceChanger.ThroatFormant,
		BrightFormant:   cfg.TX.VoiceChanger.BrightFormant,
		Breathiness:     cfg.TX.VoiceChanger.Breathiness,
		Softness:        cfg.TX.VoiceChanger.Softness,
		HighPassEnabled: cfg.TX.VoiceChanger.HighPassEnabled,
		HighPassHz:      cfg.TX.VoiceChanger.HighPassHz,
		DeEsser:         cfg.TX.VoiceChanger.DeEsser,
	})
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
	w.Resize(fyne.NewSize(700, 740))
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
		monitorPlayer:  audio.NewPlayer(),
		monitorBuf:     newMonitorRing(),
		localOutPlayer: audio.NewPlayer(),
		localOutBuf:    newMonitorRing(),
		virtualMic:     audio.NewVirtualMic(),
		sender:     netrelay.NewSender(instanceID),
		receiver:   netrelay.NewReceiver(instanceID, cfg.RX.DisplayName, jb),
		discovery:  netrelay.NewDiscoveryResponder(instanceID, cfg.RX.DisplayName, cfg.RX.ListenPort),
		stopTicker: make(chan struct{}),
	}

	ra.buildUI()
	ra.applyStartupTab(opts)
	ra.setupTrayAndClose()

	// Start live microphone monitoring so the input VU meter updates in real time even before TX starts,
	// then apply optional CLI auto-start (-tx / -rx) or local virtual mic output without racing on capturer.
	go ra.runStartupTasks(opts)

	// Start periodic UI refresh (20Hz for smooth VU meters & live stats)
	go ra.uiRefreshLoop()

	// Kick off initial LAN/Tailscale discovery
	go ra.refreshPeers()

	if opts.minimized {
		if ra.tray != nil {
			a.Run()
		} else {
			log.Printf("system tray unavailable; starting with window visible")
			w.ShowAndRun()
		}
	} else {
		w.ShowAndRun()
	}
	ra.shutdown()
}
