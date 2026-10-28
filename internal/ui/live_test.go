//go:build integration

package ui

import (
	"cmp"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// TestAgainstSoju runs the real client logic, without a terminal, against
// the dev environment (dev/compose.yaml) or any soju given by TROUT_ADDR:
//
//	go test -tags integration ./internal/ui -run TestAgainstSoju -v
//
// It connects, waits for soju's networks and their channels, then sends a
// message and waits for it to come back.
func TestAgainstSoju(t *testing.T) {
	a := New(Options{
		Addr:     cmp.Or(os.Getenv("TROUT_ADDR"), "127.0.0.1:6667"),
		User:     cmp.Or(os.Getenv("TROUT_USER"), "trout"),
		Password: cmp.Or(os.Getenv("TROUT_PASSWORD"), "trout"),
	})
	msgs := make(chan tea.Msg, 256)
	a.send = func(m tea.Msg) { msgs <- m }
	run := func(cmd tea.Cmd) {
		if cmd != nil {
			go func() { msgs <- cmd() }()
		}
	}
	// pump plays Bubble Tea's role: run commands, feed results to Update.
	pump := func(what string, done func() bool) {
		t.Helper()
		deadline := time.After(20 * time.Second)
		for !done() {
			select {
			case m := <-msgs:
				switch m := m.(type) {
				case nil:
				case tea.BatchMsg:
					for _, c := range m {
						run(c)
					}
				default:
					_, cmd := a.Update(m)
					run(cmd)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s; soju window says:\n%s", what, dump(a.home.Status()))
			}
		}
	}

	run(a.Init())
	pump("soju's networks", func() bool { return len(a.nets) > 1 && len(a.conns) == len(a.nets)-1 })
	var ch *state.Window
	pump("a joined channel", func() bool {
		for _, n := range a.nets[1:] {
			for _, w := range n.Wins {
				if w.Kind == state.KChannel && w.Joined && len(w.Nicks) > 0 {
					ch = w
					return true
				}
			}
		}
		return false
	})
	t.Logf("%s on %s: %d nicks, topic %q", ch.Name, ch.Net.Name, len(ch.Nicks), ch.Topic)

	text := "hello from the integration test " + time.Now().Format("15:04:05.000")
	a.say(ch, text)
	pump("our message to come back", func() bool {
		for _, l := range ch.Lines {
			if l.Role == state.RoleOwn && strings.HasSuffix(l.Text, text) {
				return true
			}
		}
		return false
	})
}

func dump(w *state.Window) string {
	var b strings.Builder
	for _, l := range w.Lines {
		b.WriteString(stripCodes(l.Text) + "\n")
	}
	return b.String()
}
