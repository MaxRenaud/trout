package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// liveAppWith is an App connected to a real soju, and pump, which plays
// Bubble Tea's role until done says so: it runs commands and feeds their
// results to Update. setup runs before it connects.
func liveAppWith(t *testing.T, opts Options, setup ...func(*App)) (*App, func(what string, done func() bool)) {
	a := New(opts)
	for _, f := range setup {
		f(a)
	}
	if os.Getenv("TROUT_DEBUG") != "" { // log raw IRC lines
		a.opts.Debug = func(dir, line string) { t.Log(dir, line) }
	}
	msgs := make(chan tea.Msg, 256)
	a.send = func(m tea.Msg) { msgs <- m }
	run := func(cmd tea.Cmd) {
		if cmd != nil {
			go func() { msgs <- cmd() }()
		}
	}
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
	return a, pump
}

func dump(w *state.Window) string {
	var b strings.Builder
	for _, l := range w.Lines {
		b.WriteString(stripCodes(l.Text) + "\n")
	}
	return b.String()
}
