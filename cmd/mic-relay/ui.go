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
	"fyne.io/fyne/v2/driver/desktop"
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
	localOutPlayer *audio.Player
	localOutBuf    *monitorRing
	localOutActive atomic.Bool
	virtualMic     *audio.VirtualMic
	sender         *netrelay.Sender
	receiver       *netrelay.Receiver
	discovery      *netrelay.DiscoveryResponder

	tray             *uiutil.TrayController
	stopTicker       chan struct{}
	stopOnce         sync.Once
	shuttingDown     atomic.Bool
	uiRefreshPending atomic.Bool

	// Top main menu bar
	mainMenu          *fyne.MainMenu
	vcMenu            *fyne.Menu
	vcPresetItems     map[string]*fyne.MenuItem
	activeVCPresetKey string
	vcDialogSync      func()
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
	txPTTCheck        *widget.Check
	txPTTBtn          *uiutil.HoldButton
	txLocalMicCheck   *widget.Check
	txLocalOutSelect  *widget.Select
	txLocalDeviceMap  map[string]string
	txPeerTable       *widget.Table

	// TX Mute / Push-to-Talk state
	txMuteMu      sync.Mutex
	txManualMuted bool
	pttEnabled    bool
	pttKeyHeld    bool
	pttMouseHeld  bool
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
	rxBufSlider       *uiutil.Slider
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
	ra.txLocalDeviceMap = make(map[string]string)
	ra.rxDeviceMap = make(map[string]string)

	// Top always-visible TX / RX status bar
	ra.txStripLabel = widget.NewLabel("TX: 停止中")
	ra.txStripLabel.Truncation = fyne.TextTruncateEllipsis
	ra.rxStripLabel = widget.NewLabel("RX: 停止中")
	ra.rxStripLabel.Truncation = fyne.TextTruncateEllipsis
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
	ra.setupPTTKeyHandler()
	ra.setPTTEnabled(ra.cfg.TX.PushToTalk)
	ra.syncStartStopButtons()
}

func (ra *RelayApp) syncStartStopButtons() {
	txRunning := ra.sender != nil && ra.sender.IsRunning()
	rxRunning := ra.receiver != nil && ra.receiver.IsRunning()

	if ra.txStartStopBtn != nil {
		if txRunning {
			ra.txStartStopBtn.SetText("■ 送信を停止")
			ra.txStartStopBtn.Importance = widget.DangerImportance
			ra.txStartStopBtn.Enable()
		} else if rxRunning {
			ra.txStartStopBtn.SetText("▶ 送信を開始 (受信中は開始不可)")
			ra.txStartStopBtn.Importance = widget.MediumImportance
			ra.txStartStopBtn.Disable()
		} else {
			ra.txStartStopBtn.SetText("▶ 送信を開始")
			ra.txStartStopBtn.Importance = widget.HighImportance
			ra.txStartStopBtn.Enable()
		}
		ra.txStartStopBtn.Refresh()
	}

	if ra.rxStartStopBtn != nil {
		if rxRunning {
			ra.rxStartStopBtn.SetText("■ 受信待機を停止")
			ra.rxStartStopBtn.Importance = widget.DangerImportance
			ra.rxStartStopBtn.Enable()
		} else if txRunning {
			ra.rxStartStopBtn.SetText("▶ 受信待機を開始 (送信中は開始不可)")
			ra.rxStartStopBtn.Importance = widget.MediumImportance
			ra.rxStartStopBtn.Disable()
		} else {
			ra.rxStartStopBtn.SetText("▶ 受信待機を開始")
			ra.rxStartStopBtn.Importance = widget.HighImportance
			ra.rxStartStopBtn.Enable()
		}
		ra.rxStartStopBtn.Refresh()
	}
}

func (ra *RelayApp) isTextInputFocused() bool {
	if ra.win == nil || ra.win.Canvas() == nil {
		return false
	}
	focused := ra.win.Canvas().Focused()
	if focused == nil {
		return false
	}
	_, isEntry := focused.(*widget.Entry)
	return isEntry
}

