package config

import (
	"os/exec"
	"syscall"
)

// shell runs cmd with cmd.exe. Its command line is given as is: Go would
// quote cmd the way most programs read their arguments (\"), which
// cmd.exe doesn't understand, so a command with quotes in it
// ("C:\Program Files\...\keepassxc-cli.exe" show ...) would break. With
// /S, cmd.exe takes off the outer quotes and runs the rest unchanged.
func shell(cmd string) *exec.Cmd {
	c := exec.Command("cmd.exe")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + cmd + `"`}
	return c
}
