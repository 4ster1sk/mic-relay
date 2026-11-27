//go:build windows

package uiutil

import (
	"runtime/debug"
	"syscall"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentProcess        = kernel32.NewProc("GetCurrentProcess")
	procSetProcessWorkingSetSize = kernel32.NewProc("SetProcessWorkingSetSize")
)

// ReleaseProcessMemory runs GC, returns unused Go heap pages to the OS, and
// trims the Windows process working set so physical memory drops promptly when
// the GUI window is parked in the system tray.
func ReleaseProcessMemory() {
	debug.FreeOSMemory()

	if h, _, _ := procGetCurrentProcess.Call(); h != 0 {
		// Passing (SIZE_T)-1 for both min and max trims as many unused pages
		// as possible from the working set (documented Win32 behavior).
		trim := ^uintptr(0)
		_, _, _ = procSetProcessWorkingSetSize.Call(h, trim, trim)
	}
}
