package ui

import (
	"time"

	"golang.org/x/sys/windows"
)

var getTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

// systemUptime is how long the computer has been up: Windows' tick count,
// which mIRC's $ticks is.
func systemUptime() (time.Duration, bool) {
	if getTickCount64.Find() != nil {
		return 0, false
	}
	ms, _, _ := getTickCount64.Call()
	return time.Duration(ms) * time.Millisecond, true
}
