package main

import (
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

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

	// Verify main menu order (設定 first so Fyne does not inject a Quit item into 音質),
	// codec selection, voice changer presets, and minimize-to-tray toggle
	if ra.mainMenu == nil || len(ra.mainMenu.Items) != 3 {
		t.Fatalf("expected 3 top-level menus (設定, 音質, ボイチェン)")
	}
	if ra.mainMenu.Items[0].Label != "設定" || ra.mainMenu.Items[1].Label != "音質" || ra.mainMenu.Items[2].Label != "ボイチェン" {
		t.Fatalf("unexpected menu order: %q, %q, %q",
			ra.mainMenu.Items[0].Label, ra.mainMenu.Items[1].Label, ra.mainMenu.Items[2].Label)
	}
	settingsItems := ra.mainMenu.Items[0].Items
	if len(settingsItems) == 0 || !settingsItems[len(settingsItems)-1].IsQuit || settingsItems[len(settingsItems)-1].Action == nil {
		t.Fatalf("expected 設定 menu to end with IsQuit item with non-nil Action so Fyne does not inject Quit elsewhere")
	}
	for _, m := range ra.mainMenu.Items[1:] {
		for _, it := range m.Items {
			if it.IsQuit {
				t.Fatalf("menu %q must not contain a Quit item", m.Label)
			}
		}
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
	// Verify active built-in preset has checkmark in menu and others do not
	maxKey := "builtin:" + dsp.PresetMax.Name
	natKey := "builtin:" + dsp.PresetFemaleNatural.Name
	if !ra.vcPresetItems[maxKey].Checked || ra.vcPresetItems[natKey].Checked {
		t.Fatalf("expected only MAX preset checked in menu (max=%v nat=%v)",
			ra.vcPresetItems[maxKey].Checked, ra.vcPresetItems[natKey].Checked)
	}
	// Verify Voice Changer detail dialog builds cleanly, odd-percentage preset button preserves checkmark,
	// and menu-to-open-dialog synchronization updates dialog buttons.
	vcUI := ra.buildVoiceChangerContent()
	if vcUI == nil {
		t.Fatalf("expected non-nil voice changer dialog content")
	}
	findButton := func(root fyne.CanvasObject, match func(string) bool) *widget.Button {
		var found *widget.Button
		var walk func(obj fyne.CanvasObject)
		walk = func(obj fyne.CanvasObject) {
			if found != nil || obj == nil {
				return
			}
			switch v := obj.(type) {
			case *widget.Button:
				if match(v.Text) {
					found = v
				}
			case *fyne.Container:
				for _, child := range v.Objects {
					walk(child)
				}
			case *container.Scroll:
				walk(v.Content)
			}
		}
		walk(root)
		return found
	}
	// Tap 女声ナチュラル (Softness=0.55, DeEsser=0.35 — odd percentages!) inside dialog
	natBtn := findButton(vcUI, func(s string) bool { return s == "女声ナチュラル" || s == "✓ 女声ナチュラル" })
	if natBtn == nil {
		t.Fatalf("expected 女声ナチュラル button in VC dialog")
	}
	natBtn.OnTapped()
	if natBtn.Text != "✓ 女声ナチュラル" || !ra.vcPresetItems[natKey].Checked {
		t.Fatalf("tapping 女声ナチュラル must preserve checkmark in dialog and menu (btn=%q menu=%v)", natBtn.Text, ra.vcPresetItems[natKey].Checked)
	}
	// Selecting a preset from the top menu must synchronize the open dialog's button checkmark
	ra.applyVoiceChangerPreset(dsp.PresetFemaleHigh, true)
	highBtn := findButton(vcUI, func(s string) bool { return s == "✓ 女声高め・カワボ" })
	if highBtn == nil || natBtn.Text != "女声ナチュラル" {
		t.Fatalf("menu preset selection did not sync open VC dialog (natBtn=%q highBtn=%v)", natBtn.Text, highBtn)
	}
	// Verify vcParamsEqual ignores HighPassHz when HighPassEnabled is false
	hpOffA := dsp.PresetAndrogynous.VoiceChangerParams
	hpOffB := hpOffA
	hpOffB.HighPassHz = 200
	if !vcParamsEqual(hpOffA, hpOffB) {
		t.Fatalf("expected vcParamsEqual to ignore HighPassHz when HighPassEnabled is false")
	}
	// Duplicate params: saving a custom preset identical to a built-in checks the custom preset;
	// deleting it falls back to the built-in preset.
	ra.applyVoiceChangerPreset(dsp.PresetFemaleNatural, true)
	if _, err := ra.saveVoiceChangerPreset("コピー女声", dsp.PresetFemaleNatural.VoiceChangerParams); err != nil {
		t.Fatalf("save duplicate custom preset failed: %v", err)
	}
	dupKey := customVCPresetKey("コピー女声")
	if !ra.vcPresetItems[dupKey].Checked || ra.vcPresetItems[natKey].Checked {
		t.Fatalf("expected duplicate custom preset checked over built-in (dup=%v nat=%v)",
			ra.vcPresetItems[dupKey].Checked, ra.vcPresetItems[natKey].Checked)
	}
	ra.deleteVoiceChangerPreset("コピー女声")
	if !ra.vcPresetItems[natKey].Checked {
		t.Fatalf("expected built-in preset checked after deleting duplicate custom preset")
	}
	ra.vcDialogSync = nil

	// Verify custom Voice Changer preset save, overwrite, menu sync, persistence, and delete
	if _, err := ra.saveVoiceChangerPreset("   ", dsp.PresetFemaleNatural.VoiceChangerParams); err == nil {
		t.Fatalf("expected empty custom preset name to fail")
	}
	customParams := dsp.VoiceChangerParams{
		PitchSemitones:  9.0,
		FormantRatio:    1.21,
		ThroatFormant:   1.07,
		BrightFormant:   1.09,
		Breathiness:     0.25,
		Softness:        0.58,
		HighPassEnabled: true,
		HighPassHz:      135.0,
		DeEsser:         0.42,
	}
	overwritten, err := ra.saveVoiceChangerPreset("配信用ボイス", customParams)
	if err != nil || overwritten {
		t.Fatalf("saveVoiceChangerPreset new failed: overwritten=%v err=%v", overwritten, err)
	}
	customParams.PitchSemitones = 10.5
	overwritten, err = ra.saveVoiceChangerPreset("配信用ボイス", customParams)
	if err != nil || !overwritten {
		t.Fatalf("saveVoiceChangerPreset overwrite failed: overwritten=%v err=%v", overwritten, err)
	}
	savedPresets := ra.customVoiceChangerPresets()
	if len(savedPresets) != 1 || savedPresets[0].Name != "配信用ボイス" || savedPresets[0].PitchSemitones != 10.5 {
		t.Fatalf("unexpected custom presets after overwrite: %+v", savedPresets)
	}
	foundInMenu := false
	for _, it := range ra.vcMenu.Items {
		if it.Label == "カスタム: 配信用ボイス" {
			foundInMenu = true
			break
		}
	}
	if !foundInMenu {
		t.Fatalf("expected custom preset to appear in ボイチェン menu")
	}
	// Apply custom preset -> checkmark should move to custom preset and uncheck built-ins
	customKey := customVCPresetKey("配信用ボイス")
	ra.applyVoiceChangerPresetWithKey(customKey, configPresetToDSP(savedPresets[0]), true)
	if !ra.vcPresetItems[customKey].Checked || ra.vcPresetItems[maxKey].Checked {
		t.Fatalf("expected custom preset checked in menu (custom=%v max=%v)",
			ra.vcPresetItems[customKey].Checked, ra.vcPresetItems[maxKey].Checked)
	}
	if vcUI := ra.buildVoiceChangerContent(); vcUI == nil {
		t.Fatalf("expected non-nil voice changer dialog content with custom presets")
	}
	// Modifying params away from all presets clears all preset checkmarks
	ra.cfgMu.Lock()
	ra.cfg.TX.VoiceChanger.PitchSemitones = 4.0
	ra.cfgMu.Unlock()
	ra.syncMenuState()
	for k, it := range ra.vcPresetItems {
		if it.Checked {
			t.Fatalf("expected no preset checked after manual parameter edit, but %s was checked", k)
		}
	}
	if !ra.deleteVoiceChangerPreset("配信用ボイス") {
		t.Fatalf("expected deleteVoiceChangerPreset to return true")
	}
	if len(ra.customVoiceChangerPresets()) != 0 {
		t.Fatalf("expected custom presets empty after delete")
	}
	for _, it := range ra.vcMenu.Items {
		if it.Label == "カスタム: 配信用ボイス" {
			t.Fatalf("expected deleted custom preset removed from ボイチェン menu")
		}
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