func (ra *RelayApp) handlePTTKeyDown(ev *fyne.KeyEvent) {
	if ev == nil || ev.Name != fyne.KeyV {
		return
	}
	if ra.isTextInputFocused() {
		return
	}
	ra.txMuteMu.Lock()
	enabled := ra.pttEnabled
	if enabled {
		ra.pttKeyHeld = true
	}
	ra.txMuteMu.Unlock()
	if enabled {
		ra.updateTXEffectiveMute()
	}
}

func (ra *RelayApp) handlePTTKeyUp(ev *fyne.KeyEvent) {
	if ev == nil || ev.Name != fyne.KeyV {
		return
	}
	ra.txMuteMu.Lock()
	wasHeld := ra.pttKeyHeld
	ra.pttKeyHeld = false
	ra.txMuteMu.Unlock()
	if wasHeld {
		ra.updateTXEffectiveMute()
	}
}

func (ra *RelayApp) setupPTTKeyHandler() {
	if ra.win != nil && ra.win.Canvas() != nil {
		if dc, ok := ra.win.Canvas().(desktop.Canvas); ok {
			dc.SetOnKeyDown(ra.handlePTTKeyDown)
			dc.SetOnKeyUp(ra.handlePTTKeyUp)
		}
	}
	if ra.fyneApp != nil && ra.fyneApp.Lifecycle() != nil {
		ra.fyneApp.Lifecycle().SetOnExitedForeground(func() {
			ra.releasePTTHold()
		})
	}
}

func (ra *RelayApp) releasePTTHold() {
	if ra.txPTTBtn != nil {
		ra.txPTTBtn.Release()
	}
	ra.txMuteMu.Lock()
	changed := ra.pttKeyHeld || ra.pttMouseHeld
	ra.pttKeyHeld = false
	ra.pttMouseHeld = false
	ra.txMuteMu.Unlock()
	if changed {
		ra.updateTXEffectiveMute()
	}
}

func (ra *RelayApp) setPTTEnabled(on bool) {
	ra.txMuteMu.Lock()
	ra.pttEnabled = on
	if !on {
		ra.pttKeyHeld = false
		ra.pttMouseHeld = false
	}
	ra.txMuteMu.Unlock()

	ra.cfgMu.Lock()
	ra.cfg.TX.PushToTalk = on
	ra.cfgMu.Unlock()

	ra.updateTXEffectiveMute()
}

func (ra *RelayApp) setPTTMouseHeld(held bool) {
	ra.txMuteMu.Lock()
	if !ra.pttEnabled {
		ra.pttMouseHeld = false
		ra.txMuteMu.Unlock()
		return
	}
	ra.pttMouseHeld = held
	ra.txMuteMu.Unlock()
	ra.updateTXEffectiveMute()
}

func (ra *RelayApp) updateTXEffectiveMute() {
	ra.txMuteMu.Lock()
	manualMuted := ra.txManualMuted
	pttOn := ra.pttEnabled
	talking := ra.pttKeyHeld || ra.pttMouseHeld
	ra.txMuteMu.Unlock()

	effectiveMute := manualMuted || (pttOn && !talking)
	ra.txDSP.SetMuted(effectiveMute)

	if !ra.shuttingDown.Load() {
		fyne.Do(func() {
			if ra.txPTTBtn != nil {
				if !pttOn {
					ra.txPTTBtn.Disable()
					ra.txPTTBtn.SetText("押している間話す (V)")
					ra.txPTTBtn.Importance = widget.MediumImportance
				} else if talking {
					ra.txPTTBtn.Enable()
					ra.txPTTBtn.SetText("発話中... (離すとミュート)")
					ra.txPTTBtn.Importance = widget.SuccessImportance
				} else {
					ra.txPTTBtn.Enable()
					ra.txPTTBtn.SetText("押している間話す (V)")
					ra.txPTTBtn.Importance = widget.MediumImportance
				}
				ra.txPTTBtn.Refresh()
			}
			ra.syncTrayState()
		})
	}
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
	ra.menuVCLocalMon = fyne.NewMenuItem("ローカル試聴 (スピーカーで確認)", func() {
		next := !ra.monitorEnabled.Load()
		if err := ra.setLocalMonitorEnabled(next); err != nil {
			ra.showError("ローカル試聴エラー", err)
		}
	})
	ra.vcMenu = fyne.NewMenu("ボイチェン", ra.buildVCMenuItems()...)

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

	// Place settingsMenu first because Fyne's desktop driver automatically appends
	// a Quit item to mainMenu.Items[0] unless its last item has IsQuit == true.
	ra.mainMenu = fyne.NewMainMenu(settingsMenu, qualityMenu, ra.vcMenu)
	ra.syncMenuState()
	ra.win.SetMainMenu(ra.mainMenu)
}

