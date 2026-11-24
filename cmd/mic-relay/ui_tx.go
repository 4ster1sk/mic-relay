package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"mic-relay/internal/audio"
	"mic-relay/internal/config"
	"mic-relay/internal/netrelay"
	"mic-relay/internal/protocol"
	"mic-relay/internal/uiutil"
)

const (
	codecLabelPCM    = "非圧縮 PCM (LAN)"
	codecLabelOpus96 = "Opus 96k (高音質)"
	codecLabelOpus48 = "Opus 48k (Tailscale)"
)

type responsiveTableLayout struct {
	table     *widget.Table
	minHeight float32
}

func (l *responsiveTableLayout) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(420, l.minHeight)
}

func (l *responsiveTableLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) == 0 {
		return
	}
	const (
		col0 = float32(92)
		col3 = float32(68)
		pad  = float32(18)
	)
	rem := size.Width - col0 - col3 - pad
	if rem < 200 {
		rem = 200
	}
	col1 := rem * 0.48
	col2 := rem - col1
	l.table.SetColumnWidth(0, col0)
	l.table.SetColumnWidth(1, col1)
	l.table.SetColumnWidth(2, col2)
	l.table.SetColumnWidth(3, col3)
	objects[0].Move(fyne.NewPos(0, 0))
	objects[0].Resize(size)
}

