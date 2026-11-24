package uiutil

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

type TrayCallbacks struct {
	OnShow          func()
	OnToggleTXMute  func()
	OnToggleRNNoise func()
	OnToggleVC      func()
	OnToggleTXRun   func()
	OnToggleRXMute  func()
	OnToggleRXRun   func()
	OnQuit          func()
}

type TrayController struct {
	desk        desktop.App
	itemTXMute  *fyne.MenuItem
	itemRNNoise *fyne.MenuItem
	itemVC      *fyne.MenuItem
	itemTXRun   *fyne.MenuItem
	itemRXMute  *fyne.MenuItem
	itemRXRun   *fyne.MenuItem
	menu        *fyne.Menu
}

// SetupTray registers the system tray icon & menu if the platform supports desktop.App.
func SetupTray(a fyne.App, cb TrayCallbacks) *TrayController {
	desk, ok := a.(desktop.App)
	if !ok {
		return nil
	}

	tc := &TrayController{desk: desk}
	itemShow := fyne.NewMenuItem("ウィンドウを表示", func() {
		if cb.OnShow != nil {
			cb.OnShow()
		}
	})
	tc.itemTXMute = fyne.NewMenuItem("TX マイクミュート切替 (解除)", func() {
		if cb.OnToggleTXMute != nil {
			cb.OnToggleTXMute()
		}
	})
	tc.itemRNNoise = fyne.NewMenuItem("TX ノイズ抑制 RNNoise (ON)", func() {
		if cb.OnToggleRNNoise != nil {
			cb.OnToggleRNNoise()
		}
	})
	tc.itemVC = fyne.NewMenuItem("TX ボイチェン切替 (OFF)", func() {
		if cb.OnToggleVC != nil {
			cb.OnToggleVC()
		}
	})
	tc.itemTXRun = fyne.NewMenuItem("TX 送信を開始", func() {
		if cb.OnToggleTXRun != nil {
			cb.OnToggleTXRun()
		}
	})
	tc.itemRXMute = fyne.NewMenuItem("RX 出力ミュート切替 (解除)", func() {
		if cb.OnToggleRXMute != nil {
			cb.OnToggleRXMute()
		}
	})
	tc.itemRXRun = fyne.NewMenuItem("RX 受信待機を開始", func() {
		if cb.OnToggleRXRun != nil {
			cb.OnToggleRXRun()
		}
	})
	itemQuit := fyne.NewMenuItem("終了 (完全終了)", func() {
		if cb.OnQuit != nil {
			cb.OnQuit()
		}
	})
	itemQuit.IsQuit = true

	tc.menu = fyne.NewMenu("Mic Relay",
		itemShow,
		fyne.NewMenuItemSeparator(),
		tc.itemTXMute,
		tc.itemRNNoise,
		tc.itemVC,
		tc.itemTXRun,
		fyne.NewMenuItemSeparator(),
		tc.itemRXMute,
		tc.itemRXRun,
		fyne.NewMenuItemSeparator(),
		itemQuit,
	)

	desk.SetSystemTrayIcon(AppIcon())
	desk.SetSystemTrayMenu(tc.menu)
	return tc
}

func (tc *TrayController) UpdateState(txRunning, txMuted, rnnoiseOn, vcOn, rxRunning, rxMuted bool) {
	if tc == nil {
		return
	}
	if txMuted {
		tc.itemTXMute.Label = "TX マイクミュート切替 (ミュート中)"
	} else {
		tc.itemTXMute.Label = "TX マイクミュート切替 (解除)"
	}
	if rnnoiseOn {
		tc.itemRNNoise.Label = "TX ノイズ抑制 RNNoise (ON)"
	} else {
		tc.itemRNNoise.Label = "TX ノイズ抑制 RNNoise (OFF)"
	}
	if vcOn {
		tc.itemVC.Label = "TX ボイチェン切替 (ON)"
	} else {
		tc.itemVC.Label = "TX ボイチェン切替 (OFF)"
	}
	if txRunning {
		tc.itemTXRun.Label = "TX 送信を停止"
	} else {
		tc.itemTXRun.Label = "TX 送信を開始"
	}
	tc.itemTXRun.Disabled = !txRunning && rxRunning
	if rxMuted {
		tc.itemRXMute.Label = "RX 出力ミュート切替 (ミュート中)"
	} else {
		tc.itemRXMute.Label = "RX 出力ミュート切替 (解除)"
	}
	if rxRunning {
		tc.itemRXRun.Label = "RX 受信待機を停止"
	} else {
		tc.itemRXRun.Label = "RX 受信待機を開始"
	}
	tc.itemRXRun.Disabled = !rxRunning && txRunning
	tc.menu.Refresh()
}
