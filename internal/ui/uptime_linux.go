//go:build linux || android

package ui

import (
	"time"

	"golang.org/x/sys/unix"
)

// systemUptime is how long the computer has been up, sleep included.
func systemUptime() (time.Duration, bool) {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_BOOTTIME, &ts) != nil {
		return 0, false
	}
	return time.Duration(ts.Nano()), true
}
