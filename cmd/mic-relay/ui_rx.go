package main

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"mic-relay/internal/audio"
	"mic-relay/internal/protocol"
	"mic-relay/internal/uiutil"
)

func (ra *RelayApp) buildRXTab() fyne.CanvasObject {
	// ---- Section 1: Output Device (VB-Cable / PipeWire) ----
	ra.rxOutSelect = widget.NewSelect(nil, func(selected string) {
		raw := ra.selectedRawOutputDevice()
		ra.cfgMu.Lock()
		ra.cfg.RX.OutputDevice = raw
		ra.cfgMu.Unlock()
		ra.saveConfig()
		ra.updateLoopWarning()
	})
	ra.refreshOutputDevices()

	refreshOutBtn := widget.NewButton("更新", func() {
		ra.refreshOutputDevices()
	})

	ra.rxVUMeter = uiutil.NewVUMeter()
	ra.rxDBLabel = widget.NewLabel("-60.0 dB")

	ra.rxVolLabel = widget.NewLabel(fmt.Sprintf("%.0f%%", ra.cfg.RX.OutputVolumePercent))
	volSlider := uiutil.NewSlider(0, 200)
	volSlider.Step = 5
	volSlider.Value = ra.cfg.RX.OutputVolumePercent
	volSlider.OnChanged = func(v float64) {
		ra.rxDSP.SetVolumePercent(v)
		ra.rxVolLabel.SetText(fmt.Sprintf("%.0f%%", v))
		ra.cfgMu.Lock()
		ra.cfg.RX.OutputVolumePercent = v
		ra.cfgMu.Unlock()
	}
	volSlider.OnChangeEnded = func(_ float64) { ra.saveConfig() }

	ra.rxMuteBtn = widget.NewButton("出力ミュート", func() {
		ra.toggleRXMute()
	})

	outItems := []fyne.CanvasObject{
		container.NewBorder(nil, nil, widget.NewLabel("出力デバイス"), refreshOutBtn, ra.rxOutSelect),
	}
	if runtime.GOOS == "linux" {
		ra.rxPipeWireCheck = widget.NewCheck(
			fmt.Sprintf("PipeWire 仮想マイク「%s」を自動作成して出力", audio.PipeWireVirtualMicName),
			func(on bool) {
				ra.cfgMu.Lock()
				ra.cfg.RX.AutoCreatePipeWireMic = on
				ra.cfgMu.Unlock()
				ra.saveConfig()
			},
		)
		ra.rxPipeWireCheck.Checked = ra.cfg.RX.AutoCreatePipeWireMic
		if !audio.VirtualMicSupported() {
			ra.rxPipeWireCheck.SetText("PipeWire 仮想マイク自動作成 (pw-loopback 未検出)")
			ra.rxPipeWireCheck.Disable()
		}
		outItems = append(outItems, ra.rxPipeWireCheck)
	}
	outItems = append(outItems,
		container.NewBorder(nil, nil, widget.NewLabel("出力レベル"), ra.rxDBLabel, ra.rxVUMeter),
		container.NewBorder(nil, nil, widget.NewLabel("出力音量"), container.NewHBox(ra.rxVolLabel, ra.rxMuteBtn), volSlider),
	)

	outCard := widget.NewCard("1. 仮想マイク出力先", "", container.NewVBox(outItems...))

	// ---- Section 2: Listen & Buffer Settings ----
	ra.rxPortEntry = widget.NewEntry()
	ra.rxPortEntry.SetText(strconv.Itoa(ra.cfg.RX.ListenPort))

	ra.rxNameEntry = widget.NewEntry()
	ra.rxNameEntry.SetText(ra.cfg.RX.DisplayName)

	ra.rxBufLabel = widget.NewLabel(fmt.Sprintf("%d ms", ra.cfg.RX.TargetBufferMs))
	ra.rxBufSlider = uiutil.NewSlider(10, 150)
	ra.rxBufSlider.Step = 5
	ra.rxBufSlider.Value = float64(ra.cfg.RX.TargetBufferMs)
	ra.rxBufSlider.OnChanged = func(v float64) {
		ms := int(v)
		ra.jb.SetTargetMs(ms)
		ra.rxBufLabel.SetText(fmt.Sprintf("%d ms", ms))
		ra.cfgMu.Lock()
		ra.cfg.RX.TargetBufferMs = ms
		ra.cfgMu.Unlock()
	}
	ra.rxBufSlider.OnChangeEnded = func(_ float64) { ra.saveConfig() }

	btnLAN := widget.NewButton("LAN 30ms", func() {
		ra.rxBufSlider.SetValue(30)
		ra.saveConfig()
	})
	btnTS := widget.NewButton("Tailscale 50ms", func() {
		ra.rxBufSlider.SetValue(50)
		ra.saveConfig()
	})

	listenCard := widget.NewCard("2. 待受・バッファ設定", "", container.NewVBox(
		container.NewGridWithColumns(2,
			container.NewBorder(nil, nil, widget.NewLabel("待受ポート"), nil, ra.rxPortEntry),
			container.NewBorder(nil, nil, widget.NewLabel("LAN 公開名"), nil, ra.rxNameEntry),
		),
		container.NewBorder(
			nil, nil,
			widget.NewLabel("目標バッファ"),
			container.NewHBox(ra.rxBufLabel, btnLAN, btnTS),
			ra.rxBufSlider,
		),
	))

	// ---- Section 3: Real-time Network Diagnostics ----
	ra.rxDiagSource = widget.NewLabel("送信元: --")
	ra.rxDiagCodec = widget.NewLabel("コーデック: --")
	ra.rxDiagBuf = widget.NewLabel("実効バッファ: 0.0 ms")
	ra.rxDiagJitter = widget.NewLabel("ジッター: 0.0 ms")
	ra.rxDiagLoss = widget.NewLabel("パケットロス: 0.00 %")
	ra.rxDiagDrift = widget.NewLabel("クロック補正: +0.00 %")
	for _, lbl := range []*widget.Label{
		ra.rxDiagSource, ra.rxDiagCodec, ra.rxDiagBuf,
		ra.rxDiagJitter, ra.rxDiagLoss, ra.rxDiagDrift,
	} {
		lbl.Truncation = fyne.TextTruncateEllipsis
	}

	diagCard := widget.NewCard("3. リアルタイム診断", "", container.NewGridWithColumns(3,
		ra.rxDiagSource,
		ra.rxDiagCodec,
		ra.rxDiagBuf,
		ra.rxDiagJitter,
		ra.rxDiagLoss,
		ra.rxDiagDrift,
	))

	// ---- Primary Action ----
	ra.rxStartStopBtn = widget.NewButton("▶ 受信待機を開始", func() {
		ra.toggleRXStartStop()
	})
	ra.rxStartStopBtn.Importance = widget.HighImportance

	content := container.NewVBox(
		outCard,
		listenCard,
		diagCard,
	)
	return container.NewBorder(nil, container.NewPadded(ra.rxStartStopBtn), nil, nil, container.NewVScroll(content))
}