func (ra *RelayApp) buildTXTab() fyne.CanvasObject {
	// ---- Section 1: Input Mic & Noise Suppression ----
	ra.txMicSelect = widget.NewSelect(nil, func(selected string) {
		ra.cfgMu.Lock()
		prev := ra.cfg.TX.InputDevice
		ra.cfg.TX.InputDevice = selected
		ra.cfgMu.Unlock()
		ra.saveConfig()
		ra.updateLoopWarning()
		if selected != "" && selected != prev && ra.capturer.IsRunning() {
			go func() {
				_ = ra.startMicMonitor()
			}()
		}
	})
	ra.refreshInputDevices()

	refreshMicBtn := widget.NewButton("更新", func() {
		ra.refreshInputDevices()
	})

	ra.txLoopWarnLabel = widget.NewLabel("")
	ra.txLoopWarnLabel.Wrapping = fyne.TextWrapWord
	ra.txLoopWarnLabel.Importance = widget.WarningImportance
	ra.txLoopWarnLabel.Hide()

	ra.txVUMeter = uiutil.NewVUMeter()
	ra.txDBLabel = widget.NewLabel("-60.0 dB")
	ra.txVADLabel = widget.NewLabel("RNNoise: ON")

	ra.txGainLabel = widget.NewLabel(fmt.Sprintf("%+.1f dB", ra.cfg.TX.GainDB))
	gainSlider := uiutil.NewSlider(-24, 24)
	gainSlider.Step = 0.5
	gainSlider.Value = ra.cfg.TX.GainDB
	gainSlider.OnChanged = func(v float64) {
		ra.txDSP.SetGainDB(v)
		ra.txGainLabel.SetText(fmt.Sprintf("%+.1f dB", v))
		ra.cfgMu.Lock()
		ra.cfg.TX.GainDB = v
		ra.cfgMu.Unlock()
	}
	gainSlider.OnChangeEnded = func(_ float64) { ra.saveConfig() }

	ra.txRNNoiseCheck = widget.NewCheck("ノイズ抑制 (RNNoise)", func(on bool) {
		if ra.denoiser != nil {
			ra.denoiser.SetEnabled(on)
		}
		ra.cfgMu.Lock()
		ra.cfg.TX.NoiseSuppression = on
		ra.cfgMu.Unlock()
		ra.saveConfig()
		ra.syncTrayState()
	})
	ra.txRNNoiseCheck.Checked = ra.cfg.TX.NoiseSuppression
	if ra.denoiser == nil {
		ra.txRNNoiseCheck.Disable()
	}

	ra.txGateLabel = widget.NewLabel(fmt.Sprintf("%.0f dB", ra.cfg.TX.GateThresholdDB))
	gateSlider := uiutil.NewSlider(-60, -10)
	gateSlider.Step = 1
	gateSlider.Value = ra.cfg.TX.GateThresholdDB
	gateSlider.OnChanged = func(v float64) {
		ra.txGateLabel.SetText(fmt.Sprintf("%.0f dB", v))
		ra.cfgMu.Lock()
		ra.cfg.TX.GateThresholdDB = v
		on := ra.cfg.TX.NoiseGateEnabled
		ra.cfgMu.Unlock()
		ra.txDSP.SetNoiseGate(on, v)
	}
	gateSlider.OnChangeEnded = func(_ float64) { ra.saveConfig() }

	ra.txGateCheck = widget.NewCheck("ノイズゲート", func(on bool) {
		ra.cfgMu.Lock()
		ra.cfg.TX.NoiseGateEnabled = on
		th := ra.cfg.TX.GateThresholdDB
		ra.cfgMu.Unlock()
		ra.txDSP.SetNoiseGate(on, th)
		ra.saveConfig()
	})
	ra.txGateCheck.Checked = ra.cfg.TX.NoiseGateEnabled

	ra.txPTTCheck = widget.NewCheck("プッシュトーク (V)", func(on bool) {
		ra.setPTTEnabled(on)
		ra.saveConfig()
	})
	ra.txPTTCheck.Checked = ra.cfg.TX.PushToTalk

	ra.txPTTBtn = uiutil.NewHoldButton(
		"押している間話す (V)",
		func() { ra.setPTTMouseHeld(true) },
		func() { ra.setPTTMouseHeld(false) },
	)

	micCard := widget.NewCard("1. 入力マイク・音声処理", "", container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("入力マイク"), refreshMicBtn, ra.txMicSelect),
		ra.txLoopWarnLabel,
		container.NewBorder(nil, nil, widget.NewLabel("入力レベル"), container.NewHBox(ra.txDBLabel, ra.txVADLabel), ra.txVUMeter),
		container.NewBorder(nil, nil, widget.NewLabel("入力ゲイン"), ra.txGainLabel, gainSlider),
		container.NewGridWithColumns(2,
			ra.txRNNoiseCheck,
			container.NewBorder(nil, nil, ra.txGateCheck, ra.txGateLabel, gateSlider),
		),
		container.NewBorder(nil, nil, ra.txPTTCheck, nil, ra.txPTTBtn),
	))

	// ---- Section 2: Destination Peer Aligned Table ----
	headers := []string{"種別", "ホスト名 / 名前", "アドレス : ポート", "RTT"}
	ra.txPeerTable = widget.NewTableWithHeaders(
		func() (int, int) {
			ra.txPeersMu.RLock()
			defer ra.txPeersMu.RUnlock()
			return len(ra.txPeerRows), 4
		},
		func() fyne.CanvasObject {
			lbl := widget.NewLabel("")
			lbl.Truncation = fyne.TextTruncateEllipsis
			return lbl
		},
		func(id widget.TableCellID, obj fyne.CanvasObject) {
			lbl := obj.(*widget.Label)
			ra.txPeersMu.RLock()
			defer ra.txPeersMu.RUnlock()
			if id.Row < 0 || id.Row >= len(ra.txPeerRows) {
				lbl.SetText("")
				return
			}
			r := ra.txPeerRows[id.Row]
			switch id.Col {
			case 0:
				lbl.SetText(r.Kind)
			case 1:
				lbl.SetText(r.DisplayName)
			case 2:
				lbl.SetText(fmt.Sprintf("%s:%d", r.Host, r.Port))
			case 3:
				lbl.SetText(r.RTTText)
			}
		},
	)
	ra.txPeerTable.ShowHeaderColumn = false
	ra.txPeerTable.ShowHeaderRow = true
	ra.txPeerTable.CreateHeader = func() fyne.CanvasObject {
		lbl := widget.NewLabel("")
		lbl.TextStyle = fyne.TextStyle{Bold: true}
		return lbl
	}
	ra.txPeerTable.UpdateHeader = func(id widget.TableCellID, obj fyne.CanvasObject) {
		lbl := obj.(*widget.Label)
		if id.Col >= 0 && id.Col < len(headers) {
			lbl.SetText(headers[id.Col])
		}
	}
	ra.txPeerTable.SetColumnWidth(0, 96)
	ra.txPeerTable.SetColumnWidth(1, 175)
	ra.txPeerTable.SetColumnWidth(2, 180)
	ra.txPeerTable.SetColumnWidth(3, 72)

	ra.txPeerTable.OnSelected = func(id widget.TableCellID) {
		ra.txPeersMu.RLock()
		var chosen *peerRow
		if id.Row >= 0 && id.Row < len(ra.txPeerRows) {
			r := ra.txPeerRows[id.Row]
			chosen = &r
		}
		ra.txPeersMu.RUnlock()
		if chosen == nil {
			return
		}
		ra.txSelectedRow = id.Row
		ra.txHostEntry.SetText(chosen.Host)
		ra.txPortEntry.SetText(strconv.Itoa(chosen.Port))
	}

	tableWrap := container.New(&responsiveTableLayout{
		table:     ra.txPeerTable,
		minHeight: 148,
	}, ra.txPeerTable)

	ra.txHostEntry = widget.NewEntry()
	ra.txHostEntry.SetPlaceHolder("IP または Tailscale MagicDNS 名")
	ra.txHostEntry.SetText(ra.cfg.TX.TargetHost)

	ra.txPortEntry = widget.NewEntry()
	ra.txPortEntry.SetPlaceHolder("48101")
	ra.txPortEntry.SetText(strconv.Itoa(ra.cfg.TX.TargetPort))

	ra.txProbeLabel = widget.NewLabel("")
	ra.txProbeLabel.Truncation = fyne.TextTruncateEllipsis

	rescanBtn := widget.NewButton("再探索", func() {
		go ra.refreshPeers()
	})
	probeBtn := widget.NewButton("疎通確認", func() {
		host := strings.TrimSpace(ra.txHostEntry.Text)
		portStr := strings.TrimSpace(ra.txPortEntry.Text)
		go ra.probeCurrentTarget(host, portStr)
	})
	savePresetBtn := widget.NewButton("プリセット保存", func() {
		ra.saveCurrentAsPreset()
	})
	delPresetBtn := widget.NewButton("削除", func() {
		ra.deleteSelectedPreset()
	})

	destCard := widget.NewCard("2. 送信先ピア (LAN 自動検出 / Tailscale / プリセット)", "", container.NewVBox(
		container.NewBorder(nil, nil, nil, container.NewHBox(rescanBtn, delPresetBtn), ra.txProbeLabel),
		tableWrap,
		container.NewBorder(
			nil, nil, nil,
			container.NewHBox(
				container.NewGridWrap(fyne.NewSize(72, 36), ra.txPortEntry),
				probeBtn,
				savePresetBtn,
			),
			ra.txHostEntry,
		),
	))

	// ---- Primary Action ----
	ra.txStartStopBtn = widget.NewButton("▶ 送信を開始", func() {
		ra.toggleTXStartStop()
	})
	ra.txStartStopBtn.Importance = widget.HighImportance

	content := container.NewVBox(
		micCard,
		destCard,
	)
	return container.NewBorder(nil, container.NewPadded(ra.txStartStopBtn), nil, nil, container.NewVScroll(content))
}

