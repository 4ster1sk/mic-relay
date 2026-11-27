//go:build !windows

package uiutil

import (
	"runtime/debug"
)

// ReleaseProcessMemory runs GC and returns unused Go heap pages to the OS.
func ReleaseProcessMemory() {
	debug.FreeOSMemory()
}
