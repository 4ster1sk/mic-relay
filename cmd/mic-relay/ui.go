package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"mic-relay/internal/audio"
	"mic-relay/internal/config"
	"mic-relay/internal/dsp"
	"mic-relay/internal/jitter"
	"mic-relay/internal/netrelay"
	"mic-relay/internal/uiutil"
)

type peerRow struct {
	Kind        string
	DisplayName string
	Host        string
	Port        int
	RTTText     string
	IsPreset    bool
	PresetIndex int
}

type RelayApp struct {
	fyneApp    fyne.App
	win        fyne.Window
	cfgPath    string
	cfgMu      sync.Mutex
	cfg        config.Config
	instanceID string

	txDSP          *dsp.Processor
	rxDSP          *dsp.Processor
	denoiser       *dsp.Denoiser
	voiceChanger   *dsp.VoiceChanger
	jb             *jitter.Buffer
	capturer       *audio.Capturer
	player         *audio.Player
	monitorPlayer  *audio.Player
	monitorBuf     *monitorRing
	monitorEnabled atomic.Bool
	virtualMic     *audio.VirtualMic
	sender         *netrelay.Sender
	receiver       *netrelay.Receiver
	discovery      *netrelay.DiscoveryResponder

	tray         *uiutil.TrayController
	stopTicker   chan struct{}
	stopOnce     sync.Once
	shuttingDown atomic.Bool

	// Top main menu bar
	mainMenu          *fyne.MainMenu
	menuCodecPCM      *fyne.MenuItem
	menuCodecOpus96   *fyne.MenuItem
	menuCodecOpus48   *fyne.MenuItem
	menuVCEnable      *fyne.MenuItem
	menuVCLocalMon    *fyne.MenuItem
	menuMinToTray     *fyne.MenuItem

	// Top always-visible dual status strip
	txStripLabel *widget.Label
	rxStripLabel *widget.Label

	// TX tab widgets
	txMicSelect       *widget.Select
	txLoopWarnLabel   *widget.Label
	txVUMeter         *uiutil.VUMeter
	txDBLabel         *widget.Label
	txVADLabel        *widget.Label
	txGainLabel       *widget.Label
	txRNNoiseCheck    *widget.Check
	txGateCheck       *widget.Check
	txGateLabel       *widget.Label
	txPeerTable       *widget.Table
	txPeersMu         sync.RWMutex
	txPeerRows        []peerRow
	txSelectedRow     int
	txHostEntry       *widget.Entry
	txPortEntry       *widget.Entry
	txProbeLabel      *widget.Label
	txStartStopBtn    *widget.Button

	// RX tab widgets
	rxOutSelect       *widget.Select
	rxDeviceMap       map[string]string // display label -> raw device name
	rxPipeWireCheck   *widget.Check
	rxVUMeter         *uiutil.VUMeter
	rxDBLabel         *widget.Label
	rxVolLabel        *widget.Label
	rxMuteBtn         *widget.Button
	rxPortEntry       *widget.Entry
	rxNameEntry       *widget.Entry
	rxBufSlider       *widget.Slider
	rxBufLabel        *widget.Label
	rxDiagSource      *widget.Label
	rxDiagCodec       *widget.Label
	rxDiagBuf         *widget.Label
	rxDiagJitter      *widget.Label
	rxDiagLoss        *widget.Label
	rxDiagDrift       *widget.Label
	rxStartStopBtn    *widget.Button

	tabs *container.AppTabs
}

func (ra *RelayApp) buildUI() {
	ra.txSelectedRow = -1
	ra.rxDeviceMap = make(map[string]string)

	// Top always-visible TX / RX status bar
	ra.txStripLabel = widget.NewLabel("TX: 停止中")
	ra.rxStripLabel = widget.NewLabel("RX: 停止中")
	statusStrip := container.NewGridWithColumns(2,
		widget.NewCard("", "", ra.txStripLabel),
		widget.NewCard("", "", ra.rxStripLabel),
	)

	txTab := container.NewTabItem("送信 (TX) — マイクを送る", ra.buildTXTab())
	rxTab := container.NewTabItem("受信 (RX) — 仮想マイクに出す", ra.buildRXTab())

	ra.tabs = container.NewAppTabs(txTab, rxTab)
	if ra.cfg.ActiveTab == "rx" {
		ra.tabs.SelectIndex(1)
	}
	ra.tabs.OnSelected = func(item *container.TabItem) {
		ra.cfgMu.Lock()
		if item == rxTab {
			ra.cfg.ActiveTab = "rx"
		} else {
			ra.cfg.ActiveTab = "tx"
		}
		ra.cfgMu.Unlock()
		ra.saveConfig()
	}

	root := container.NewBorder(statusStrip, nil, nil, nil, ra.tabs)
	ra.win.SetContent(root)
	ra.buildMainMenu()
}

