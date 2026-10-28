package local

import (
	"os"
	"os/exec"
)

func detach(*exec.Cmd) {}

// interrupt kills: Windows has no SIGTERM for a console-less child.
func interrupt(p *os.Process) { p.Kill() }
