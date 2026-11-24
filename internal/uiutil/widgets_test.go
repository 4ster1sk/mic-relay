package uiutil

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

func TestHoldButtonReleasesOnDragOutsideWindow(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	presses := 0
	releases := 0
	btn := NewHoldButton("PTT", func() { presses++ }, func() { releases++ })
	w := test.NewWindow(btn)
	defer w.Close()
	w.Resize(fyne.NewSize(200, 60))

	// Case 1: Press, drag outside window, release (only DragEnd fires when pointer is outside window)
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	if presses != 1 || releases != 0 {
		t.Fatalf("after MouseDown: presses=%d releases=%d, want 1/0", presses, releases)
	}
	btn.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(-20, -20)}})
	btn.DragEnd()
	if presses != 1 || releases != 1 {
		t.Fatalf("after DragEnd outside window: presses=%d releases=%d, want 1/1", presses, releases)
	}

	// Case 2: Press, drag inside window, release (both MouseUp and DragEnd fire; OnRelease must run once)
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(50, 10)}})
	btn.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.DragEnd()
	if presses != 2 || releases != 2 {
		t.Fatalf("after MouseUp+DragEnd: presses=%d releases=%d, want 2/2", presses, releases)
	}

	// Case 3: Press near edge, leave button before drag threshold -> MouseOut releases
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.MouseOut()
	if presses != 3 || releases != 3 {
		t.Fatalf("after MouseOut without drag: presses=%d releases=%d, want 3/3", presses, releases)
	}

	// Case 4: Stray Dragged/DragEnd/MouseUp without MouseDown must not fire callbacks
	btn.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(10, 10)}})
	btn.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.DragEnd()
	if presses != 3 || releases != 3 {
		t.Fatalf("stray events should be ignored: presses=%d releases=%d, want 3/3", presses, releases)
	}

	// Case 5: Secondary mouse button must not press or release primary hold
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	btn.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	if presses != 3 || releases != 3 {
		t.Fatalf("secondary button alone should be ignored: presses=%d releases=%d, want 3/3", presses, releases)
	}
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	btn.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	if presses != 4 || releases != 3 {
		t.Fatalf("secondary button during primary hold must not release: presses=%d releases=%d, want 4/3", presses, releases)
	}
	btn.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	if presses != 4 || releases != 4 {
		t.Fatalf("primary release after secondary click failed: presses=%d releases=%d, want 4/4", presses, releases)
	}

	// Case 6: External Release() (e.g. OnExitedForeground) during drag releases once and ignores subsequent drag end
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(30, 10)}})
	btn.Release()
	btn.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(40, 10)}})
	btn.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.DragEnd()
	if presses != 5 || releases != 5 {
		t.Fatalf("external Release during drag should fire OnRelease once: presses=%d releases=%d, want 5/5", presses, releases)
	}

	// Case 7: Disable while held must release
	btn.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	btn.Disable()
	if presses != 6 || releases != 6 {
		t.Fatalf("Disable while held should release: presses=%d releases=%d, want 6/6", presses, releases)
	}
}

