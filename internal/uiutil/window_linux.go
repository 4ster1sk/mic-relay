//go:build linux

package uiutil

import "fyne.io/fyne/v2"

// BringWindowToFront raises win to the foreground even when it is already
// visible behind other windows or minimized. Must be called on the Fyne main
// goroutine (e.g. inside fyne.Do).
//
// On Linux (especially Wayland where RequestFocus is a no-op, and X11 WMs that
// ignore focus requests on already-mapped windows), Hide followed by Show
// remaps the window on top of the window stack. (Trade-off: remapping may reset
// maximized/tiled state on some compositors.)
func BringWindowToFront(win fyne.Window) {
	if win == nil {
		return
	}
	win.Hide()
	win.Show()
	win.RequestFocus()
}
