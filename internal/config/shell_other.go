//go:build !windows

package config

import "os/exec"

// shell runs cmd with /bin/sh.
func shell(cmd string) *exec.Cmd { return exec.Command("/bin/sh", "-c", cmd) }