func TestSliderIgnoresDragWithoutMouseDown(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	sl := NewSlider(-24, 24)
	sl.Step = 0.5
	sl.Value = 0

	changedCount := 0
	endedCount := 0
	sl.OnChanged = func(_ float64) { changedCount++ }
	sl.OnChangeEnded = func(_ float64) { endedCount++ }

	w := test.NewWindow(sl)
	defer w.Close()
	w.Resize(fyne.NewSize(300, 40))

	// Passing cursor over slider while holding another button (Dragged without MouseDown)
	sl.MouseIn(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(250, 20)}})
	sl.DragEnd()
	if sl.Value != 0 || changedCount != 0 || endedCount != 0 {
		t.Fatalf("stray drag changed slider: val=%.1f changed=%d ended=%d, want 0/0/0", sl.Value, changedCount, endedCount)
	}

	// Normal drag originating on slider (MouseDown -> Dragged -> MouseUp -> DragEnd)
	sl.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(250, 20)}})
	sl.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.DragEnd()
	if sl.Value <= 0 || changedCount == 0 || endedCount != 1 {
		t.Fatalf("normal drag failed: val=%.1f changed=%d ended=%d", sl.Value, changedCount, endedCount)
	}

	// Drag that crosses onto an adjacent hoverable widget on the first move:
	// Fyne fires MouseOut before the first Dragged; drag must still succeed.
	prevVal := sl.Value
	sl.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.MouseOut()
	sl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(20, 20)}})
	sl.DragEnd()
	if sl.Value >= prevVal || endedCount != 2 {
		t.Fatalf("diagonal drag with MouseOut before first Dragged failed: val=%.1f prev=%.1f ended=%d", sl.Value, prevVal, endedCount)
	}

	// Stale press recovery: MouseDown -> MouseOut (<2px exit, released outside) -> later MouseIn + stray Dragged -> ignored
	savedVal := sl.Value
	sl.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.MouseOut()
	sl.MouseIn(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(280, 20)}})
	sl.DragEnd()
	if sl.Value != savedVal || endedCount != 2 {
		t.Fatalf("stray drag after stale press + MouseIn should be ignored: val=%.1f saved=%.1f ended=%d", sl.Value, savedVal, endedCount)
	}

	// Disable while pressed/dragging clears state so re-enabling does not accept a stray drag
	sl.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.Disable()
	sl.Enable()
	sl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(280, 20)}})
	sl.DragEnd()
	if sl.Value != savedVal || endedCount != 2 {
		t.Fatalf("stray drag after Disable/Enable should be ignored: val=%.1f saved=%.1f ended=%d", sl.Value, savedVal, endedCount)
	}

	// Secondary button must not arm or disarm slider drag
	sl.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	sl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(280, 20)}})
	sl.DragEnd()
	if sl.Value != savedVal || endedCount != 2 {
		t.Fatalf("secondary button drag should be ignored: val=%.1f saved=%.1f ended=%d", sl.Value, savedVal, endedCount)
	}
	sl.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.MouseDown(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	sl.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonSecondary})
	sl.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: fyne.NewPos(280, 20)}})
	sl.MouseUp(&desktop.MouseEvent{Button: desktop.MouseButtonPrimary})
	sl.DragEnd()
	if sl.Value <= savedVal || endedCount != 3 {
		t.Fatalf("secondary click during primary drag must not disarm slider: val=%.1f saved=%.1f ended=%d", sl.Value, savedVal, endedCount)
	}

	// Direct tap still updates value and fires OnChangeEnded
	afterDragVal := sl.Value
	sl.Tapped(&fyne.PointEvent{Position: fyne.NewPos(20, 20)})
	if sl.Value >= afterDragVal || endedCount != 4 {
		t.Fatalf("tap failed: val=%.1f prev=%.1f ended=%d", sl.Value, afterDragVal, endedCount)
	}
}

func TestBringWindowToFront(t *testing.T) {
	BringWindowToFront(nil)

	a := test.NewApp()
	defer a.Quit()
	w := test.NewWindow(NewHoldButton("test", nil, nil))
	defer w.Close()
	BringWindowToFront(w)
}

type fakeDesktopApp struct {
	desktop.App
	setMenuCalls int
	lastMenu     *fyne.Menu
}

func (f *fakeDesktopApp) SetSystemTrayMenu(m *fyne.Menu) {
	f.setMenuCalls++
	f.lastMenu = m
}

