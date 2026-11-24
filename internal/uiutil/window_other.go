//go:build !windows && !linux

package uiutil

import "fyne.io/fyne/v2"

// BringWindowToFront raises win to the foreground. Must be called on the Fyne
// main goroutine (e.g. inside fyne.Do).
func BringWindowToFront(win fyne.Window) {
	if win == nil {
		return
	}
	win.Show()
	win.RequestFocus()
}