func (ra *RelayApp) buildVCMenuItems() []*fyne.MenuItem {
	activeKey := ra.resolveActiveVCPresetKey()
	ra.vcPresetItems = make(map[string]*fyne.MenuItem)

	items := []*fyne.MenuItem{
		ra.menuVCEnable,
		fyne.NewMenuItemSeparator(),
	}
	for _, def := range builtinVCPresets {
		d := def
		it := fyne.NewMenuItem(d.menuLabel, func() {
			ra.applyVoiceChangerPresetWithKey(d.key, d.preset, true)
		})
		it.Checked = d.key == activeKey
		ra.vcPresetItems[d.key] = it
		items = append(items, it)
	}

	customs := ra.customVoiceChangerPresets()
	if len(customs) > 0 {
		items = append(items, fyne.NewMenuItemSeparator())
		for _, p := range customs {
			key := customVCPresetKey(p.Name)
			preset := configPresetToDSP(p)
			it := fyne.NewMenuItem("カスタム: "+p.Name, func() {
				ra.applyVoiceChangerPresetWithKey(key, preset, true)
			})
			it.Checked = key == activeKey
			ra.vcPresetItems[key] = it
			items = append(items, it)
		}
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		ra.menuVCLocalMon,
		fyne.NewMenuItem("詳細調整・ローカル試聴...", func() {
			ra.showVoiceChangerDialog()
		}),
	)
	return items
}

func (ra *RelayApp) rebuildVCMenu() {
	if ra.vcMenu == nil || ra.mainMenu == nil {
		return
	}
	ra.vcMenu.Items = ra.buildVCMenuItems()
	ra.mainMenu.Refresh()
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
	activePresetKey := ra.resolveActiveVCPresetKey()
	for key, it := range ra.vcPresetItems {
		it.Checked = key == activePresetKey
	}
	ra.mainMenu.Refresh()
}