func TestTrayControllerDynamicMenu(t *testing.T) {
	desk := &fakeDesktopApp{}
	tc := newTrayController(desk, TrayCallbacks{})

	hasItem := func(target *fyne.MenuItem) bool {
		for _, it := range tc.menu.Items {
			if it == target {
				return true
			}
		}
		return false
	}
	assertNoConsecutiveSeparators := func(state string) {
		prevSep := false
		for i, it := range tc.menu.Items {
			if it.IsSeparator && prevSep {
				t.Fatalf("[%s] consecutive separators at index %d", state, i)
			}
			prevSep = it.IsSeparator
		}
	}

	// Idle: both TX and RX items are shown and enabled
	tc.UpdateState(false, false, true, false, false, false)
	if desk.setMenuCalls != 1 {
		t.Fatalf("expected 1 SetSystemTrayMenu call, got %d", desk.setMenuCalls)
	}
	// Duplicate state must not trigger redundant rebuild
	tc.UpdateState(false, false, true, false, false, false)
	if desk.setMenuCalls != 1 {
		t.Fatalf("expected duplicate UpdateState to be deduplicated, got %d calls", desk.setMenuCalls)
	}
	assertNoConsecutiveSeparators("idle")
	if tc.itemTXRun.Disabled || tc.itemRXRun.Disabled {
		t.Fatalf("expected both TX/RX tray items enabled when idle")
	}
	if !hasItem(tc.itemShow) || !hasItem(tc.itemTXRun) || !hasItem(tc.itemRXRun) || !hasItem(tc.itemQuit) {
		t.Fatalf("expected show, TX, RX, and quit items when idle")
	}

	// TX running: only TX items (plus show & quit) are shown; RX items hidden
	tc.UpdateState(true, true, false, true, false, false)
	if desk.setMenuCalls != 2 {
		t.Fatalf("expected 2 SetSystemTrayMenu calls, got %d", desk.setMenuCalls)
	}
	assertNoConsecutiveSeparators("tx-running")
	if tc.itemTXRun.Disabled || !tc.itemRXRun.Disabled {
		t.Fatalf("expected RX tray item disabled while TX running")
	}
	if !hasItem(tc.itemShow) || !hasItem(tc.itemTXMute) || !hasItem(tc.itemRNNoise) || !hasItem(tc.itemVC) || !hasItem(tc.itemTXRun) || !hasItem(tc.itemQuit) {
		t.Fatalf("expected TX items shown while TX running")
	}
	if hasItem(tc.itemRXMute) || hasItem(tc.itemRXRun) {
		t.Fatalf("expected RX items hidden while TX running")
	}
	if tc.itemTXMute.Label != "TX マイクミュート切替 (ミュート中)" || tc.itemRNNoise.Label != "TX ノイズ抑制 RNNoise (OFF)" || tc.itemVC.Label != "TX ボイチェン切替 (ON)" {
		t.Fatalf("unexpected TX labels: mute=%q rn=%q vc=%q", tc.itemTXMute.Label, tc.itemRNNoise.Label, tc.itemVC.Label)
	}

	// RX running: only RX items (plus show & quit) are shown; TX items hidden
	tc.UpdateState(false, false, true, false, true, true)
	if desk.setMenuCalls != 3 {
		t.Fatalf("expected 3 SetSystemTrayMenu calls, got %d", desk.setMenuCalls)
	}
	assertNoConsecutiveSeparators("rx-running")
	if !tc.itemTXRun.Disabled || tc.itemRXRun.Disabled {
		t.Fatalf("expected TX tray item disabled while RX running")
	}
	if !hasItem(tc.itemShow) || !hasItem(tc.itemRXMute) || !hasItem(tc.itemRXRun) || !hasItem(tc.itemQuit) {
		t.Fatalf("expected RX items shown while RX running")
	}
	if hasItem(tc.itemTXMute) || hasItem(tc.itemRNNoise) || hasItem(tc.itemVC) || hasItem(tc.itemTXRun) {
		t.Fatalf("expected TX items hidden while RX running")
	}
	if tc.itemRXMute.Label != "RX 出力ミュート切替 (ミュート中)" || tc.itemRXRun.Label != "RX 受信待機を停止" {
		t.Fatalf("unexpected RX labels: mute=%q run=%q", tc.itemRXMute.Label, tc.itemRXRun.Label)
	}

	// Back to idle: both TX and RX items restored
	tc.UpdateState(false, false, true, false, false, false)
	if desk.setMenuCalls != 4 {
		t.Fatalf("expected 4 SetSystemTrayMenu calls, got %d", desk.setMenuCalls)
	}
	assertNoConsecutiveSeparators("idle-restored")
	if !hasItem(tc.itemTXRun) || !hasItem(tc.itemRXRun) {
		t.Fatalf("expected both TX and RX items restored when idle")
	}
}
