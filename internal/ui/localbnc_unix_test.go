//go:build !windows

package ui

import (
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/local"
)

// TestBouncerVanishes freezes the real soju (SIGSTOP), the way a bouncer
// whose machine vanished looks: the connection stays open and takes what's
// sent, and nothing answers. A message sent then is reported as not sent,
// and the bouncer as gone, within the liveness timings.
func TestBouncerVanishes(t *testing.T) {
	if _, _, err := local.Find(); err != nil {
		t.Skip(err)
	}
	dir, err := os.MkdirTemp(shortTemp(), "trout-gone-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	s, err := local.Start(t.Context(), dir, "max", "pw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(s.PID(), syscall.SIGCONT); s.Stop() })
	a, pump := liveAppWith(t, Options{Bouncer: config.Bouncer{Address: "irc+insecure://" + s.Addr, User: "max"}, Password: "pw"},
		func(a *App) { a.liveness = [3]time.Duration{time.Hour, 300 * time.Millisecond, 300 * time.Millisecond} })
	pump("the control connection", func() bool { return a.control != nil })
	a.home.Status().Lines = nil
	syscall.Kill(s.PID(), syscall.SIGSTOP)
	a.ircCommand(a.home.Status(), "PRIVMSG BouncerServ :help") // something sent into the void
	pump("the bouncer declared gone", func() bool { return a.control == nil })
	if got := strings.Join(lineTexts(a.home.Status()), "\n"); !strings.Contains(got, "no answer, even to a ping") {
		t.Fatalf("status window:\n%s", got)
	}
}