func (ra *RelayApp) buildMainMenu() {
	ra.menuCodecPCM = fyne.NewMenuItem(codecLabelPCM, func() {
		ra.selectCodecMode(config.CodecModePCM)
	})
	ra.menuCodecOpus96 = fyne.NewMenuItem(codecLabelOpus96, func() {
		ra.selectCodecMode(config.CodecModeOpus96)
	})
	ra.menuCodecOpus48 = fyne.NewMenuItem(codecLabelOpus48, func() {
		ra.selectCodecMode(config.CodecModeOpus48)
	})
	qualityMenu := fyne.NewMenu("音質",
		ra.menuCodecPCM,
		ra.menuCodecOpus96,
		ra.menuCodecOpus48,
	)

	ra.menuVCEnable = fyne.NewMenuItem("ボイスチェンジャー有効", func() {
		next := !(ra.voiceChanger != nil && ra.voiceChanger.IsEnabled())
		ra.setVoiceChangerEnabled(next)
	})
	itemVCFemaleNat := fyne.NewMenuItem("プリセット: 女声（ナチュラル）", func() {
		ra.applyVoiceChangerPreset(dsp.PresetFemaleNatural, true)
	})
	itemVCFemaleHigh := fyne.NewMenuItem("プリセット: 女声（高め・アニメ調）", func() {
		ra.applyVoiceChangerPreset(dsp.PresetFemaleHigh, true)
	})
	itemVCAndro := fyne.NewMenuItem("プリセット: 中性・少年声", func() {
		ra.applyVoiceChangerPreset(dsp.PresetAndrogynous, true)
	})
	ra.menuVCLocalMon = fyne.NewMenuItem("ローカル試聴 (スピーカーで確認)", func() {
		next := !ra.monitorEnabled.Load()
		if err := ra.setLocalMonitorEnabled(next); err != nil {
			ra.showError("ローカル試聴エラー", err)
		}
	})
	itemVCDetail := fyne.NewMenuItem("詳細調整・ローカル試聴...", func() {
		ra.showVoiceChangerDialog()
	})
	vcMenu := fyne.NewMenu("ボイチェン",
		ra.menuVCEnable,
		fyne.NewMenuItemSeparator(),
		itemVCFemaleNat,
		itemVCFemaleHigh,
		itemVCAndro,
		fyne.NewMenuItemSeparator(),
		ra.menuVCLocalMon,
		itemVCDetail,
	)

	ra.menuMinToTray = fyne.NewMenuItem("閉じるでトレイに最小化", func() {
		ra.cfgMu.Lock()
		next := !ra.cfg.MinimizeToTray
		ra.cfgMu.Unlock()
		ra.setMinimizeToTray(next)
	})
	itemQuit := fyne.NewMenuItem("終了 (完全終了)", func() {
		ra.shutdown()
		ra.fyneApp.Quit()
	})
	itemQuit.IsQuit = true
	settingsMenu := fyne.NewMenu("設定",
		ra.menuMinToTray,
		fyne.NewMenuItemSeparator(),
		itemQuit,
	)

	ra.mainMenu = fyne.NewMainMenu(qualityMenu, vcMenu, settingsMenu)
	ra.syncMenuState()
	ra.win.SetMainMenu(ra.mainMenu)
}

func (ra *RelayApp) selectCodecMode(mode config.CodecMode) {
	ra.cfgMu.Lock()
	ra.cfg.TX.CodecMode = mode
	ra.cfgMu.Unlock()
	if err := ra.sender.SetCodecMode(mode); err != nil {
		ra.showError("コーデック切替エラー", err)
	}
	ra.syncMenuState()
	ra.saveConfig()
}