func (ra *RelayApp) setupTrayAndClose() {
	ra.tray = uiutil.SetupTray(ra.fyneApp, uiutil.TrayCallbacks{
		OnShow: func() {
			fyne.Do(func() {
				uiutil.BringWindowToFront(ra.win)
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
			// Release any held PTT key/mouse before hiding window
			ra.releasePTTHold()
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
	localOut := ra.selectedRawLocalOutputDevice()
	if audio.IsPotentialFeedbackLoop(txIn, rxOut, ra.receiver.IsRunning()) ||
		audio.IsPotentialFeedbackLoop(txIn, localOut, ra.localOutActive.Load()) {
		ra.txLoopWarnLabel.SetText("⚠ 警告: 仮想マイクが送信入力に選択されています（ハウリングの原因になります）")
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

func setLabelTextIfChanged(lbl *widget.Label, text string) {
	if lbl != nil && lbl.Text != text {
		lbl.SetText(text)
	}
}

func (ra *RelayApp) uiRefreshLoop() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ra.stopTicker:
			return
		case <-ticker.C:
			if !ra.uiRefreshPending.CompareAndSwap(false, true) {
				continue
			}
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
				defer ra.uiRefreshPending.Store(false)
				if ra.shuttingDown.Load() {
					return
				}

				selectedTab := 0
				if ra.tabs != nil {
					selectedTab = ra.tabs.SelectedIndex()
				}

				// TX VU + VAD (only refresh while TX tab is visible)
				if selectedTab == 0 {
					ra.txVUMeter.SetLevels(txRMS, txPeakHold)
					setLabelTextIfChanged(ra.txDBLabel, fmt.Sprintf("%5.1f dB", txRMS))
					if rnOn && ra.capturer.IsRunning() {
						if vad >= 0.5 {
							setLabelTextIfChanged(ra.txVADLabel, "VAD: 発話中")
						} else {
							setLabelTextIfChanged(ra.txVADLabel, "VAD: 待機")
						}
					} else if rnOn {
						setLabelTextIfChanged(ra.txVADLabel, "RNNoise: ON")
					} else {
						setLabelTextIfChanged(ra.txVADLabel, "RNNoise: OFF")
					}
				}

				// Top TX Status Strip
				vcTag := ""
				if ra.voiceChanger != nil && ra.voiceChanger.IsEnabled() {
					vcTag = " [VC]"
				}
				monTag := ""
				if ra.monitorEnabled.Load() {
					monTag = " [試聴]"
				}
				localTag := ""
				if ra.localOutActive.Load() {
					localTag = " [ローカル仮想マイク]"
				}
				ra.txMuteMu.Lock()
				pttOn := ra.pttEnabled
				pttTalk := ra.pttKeyHeld || ra.pttMouseHeld
				manualMuted := ra.txManualMuted
				ra.txMuteMu.Unlock()
				pttTag := ""
				if pttOn {
					if pttTalk {
						pttTag = " [PTT発話]"
					} else {
						pttTag = " [PTT待機]"
					}
				}
				if txStats.Connected {
					muteStr := ""
					if manualMuted {
						muteStr = " [MUTE]"
					}
					setLabelTextIfChanged(ra.txStripLabel, fmt.Sprintf("● TX 送信中%s%s%s%s%s → %s (%s %dk / RTT %.1fms)",
						muteStr, pttTag, vcTag, monTag, localTag, txStats.TargetAddr, txStats.CodecLabel, txStats.BitrateKbps, txStats.RTTMs))
				} else {
					setLabelTextIfChanged(ra.txStripLabel, "○ TX 停止中"+pttTag+vcTag+monTag+localTag)
				}

				// Top RX Status Strip
				if rxStats.Listening {
					if rxStats.Active {
						setLabelTextIfChanged(ra.rxStripLabel, fmt.Sprintf("● RX 受信中 ← %s (%s / Buf %.1fms)",
							rxStats.RemoteAddr, rxStats.CodecLabel, rxStats.Jitter.BufferedMs))
					} else {
						setLabelTextIfChanged(ra.rxStripLabel, fmt.Sprintf("◎ RX 待受中 (:%d)", rxStats.ListenPort))
					}
				} else {
					setLabelTextIfChanged(ra.rxStripLabel, "○ RX 停止中")
				}

				// RX VU & Diagnostics (only refresh while RX tab is visible)
				if selectedTab == 1 {
					ra.rxVUMeter.SetLevels(rxRMS, rxPeakHold)
					setLabelTextIfChanged(ra.rxDBLabel, fmt.Sprintf("%5.1f dB", rxRMS))

					src := "--"
					if rxStats.Active && rxStats.RemoteAddr != "" {
						src = rxStats.RemoteAddr
					}
					setLabelTextIfChanged(ra.rxDiagSource, "送信元: "+src)
					setLabelTextIfChanged(ra.rxDiagCodec, "コーデック: "+rxStats.CodecLabel)
					setLabelTextIfChanged(ra.rxDiagBuf, fmt.Sprintf("実効バッファ: %.1f ms", rxStats.Jitter.BufferedMs))
					setLabelTextIfChanged(ra.rxDiagJitter, fmt.Sprintf("ジッター: %.1f ms", rxStats.Jitter.JitterMs))
					setLabelTextIfChanged(ra.rxDiagLoss, fmt.Sprintf("パケットロス: %.2f %%", rxStats.Jitter.LossPercent))
					setLabelTextIfChanged(ra.rxDiagDrift, fmt.Sprintf("クロック補正: %+.2f %%", rxStats.Jitter.DriftPercent))
				}
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
		ra.stopLocalVirtualMicOutput()
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
