//go:build !windows

package ui

import (
	"syscall"
	"testing"
	"time"
)

// TestHangup sends trout a SIGHUP, as closing its terminal does: it quits
// (so Run stops its soju) rather than dying.
func TestHangup(t *testing.T) {
	quit := make(chan struct{})
	stop := quitOnHangup(func() { close(quit) })
	defer stop()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-quit:
	case <-time.After(5 * time.Second):
		t.Fatal("a hangup didn't quit")
	}
}