func (ra *RelayApp) refreshInputDevices() {
	devs, err := audio.ListCaptureDevices()
	if err != nil {
		ra.txMicSelect.Options = []string{"(デフォルト入力)"}
		ra.txMicSelect.SetSelectedIndex(0)
		return
	}
	opts := make([]string, 0, len(devs))
	selected := ""
	ra.cfgMu.Lock()
	saved := ra.cfg.TX.InputDevice
	ra.cfgMu.Unlock()

	for _, d := range devs {
		opts = append(opts, d.Name)
		if d.Name == saved {
			selected = d.Name
		}
		if selected == "" && d.IsDefault {
			selected = d.Name
		}
	}
	if len(opts) == 0 {
		opts = []string{"(入力デバイスなし)"}
	}
	ra.txMicSelect.Options = opts
	if selected != "" {
		ra.txMicSelect.SetSelected(selected)
	} else {
		ra.txMicSelect.SetSelectedIndex(0)
	}
	ra.txMicSelect.Refresh()
}

func (ra *RelayApp) refreshPeers() {
	fyne.Do(func() {
		if ra.txProbeLabel != nil {
			ra.txProbeLabel.SetText("探索中...")
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	lanPeers, _ := netrelay.DiscoverLAN(ctx, ra.instanceID, protocol.DefaultDiscoveryPort, 1200*time.Millisecond)
	tsPeers := netrelay.DiscoverTailscalePeers(ctx, protocol.DefaultAudioPort)

	rows := make([]peerRow, 0, len(lanPeers)+len(tsPeers)+4)
	for _, p := range lanPeers {
		rtt := "--"
		if _, ms, err := netrelay.ProbePeer(ctx, ra.instanceID, p.Host, p.Port, 500*time.Millisecond); err == nil {
			rtt = fmt.Sprintf("%.1f ms", ms)
		}
		rows = append(rows, peerRow{
			Kind:        formatPeerKind("LAN", p.OS),
			DisplayName: p.DisplayName,
			Host:        p.Host,
			Port:        p.Port,
			RTTText:     rtt,
		})
	}
	for _, p := range tsPeers {
		rows = append(rows, peerRow{
			Kind:        formatPeerKind("Tailscale", p.OS),
			DisplayName: p.DisplayName,
			Host:        p.Host,
			Port:        p.Port,
			RTTText:     "--",
		})
	}

	ra.cfgMu.Lock()
	presets := append([]config.PeerPreset(nil), ra.cfg.TX.Presets...)
	ra.cfgMu.Unlock()
	for i, pr := range presets {
		rows = append(rows, peerRow{
			Kind:        "Preset",
			DisplayName: pr.Name,
			Host:        pr.Host,
			Port:        pr.Port,
			RTTText:     "--",
			IsPreset:    true,
			PresetIndex: i,
		})
	}

	ra.txPeersMu.Lock()
	ra.txPeerRows = rows
	ra.txSelectedRow = -1
	ra.txPeersMu.Unlock()

	fyne.Do(func() {
		if ra.txPeerTable != nil {
			ra.txPeerTable.UnselectAll()
			ra.txPeerTable.Refresh()
		}
		if ra.txProbeLabel != nil {
			ra.txProbeLabel.SetText(fmt.Sprintf("検出: LAN %d / Tailscale %d / Preset %d", len(lanPeers), len(tsPeers), len(presets)))
		}
	})
}

func (ra *RelayApp) probeCurrentTarget(host, portStr string) {
	port, err := strconv.Atoi(portStr)
	if host == "" || err != nil || port <= 0 || port > 65535 {
		fyne.Do(func() { ra.txProbeLabel.SetText("有効なホストとポート (1〜65535) を入力してください") })
		return
	}
	fyne.Do(func() { ra.txProbeLabel.SetText("疎通確認中...") })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	pong, rttMs, err := netrelay.ProbePeer(ctx, ra.instanceID, host, port, 1500*time.Millisecond)
	if err != nil {
		fyne.Do(func() { ra.txProbeLabel.SetText("応答なし (タイムアウト/未起動)") })
		return
	}
	fyne.Do(func() {
		ra.txProbeLabel.SetText(fmt.Sprintf("疎通 OK: %s (%s) — RTT %.1f ms", pong.DisplayName, pong.OS, rttMs))
	})
}

func (ra *RelayApp) saveCurrentAsPreset() {
	host := strings.TrimSpace(ra.txHostEntry.Text)
	port, err := strconv.Atoi(strings.TrimSpace(ra.txPortEntry.Text))
	if host == "" || err != nil || port <= 0 || port > 65535 {
		ra.txProbeLabel.SetText("保存する有効なホストとポート (1〜65535) を入力してください")
		return
	}
	ra.cfgMu.Lock()
	for _, pr := range ra.cfg.TX.Presets {
		if pr.Host == host && pr.Port == port {
			ra.cfgMu.Unlock()
			ra.txProbeLabel.SetText("同じ送信先が既にプリセットに存在します")
			return
		}
	}
	ra.cfg.TX.Presets = append(ra.cfg.TX.Presets, config.PeerPreset{
		Name: host,
		Host: host,
		Port: port,
	})
	ra.cfgMu.Unlock()
	ra.saveConfig()
	go ra.refreshPeers()
}

func (ra *RelayApp) deleteSelectedPreset() {
	ra.txPeersMu.Lock()
	idx := ra.txSelectedRow
	presetIdx := -1
	if idx >= 0 && idx < len(ra.txPeerRows) && ra.txPeerRows[idx].IsPreset {
		presetIdx = ra.txPeerRows[idx].PresetIndex
	}
	ra.txSelectedRow = -1
	ra.txPeersMu.Unlock()
	if ra.txPeerTable != nil {
		ra.txPeerTable.UnselectAll()
	}
	if presetIdx < 0 {
		return
	}
	ra.cfgMu.Lock()
	if presetIdx < len(ra.cfg.TX.Presets) {
		ra.cfg.TX.Presets = append(ra.cfg.TX.Presets[:presetIdx], ra.cfg.TX.Presets[presetIdx+1:]...)
	}
	ra.cfgMu.Unlock()
	ra.saveConfig()
	go ra.refreshPeers()
}

func (ra *RelayApp) toggleTXMute() {
	ra.txMuteMu.Lock()
	ra.txManualMuted = !ra.txManualMuted
	ra.txMuteMu.Unlock()
	ra.updateTXEffectiveMute()
}

func (ra *RelayApp) selectedInputDeviceName() string {
	ra.cfgMu.Lock()
	devName := ra.cfg.TX.InputDevice
	ra.cfgMu.Unlock()
	if strings.HasPrefix(devName, "(") {
		return ""
	}
	return devName
}

func (ra *RelayApp) onCaptureFrame(frame []float32) {
	// 1. Optional RNNoise Noise Suppression (48kHz / 480 samples = 10ms)
	if ra.denoiser != nil {
		ra.denoiser.Process(frame)
	}
	// 2. Optional Pure-Go Voice Changer (Pitch + Formant + HPF)
	if ra.voiceChanger != nil {
		ra.voiceChanger.Process(frame)
	}
	// 3. DSP (Gain, Noise Gate, Smooth Mute, Soft Limiter, live VU meter)
	ra.txDSP.Process(frame)
	// 4. Optional local speaker/headphone preview
	if ra.monitorEnabled.Load() && ra.monitorBuf != nil {
		ra.monitorBuf.Write(frame)
	}
	// 5. Encode & UDP Transmit only while actively streaming
	if ra.sender.IsRunning() {
		_ = ra.sender.SendFrame(frame)
	}
}

func (ra *RelayApp) startMicMonitor() error {
	ra.capturer.Stop()
	devName := ra.selectedInputDeviceName()
	return ra.capturer.Start(devName, ra.onCaptureFrame)
}

func (ra *RelayApp) toggleTXStartStop() {
	if ra.sender.IsRunning() {
		ra.stopTX()
		return
	}
	host := strings.TrimSpace(ra.txHostEntry.Text)
	port, err := strconv.Atoi(strings.TrimSpace(ra.txPortEntry.Text))
	if host == "" || err != nil || port <= 0 || port > 65535 {
		ra.showError("送信先エラー", fmt.Errorf("有効な送信先ホストとポート (1〜65535) を指定してください"))
		return
	}

	ra.cfgMu.Lock()
	ra.cfg.TX.TargetHost = host
	ra.cfg.TX.TargetPort = port
	mode := ra.cfg.TX.CodecMode
	ra.cfgMu.Unlock()
	ra.saveConfig()

	if !ra.capturer.IsRunning() {
		if err := ra.startMicMonitor(); err != nil {
			ra.showError("マイクキャプチャ開始エラー", err)
			return
		}
	}

	if err := ra.sender.Start(host, port, mode); err != nil {
		ra.showError("送信開始エラー", err)
		return
	}

	ra.txStartStopBtn.SetText("■ 送信を停止")
	ra.txStartStopBtn.Importance = widget.DangerImportance
	ra.txStartStopBtn.Refresh()
	ra.updateLoopWarning()
	ra.syncTrayState()
}

func (ra *RelayApp) stopTX() {
	ra.sender.Stop()
	if !ra.capturer.IsRunning() {
		ra.txDSP.ResetLevels()
	}
	if ra.txStartStopBtn != nil && !ra.shuttingDown.Load() {
		fyne.Do(func() {
			ra.txStartStopBtn.SetText("▶ 送信を開始")
			ra.txStartStopBtn.Importance = widget.HighImportance
			ra.txStartStopBtn.Refresh()
			ra.updateLoopWarning()
			ra.syncTrayState()
		})
	}
}
