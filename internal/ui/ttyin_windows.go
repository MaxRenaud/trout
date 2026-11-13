package ui

import (
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

// inputOption leaves the input to Bubble Tea on Windows: it reads the
// console's input records there, mouse events included, so no escape
// sequence gets split. It does take Bubble Tea's signal handling, for
// watchBreak.
func inputOption(*breakKey) tea.ProgramOption { return tea.WithoutSignalHandler() }

// watchBreak hears Ctrl+Break, which the console sends trout as a signal
// (Go's os.Interrupt) whatever the console's mode, and makes it the break
// key, mIRC's: Bubble Tea would quit on it. Ctrl+Break with no script
// running does nothing, as in mIRC. SIGTERM, a console closing, still
// quits, as Bubble Tea had it. The function it returns stops listening.
func watchBreak(brk *breakKey, quit func()) (stop func()) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sig:
				if s != os.Interrupt {
					quit()
					return
				}
				brk.pressed.Store(true)
			case <-done:
				return
			}
		}
	}()
	return func() { signal.Stop(sig); close(done) }
}