func (ra *RelayApp) syncMenuState() {
	if ra.mainMenu == nil {
		return
	}
	ra.cfgMu.Lock()
	mode := ra.cfg.TX.CodecMode
	minToTray := ra.cfg.MinimizeToTray
	ra.cfgMu.Unlock()

	ra.menuCodecPCM.Checked = mode == config.CodecModePCM || mode == ""
	ra.menuCodecOpus96.Checked = mode == config.CodecModeOpus96
	ra.menuCodecOpus48.Checked = mode == config.CodecModeOpus48
	ra.menuVCEnable.Checked = ra.voiceChanger != nil && ra.voiceChanger.IsEnabled()
	ra.menuVCLocalMon.Checked = ra.monitorEnabled.Load()
	ra.menuMinToTray.Checked = minToTray
	ra.mainMenu.Refresh()
}

func (ra *RelayApp) setupTrayAndClose() {
	ra.tray = uiutil.SetupTray(ra.fyneApp, uiutil.TrayCallbacks{
		OnShow: func() {
			fyne.Do(func() {
				ra.win.Show()
				ra.win.RequestFocus()
			})
		},
		OnToggleTXMute: func() {
			fyne.Do(func() { ra.toggleTXMute() })
		},
		OnToggleRNNoise: func() {
			fyne.Do(func() {
				if ra.denoiser != nil {
					next := !ra.denoiser.IsEnabled()
					ra.txRNNoiseCheck.SetChecked(next)
				}
			})
		},
		OnToggleVC: func() {
			fyne.Do(func() {
				next := !(ra.voiceChanger != nil && ra.voiceChanger.IsEnabled())
				ra.setVoiceChangerEnabled(next)
			})
		},
		OnToggleTXRun: func() {
			fyne.Do(func() { ra.toggleTXStartStop() })
		},
		OnToggleRXMute: func() {
			fyne.Do(func() { ra.toggleRXMute() })
		},
		OnToggleRXRun: func() {
			fyne.Do(func() { ra.toggleRXStartStop() })
		},
		OnQuit: func() {
			ra.shutdown()
			fyne.Do(func() { ra.fyneApp.Quit() })
		},
	})

	ra.win.SetCloseIntercept(func() {
		ra.cfgMu.Lock()
		minToTray := ra.cfg.MinimizeToTray
		ra.cfgMu.Unlock()

		if minToTray && ra.tray != nil {
			ra.win.Hide()
			return
		}
		ra.shutdown()
		ra.fyneApp.Quit()
	})
	ra.syncTrayState()
}

func (ra *RelayApp) setMinimizeToTray(v bool) {
	ra.cfgMu.Lock()
	ra.cfg.MinimizeToTray = v
	ra.cfgMu.Unlock()
	ra.syncMenuState()
	ra.saveConfig()
}

func (ra *RelayApp) syncTrayState() {
	if ra.tray == nil {
		return
	}
	rnOn := ra.denoiser != nil && ra.denoiser.IsEnabled()
	vcOn := ra.voiceChanger != nil && ra.voiceChanger.IsEnabled()
	ra.tray.UpdateState(
		ra.sender.IsRunning(),
		ra.txDSP.IsMuted(),
		rnOn,
		vcOn,
		ra.receiver.IsRunning(),
		ra.rxDSP.IsMuted(),
	)
}

func (ra *RelayApp) updateLoopWarning() {
	if ra.txLoopWarnLabel == nil {
		return
	}
	txIn := ""
	if ra.txMicSelect != nil {
		txIn = ra.txMicSelect.Selected
	}
	rxOut := ra.selectedRawOutputDevice()
	if audio.IsPotentialFeedbackLoop(txIn, rxOut, ra.receiver.IsRunning()) {
		ra.txLoopWarnLabel.SetText("⚠ 警告: 受信側の仮想マイクが送信入力に選択されています（ハウリングの原因になります）")
		ra.txLoopWarnLabel.Show()
	} else {
		ra.txLoopWarnLabel.Hide()
	}
}

func (ra *RelayApp) saveConfig() {
	ra.cfgMu.Lock()
	cfgCopy := ra.cfg
	ra.cfgMu.Unlock()
	if err := config.Save(ra.cfgPath, cfgCopy); err != nil {
		log.Printf("config save error: %v", err)
	}
}

func (ra *RelayApp) showError(title string, err error) {
	dialog.ShowError(fmt.Errorf("%s: %w", title, err), ra.win)
}

