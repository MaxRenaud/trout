//go:build unix

package main

import (
	"os"
	"syscall"
)

// chown gives p the owner and group st has.
func chown(p string, st os.FileInfo) error {
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || os.Geteuid() != 0 { // only root can give files away
		return nil
	}
	return os.Chown(p, int(s.Uid), int(s.Gid))
}
