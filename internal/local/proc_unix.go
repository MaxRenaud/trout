//go:build !windows

package local

import (
	"os"
	"os/exec"
	"syscall"
)

// detach keeps soju out of the terminal's process group, so Ctrl+C and
// friends reach trout, which stops soju itself.
func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func interrupt(p *os.Process) { p.Signal(syscall.SIGTERM) }