func (ra *RelayApp) uiRefreshLoop() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ra.stopTicker:
			return
		case <-ticker.C:
			txRMS, _, txPeakHold := ra.txDSP.Levels()
			rxRMS, _, rxPeakHold := ra.rxDSP.Levels()
			txStats := ra.sender.Stats()
			rxStats := ra.receiver.Stats()
			vad := float32(0)
			rnOn := false
			if ra.denoiser != nil {
				rnOn = ra.denoiser.IsEnabled()
				vad = ra.denoiser.VAD()
			}

			fyne.Do(func() {
				// TX VU + VAD
				ra.txVUMeter.SetLevels(txRMS, txPeakHold)
				ra.txDBLabel.SetText(fmt.Sprintf("%5.1f dB", txRMS))
				if rnOn && ra.capturer.IsRunning() {
					if vad >= 0.5 {
						ra.txVADLabel.SetText("VAD: 発話中")
					} else {
						ra.txVADLabel.SetText("VAD: 待機")
					}
				} else if rnOn {
					ra.txVADLabel.SetText("RNNoise: ON")
				} else {
					ra.txVADLabel.SetText("RNNoise: OFF")
				}

				// RX VU
				ra.rxVUMeter.SetLevels(rxRMS, rxPeakHold)
				ra.rxDBLabel.SetText(fmt.Sprintf("%5.1f dB", rxRMS))

				// Top TX Status Strip
				vcTag := ""
				if ra.voiceChanger != nil && ra.voiceChanger.IsEnabled() {
					vcTag = " [VC]"
				}
				monTag := ""
				if ra.monitorEnabled.Load() {
					monTag = " [試聴]"
				}
				if txStats.Connected {
					muteStr := ""
					if ra.txDSP.IsMuted() {
						muteStr = " [MUTE]"
					}
					ra.txStripLabel.SetText(fmt.Sprintf("● TX 送信中%s%s%s → %s (%s %dk / RTT %.1fms)",
						muteStr, vcTag, monTag, txStats.TargetAddr, txStats.CodecLabel, txStats.BitrateKbps, txStats.RTTMs))
				} else {
					ra.txStripLabel.SetText("○ TX 停止中" + vcTag + monTag)
				}

				// Top RX Status Strip & Diagnostics
				if rxStats.Listening {
					if rxStats.Active {
						ra.rxStripLabel.SetText(fmt.Sprintf("● RX 受信中 ← %s (%s / Buf %.1fms)",
							rxStats.RemoteAddr, rxStats.CodecLabel, rxStats.Jitter.BufferedMs))
					} else {
						ra.rxStripLabel.SetText(fmt.Sprintf("◎ RX 待受中 (:%d)", rxStats.ListenPort))
					}
				} else {
					ra.rxStripLabel.SetText("○ RX 停止中")
				}

				src := "--"
				if rxStats.Active && rxStats.RemoteAddr != "" {
					src = rxStats.RemoteAddr
				}
				ra.rxDiagSource.SetText("送信元: " + src)
				ra.rxDiagCodec.SetText("コーデック: " + rxStats.CodecLabel)
				ra.rxDiagBuf.SetText(fmt.Sprintf("実効バッファ: %.1f ms", rxStats.Jitter.BufferedMs))
				ra.rxDiagJitter.SetText(fmt.Sprintf("ジッター: %.1f ms", rxStats.Jitter.JitterMs))
				ra.rxDiagLoss.SetText(fmt.Sprintf("パケットロス: %.2f %%", rxStats.Jitter.LossPercent))
				ra.rxDiagDrift.SetText(fmt.Sprintf("クロック補正: %+.2f %%", rxStats.Jitter.DriftPercent))
			})
		}
	}
}

func (ra *RelayApp) shutdown() {
	ra.stopOnce.Do(func() {
		ra.shuttingDown.Store(true)
		close(ra.stopTicker)
		ra.monitorEnabled.Store(false)
		if ra.monitorPlayer != nil {
			ra.monitorPlayer.Stop()
		}
		ra.stopTX()
		ra.capturer.Stop()
		ra.stopRX()
		ra.saveConfig()
	})
}

func formatPeerKind(kind, osName string) string {
	o := strings.ToLower(osName)
	osShort := ""
	switch {
	case strings.Contains(o, "win"):
		osShort = "Win"
	case strings.Contains(o, "linux"):
		osShort = "Linux"
	case strings.Contains(o, "darwin") || strings.Contains(o, "mac"):
		osShort = "Mac"
	}
	switch kind {
	case "LAN":
		if osShort != "" {
			return "LAN / " + osShort
		}
		return "LAN"
	case "Tailscale":
		if osShort != "" {
			return "TS / " + osShort
		}
		return "Tailscale"
	default:
		return "Preset"
	}
}
