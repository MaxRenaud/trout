//go:build !linux && !android && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package ui

import "time"

// systemUptime isn't known here: $uptime(system) falls back to trout's.
func systemUptime() (time.Duration, bool) { return 0, false }
