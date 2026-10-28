//go:build !windows

package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// The system clipboard through its usual commands.

func systemCopy(text string) error {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("pbcopy")
	case os.Getenv("WAYLAND_DISPLAY") != "":
		cmd = exec.Command("wl-copy")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

func systemPaste() (string, error) {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("pbpaste")
	case os.Getenv("WAYLAND_DISPLAY") != "":
		cmd = exec.Command("wl-paste", "--no-newline")
	default:
		cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
	}
	out, err := cmd.Output()
	return string(out), err
}