func (ra *RelayApp) selectedRawOutputDevice() string {
	if ra.rxOutSelect == nil {
		return ""
	}
	sel := ra.rxOutSelect.Selected
	if raw, ok := ra.rxDeviceMap[sel]; ok {
		return raw
	}
	if strings.HasPrefix(sel, "(") {
		return ""
	}
	return sel
}

func (ra *RelayApp) refreshOutputDevices() {
	devs, err := audio.ListPlaybackDevices()
	ra.rxDeviceMap = make(map[string]string)
	if err != nil {
		ra.rxOutSelect.Options = []string{"(デフォルト出力)"}
		ra.rxOutSelect.SetSelectedIndex(0)
		return
	}

	opts := make([]string, 0, len(devs))
	ra.cfgMu.Lock()
	saved := ra.cfg.RX.OutputDevice
	ra.cfgMu.Unlock()

	selectedLabel := ""
	for _, d := range devs {
		label := d.Name
		if d.IsVirtual {
			label = "[推奨] " + d.Name
		}
		ra.rxDeviceMap[label] = d.Name
		opts = append(opts, label)
		if saved != "" && d.Name == saved {
			selectedLabel = label
		}
	}
	if len(opts) == 0 {
		opts = []string{"(出力デバイスなし)"}
	}
	ra.rxOutSelect.Options = opts
	if selectedLabel != "" {
		ra.rxOutSelect.SetSelected(selectedLabel)
	} else {
		// Because ListPlaybackDevices sorts VB-Cable first, index 0 auto-selects VB-Cable if present!
		ra.rxOutSelect.SetSelectedIndex(0)
	}
	ra.rxOutSelect.Refresh()
}

