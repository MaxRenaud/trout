package ui

import (
	"os"
	"os/signal"
	"syscall"
)

// quitOnHangup has a SIGHUP (the terminal window closed) quit trout like
// SIGTERM does, rather than kill it, so Run still stops trout's own soju:
// soju is in its own process group (local.detach) and doesn't get the
// hangup itself. Windows sends closing consoles SIGTERM, which watchBreak
// handles. The function it returns stops listening.
func quitOnHangup(quit func()) (stop func()) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-hup:
			quit()
		case <-done:
		}
	}()
	return func() { signal.Stop(hup); close(done) }
}
