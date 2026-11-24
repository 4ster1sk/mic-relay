package uiutil

import (
	"sync"

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

type trayState struct {
	txRunning bool
	txMuted   bool
	rnnoiseOn bool
	vcOn      bool
	rxRunning bool
	rxMuted   bool
}

type TrayController struct {
	mu          sync.Mutex
	desk        desktop.App
	itemShow    *fyne.MenuItem
	itemTXMute  *fyne.MenuItem
	itemRNNoise *fyne.MenuItem
	itemVC      *fyne.MenuItem
	itemTXRun   *fyne.MenuItem
	itemRXMute  *fyne.MenuItem
	itemRXRun   *fyne.MenuItem
	itemQuit    *fyne.MenuItem
	menu        *fyne.Menu
	lastState   trayState
	hasState    bool
}

func newTrayController(desk desktop.App, cb TrayCallbacks) *TrayController {
	tc := &TrayController{desk: desk}
	tc.itemShow = fyne.NewMenuItem("ウィンドウを表示", func() {
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
	tc.itemQuit = fyne.NewMenuItem("終了 (完全終了)", func() {
		if cb.OnQuit != nil {
			cb.OnQuit()
		}
	})
	tc.itemQuit.IsQuit = true

	tc.menu = fyne.NewMenu("Mic Relay", tc.buildMenuItems(false, false)...)
	return tc
}

func (tc *TrayController) buildMenuItems(txRunning, rxRunning bool) []*fyne.MenuItem {
	items := []*fyne.MenuItem{
		tc.itemShow,
		fyne.NewMenuItemSeparator(),
	}
	switch {
	case txRunning && !rxRunning:
		items = append(items,
			tc.itemTXMute,
			tc.itemRNNoise,
			tc.itemVC,
			tc.itemTXRun,
		)
	case rxRunning && !txRunning:
		items = append(items,
			tc.itemRXMute,
			tc.itemRXRun,
		)
	default:
		items = append(items,
			tc.itemTXMute,
			tc.itemRNNoise,
			tc.itemVC,
			tc.itemTXRun,
			fyne.NewMenuItemSeparator(),
			tc.itemRXMute,
			tc.itemRXRun,
		)
	}
	items = append(items,
		fyne.NewMenuItemSeparator(),
		tc.itemQuit,
	)
	return items
}

// SetupTray registers the system tray icon & menu if the platform supports desktop.App.
func SetupTray(a fyne.App, cb TrayCallbacks) *TrayController {
	desk, ok := a.(desktop.App)
	if !ok {
		return nil
	}

	tc := newTrayController(desk, cb)
	desk.SetSystemTrayIcon(AppIcon())
	desk.SetSystemTrayMenu(tc.menu)
	return tc
}

func (tc *TrayController) UpdateState(txRunning, txMuted, rnnoiseOn, vcOn, rxRunning, rxMuted bool) {
	if tc == nil {
		return
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()

	next := trayState{
		txRunning: txRunning,
		txMuted:   txMuted,
		rnnoiseOn: rnnoiseOn,
		vcOn:      vcOn,
		rxRunning: rxRunning,
		rxMuted:   rxMuted,
	}
	if tc.hasState && tc.lastState == next {
		return
	}
	tc.lastState = next
	tc.hasState = true

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

	tc.menu.Items = tc.buildMenuItems(txRunning, rxRunning)
	if tc.desk != nil {
		tc.desk.SetSystemTrayMenu(tc.menu)
	}
}