func (ra *RelayApp) toggleRXMute() {
	next := !ra.rxDSP.IsMuted()
	ra.rxDSP.SetMuted(next)
	if next {
		ra.rxMuteBtn.SetText("🔇 ミュート中")
		ra.rxMuteBtn.Importance = widget.DangerImportance
	} else {
		ra.rxMuteBtn.SetText("出力ミュート")
		ra.rxMuteBtn.Importance = widget.MediumImportance
	}
	ra.rxMuteBtn.Refresh()
	ra.syncTrayState()
}

func (ra *RelayApp) toggleRXStartStop() {
	if ra.receiver.IsRunning() {
		ra.stopRX()
		return
	}
	if ra.sender.IsRunning() {
		return
	}

	port, err := strconv.Atoi(strings.TrimSpace(ra.rxPortEntry.Text))
	if err != nil || port <= 0 || port > 65535 {
		ra.showError("待受ポートエラー", fmt.Errorf("1〜65535 の有効なポート番号を入力してください"))
		return
	}
	dispName := strings.TrimSpace(ra.rxNameEntry.Text)
	if dispName == "" {
		dispName = "mic-relay"
	}

	ra.cfgMu.Lock()
	ra.cfg.RX.ListenPort = port
	ra.cfg.RX.DisplayName = dispName
	autoPW := runtime.GOOS == "linux" && ra.cfg.RX.AutoCreatePipeWireMic && audio.VirtualMicSupported()
	ra.cfgMu.Unlock()
	ra.saveConfig()

	// Stop local TX virtual mic output before RX claims the virtual mic / output device
	ra.stopLocalVirtualMicOutput()

	targetDevice := ra.selectedRawOutputDevice()
	if autoPW {
		if err := ra.virtualMic.Start(); err != nil {
			ra.showError("PipeWire 仮想マイク作成エラー", err)
			return
		}
		ra.refreshOutputDevices()
		targetDevice = audio.PipeWireVirtualSinkName
		for label, raw := range ra.rxDeviceMap {
			if raw == targetDevice {
				ra.rxOutSelect.SetSelected(label)
				break
			}
		}
	}

	ra.receiver.SetDisplayName(dispName)
	if err := ra.receiver.Start(port); err != nil {
		ra.virtualMic.Stop()
		ra.showError("受信待受エラー", err)
		return
	}

	ra.discovery.UpdateInfo(dispName, port)
	_ = ra.discovery.Start(protocol.DefaultDiscoveryPort)

	err = ra.player.Start(targetDevice, func(dst []float32) {
		ra.jb.Read(dst)
		ra.rxDSP.Process(dst)
	})
	if err != nil {
		ra.receiver.Stop()
		ra.discovery.Stop()
		ra.virtualMic.Stop()
		ra.showError("オーディオ出力開始エラー", err)
		return
	}

	ra.syncStartStopButtons()
	ra.updateLoopWarning()
	ra.syncTrayState()
}

func (ra *RelayApp) stopRX() {
	ra.player.Stop()
	ra.receiver.Stop()
	ra.discovery.Stop()
	ra.virtualMic.Stop()
	ra.rxDSP.ResetLevels()
	if !ra.shuttingDown.Load() {
		ra.cfgMu.Lock()
		localOn := ra.cfg.TX.LocalVirtualMicEnabled
		ra.cfgMu.Unlock()
		if localOn {
			if !ra.capturer.IsRunning() {
				_ = ra.startMicMonitor()
			}
			_ = ra.startLocalVirtualMicOutput()
		}
		fyne.Do(func() {
			ra.syncStartStopButtons()
			ra.updateLoopWarning()
			ra.syncTrayState()
		})
	}
}
