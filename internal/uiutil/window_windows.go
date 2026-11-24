//go:build windows

package uiutil

import (
	"syscall"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procIsIconic            = user32.NewProc("IsIconic")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procBringWindowToTop    = user32.NewProc("BringWindowToTop")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
)

const (
	swShow        = 5
	swRestore     = 9
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpShowWindow = 0x0040
	hwndTopmost   = ^uintptr(0)     // (HWND)-1
	hwndNoTopmost = ^uintptr(0) - 1 // (HWND)-2
)

// BringWindowToFront restores and raises win to the foreground even if it is
// already visible behind other windows or minimized. Must be called on the Fyne
// main goroutine (e.g. inside fyne.Do).
func BringWindowToFront(win fyne.Window) {
	if win == nil {
		return
	}

	nw, ok := win.(driver.NativeWindow)
	if !ok {
		win.Show()
		win.RequestFocus()
		return
	}

	var hwnd uintptr
	var visible bool
	nw.RunNative(func(ctx any) {
		if wc, ok := ctx.(driver.WindowsWindowContext); ok && wc.HWND != 0 {
			hwnd = wc.HWND
			v, _, _ := procIsWindowVisible.Call(hwnd)
			visible = v != 0
		}
	})

	// Only call win.Show() when the window is not yet created or hidden to tray;
	// calling doShowAgain() on an already-visible minimized/maximized window can
	// apply stale coordinates or shift a maximized frame.
	if hwnd == 0 || !visible {
		win.Show()
		nw.RunNative(func(ctx any) {
			if wc, ok := ctx.(driver.WindowsWindowContext); ok {
				hwnd = wc.HWND
			}
		})
	}

	if hwnd != 0 {
		if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
			_, _, _ = procShowWindow.Call(hwnd, swRestore)
		} else {
			_, _, _ = procShowWindow.Call(hwnd, swShow)
		}
		flags := uintptr(swpNoMove | swpNoSize | swpShowWindow)
		_, _, _ = procSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, flags)
		_, _, _ = procSetWindowPos.Call(hwnd, hwndNoTopmost, 0, 0, 0, 0, flags)
		_, _, _ = procBringWindowToTop.Call(hwnd)
		_, _, _ = procSetForegroundWindow.Call(hwnd)
	}
	win.RequestFocus()
}
