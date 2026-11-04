//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package ui

import (
	"time"

	"golang.org/x/sys/unix"
)

// systemUptime is how long the computer has been up, from its boot time.
func systemUptime() (time.Duration, bool) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0, false
	}
	return time.Since(time.Unix(tv.Unix())), true
}
