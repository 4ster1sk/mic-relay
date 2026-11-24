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
		fyneApp:       a,
		win:           w,
		cfgPath:       cfgPath,
		cfg:           cfg,
		instanceID:    "test-inst",
		txDSP:         txDSP,
		rxDSP:         rxDSP,
		voiceChanger:  dsp.NewVoiceChanger(protocol.SampleRate),
		jb:            jb,
		capturer:      audio.NewCapturer(),
		player:        audio.NewPlayer(),
		monitorPlayer: audio.NewPlayer(),
		monitorBuf:    newMonitorRing(),
		virtualMic:    audio.NewVirtualMic(),
		sender:        netrelay.NewSender("test-inst"),
		receiver:      netrelay.NewReceiver("test-inst", "test", jb),
		discovery:     netrelay.NewDiscoveryResponder("test-inst", "test", 48101),
		stopTicker:    make(chan struct{}),
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

	// Verify main menu codec selection, voice changer presets, and minimize-to-tray toggle
	if ra.mainMenu == nil || len(ra.mainMenu.Items) != 3 {
		t.Fatalf("expected 3 top-level menus (音質, ボイチェン, 設定)")
	}
	ra.selectCodecMode(config.CodecModeOpus96)
	if !ra.menuCodecOpus96.Checked || ra.menuCodecPCM.Checked {
		t.Fatalf("expected Opus96 menu item checked after selectCodecMode")
	}
	ra.applyVoiceChangerPreset(dsp.PresetFemaleNatural, true)
	if !ra.menuVCEnable.Checked || !ra.voiceChanger.IsEnabled() {
		t.Fatalf("expected voice changer enabled via preset")
	}
	ra.applyVoiceChangerPreset(dsp.PresetFemaleWhisper, true)
	vcCfg := ra.voiceChanger.Config()
	if vcCfg.Breathiness != dsp.PresetFemaleWhisper.Breathiness || vcCfg.HighPassHz != dsp.PresetFemaleWhisper.HighPassHz {
		t.Fatalf("expected whisper preset params applied, got %+v", vcCfg)
	}
	ra.applyVoiceChangerPreset(dsp.PresetMax, true)
	vcCfg = ra.voiceChanger.Config()
	if vcCfg != dsp.PresetMax.VoiceChangerParams {
		t.Fatalf("expected max preset params applied, got %+v want %+v", vcCfg, dsp.PresetMax.VoiceChangerParams)
	}
	// Verify Voice Changer detail dialog builds cleanly without panic
	if vcUI := ra.buildVoiceChangerContent(); vcUI == nil {
		t.Fatalf("expected non-nil voice changer dialog content")
	}
	ra.setVoiceChangerEnabled(false)
	if ra.menuVCEnable.Checked || ra.voiceChanger.IsEnabled() {
		t.Fatalf("expected voice changer disabled")
	}
	ra.setMinimizeToTray(false)
	if ra.menuMinToTray.Checked {
		t.Fatalf("expected minimize-to-tray menu item unchecked")
	}
	ra.setMinimizeToTray(true)
	if !ra.menuMinToTray.Checked {
		t.Fatalf("expected minimize-to-tray menu item checked")
	}

	// Verify Push-to-Talk: V key works when no Entry is focused, ignored when Entry is focused
	ra.setPTTEnabled(true)
	if !ra.txDSP.IsMuted() {
		t.Fatalf("expected TX muted when PTT enabled and idle")
	}
	// Focus host Entry -> V key must NOT unmute
	w.Canvas().Focus(ra.txHostEntry)
	ra.handlePTTKeyDown(&fyne.KeyEvent{Name: fyne.KeyV})
	if !ra.txDSP.IsMuted() {
		t.Fatalf("expected V key ignored while text Entry is focused")
	}
	// Unfocus Entry -> V key MUST unmute on KeyDown and re-mute on KeyUp
	w.Canvas().Unfocus()
	ra.handlePTTKeyDown(&fyne.KeyEvent{Name: fyne.KeyV})
	if ra.txDSP.IsMuted() {
		t.Fatalf("expected V key to unmute TX when Entry is not focused")
	}
	ra.handlePTTKeyUp(&fyne.KeyEvent{Name: fyne.KeyV})
	if !ra.txDSP.IsMuted() {
		t.Fatalf("expected V key release to re-mute TX")
	}
	// Mouse hold button works too
	ra.setPTTMouseHeld(true)
	if ra.txDSP.IsMuted() {
		t.Fatalf("expected PTT mouse hold to unmute TX")
	}
	ra.setPTTMouseHeld(false)
	if !ra.txDSP.IsMuted() {
		t.Fatalf("expected PTT mouse release to re-mute TX")
	}
	ra.setPTTEnabled(false)
	if ra.txDSP.IsMuted() {
		t.Fatalf("expected TX unmuted when PTT disabled")
	}

	// Verify TX and RX start/stop buttons are mutually exclusive
	if ra.txStartStopBtn.Disabled() || ra.rxStartStopBtn.Disabled() {
		t.Fatalf("expected both TX and RX start buttons enabled initially")
	}
	if err := ra.sender.Start("127.0.0.1", 48199, config.CodecModePCM); err != nil {
		t.Fatalf("sender.Start failed: %v", err)
	}
	ra.syncStartStopButtons()
	if ra.txStartStopBtn.Disabled() || !ra.rxStartStopBtn.Disabled() {
		t.Fatalf("expected RX start button disabled while TX is running (txDisabled=%v rxDisabled=%v)",
			ra.txStartStopBtn.Disabled(), ra.rxStartStopBtn.Disabled())
	}
	ra.toggleRXStartStop()
	if ra.receiver.IsRunning() {
		t.Fatalf("expected toggleRXStartStop to be blocked while TX is running")
	}
	if len(w.Canvas().Overlays().List()) != 0 {
		t.Fatalf("expected blocked toggleRXStartStop to return before showing any error dialog")
	}
	ra.sender.Stop()
	ra.syncStartStopButtons()
	if ra.txStartStopBtn.Disabled() || ra.rxStartStopBtn.Disabled() {
		t.Fatalf("expected both buttons enabled after TX stops")
	}

	if err := ra.receiver.Start(0); err != nil {
		t.Fatalf("receiver.Start failed: %v", err)
	}
	ra.syncStartStopButtons()
	if !ra.txStartStopBtn.Disabled() || ra.rxStartStopBtn.Disabled() {
		t.Fatalf("expected TX start button disabled while RX is running (txDisabled=%v rxDisabled=%v)",
			ra.txStartStopBtn.Disabled(), ra.rxStartStopBtn.Disabled())
	}
	ra.toggleTXStartStop()
	if ra.sender.IsRunning() {
		t.Fatalf("expected toggleTXStartStop to be blocked while RX is running")
	}
	if len(w.Canvas().Overlays().List()) != 0 {
		t.Fatalf("expected blocked toggleTXStartStop to return before showing any error dialog")
	}
	ra.receiver.Stop()
	ra.syncStartStopButtons()
	if ra.txStartStopBtn.Disabled() || ra.rxStartStopBtn.Disabled() {
		t.Fatalf("expected both buttons enabled after RX stops")
	}

	// Render at default 700x680
	w.Resize(fyne.NewSize(700, 680))

	// Verify long TX/RX status strip, probe, warning, and diagnostic texts do not expand window MinSize
	baseMin := w.Content().MinSize()
	ra.txStripLabel.SetText("● TX 送信中 [MUTE] [PTT発話] [VC] [試聴] → desktop-vrc.tail1a2b.ts.net:48101 (Opus 96k / RTT 12.3ms)")
	ra.rxStripLabel.SetText("● RX 受信中 ← desktop-vrc.tail1a2b.ts.net:48101 (Opus 96k / Buf 30.0ms)")
	ra.txProbeLabel.SetText("疎通 OK: desktop-vrc.tail1a2b.ts.net (windows) — RTT 12.3 ms")
	ra.txStartStopBtn.SetText("■ 送信を停止")
	ra.txVADLabel.SetText("VAD: 発話中")
	ra.txPTTBtn.SetText("発話中... (離すとミュート)")
	ra.txLoopWarnLabel.SetText("⚠ 警告: 受信側の仮想マイクが送信入力に選択されています（ハウリングの原因になります）")
	ra.txLoopWarnLabel.Show()
	ra.rxDiagSource.SetText("送信元: desktop-vrc.tail1a2b.ts.net:48101")
	ra.rxDiagCodec.SetText("コーデック: Opus 96k (48kHz Mono)")
	ra.rxDiagBuf.SetText("実効バッファ: 150.0 ms")
	ra.rxDiagJitter.SetText("ジッター: 99.9 ms")
	ra.rxDiagLoss.SetText("パケットロス: 12.34 %")
	ra.rxDiagDrift.SetText("クロック補正: +1.23 %")

	checkMinSize := func(tabName string) {
		afterMin := w.Content().MinSize()
		if afterMin.Width > baseMin.Width || afterMin.Width > 700 {
			t.Fatalf("[%s] long status text expanded content MinSize width: before=%.1f after=%.1f", tabName, baseMin.Width, afterMin.Width)
		}
		if afterMin.Height > baseMin.Height || afterMin.Height > 680 {
			t.Fatalf("[%s] long status text expanded content MinSize height: before=%.1f after=%.1f", tabName, baseMin.Height, afterMin.Height)
		}
	}
	checkMinSize("TX")
	ra.tabs.SelectIndex(1)
	checkMinSize("RX")
	ra.tabs.SelectIndex(0)
	ra.txLoopWarnLabel.Hide()

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
		saveCapture("tx_menu_700x680.png")

		ra.tabs.SelectIndex(1)
		w.Resize(fyne.NewSize(700, 680))
		saveCapture("rx_menu_700x680.png")

		vcWin := a.NewWindow("ボイチェン詳細調整・ローカル試聴")
		vcWin.SetContent(ra.buildVoiceChangerContent())
		vcWin.Resize(fyne.NewSize(580, 680))
		img := vcWin.Canvas().Capture()
		if f, err := os.Create(filepath.Join(outDir, "vc_dialog_580x680.png")); err == nil {
			_ = png.Encode(f, img)
			_ = f.Close()
		}
		vcWin.Close()
	}
}
