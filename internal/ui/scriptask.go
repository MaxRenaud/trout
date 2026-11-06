package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/script"
)

// Input boxes in the middle of a script ($? in an event, a timer, a
// socket's or $urlget's alias), which mIRC asks and waits for. The run
// goes on a goroutine of its own while Update waits for it, so only one
// of them touches trout's state at a time: when the script asks, Update
// shows the box and goes on with everything else; the answer hands the
// run back. Meanwhile other script runs wait their turn, like the rest of
// a script waits in mIRC, and $? in a run that can't wait (^ events, on
// INPUT, a CTCP's) is an error. Those still run while another waits:
// trout needs their answer (did they halt?) before it shows the line or
// sends the reply, so they can't join the queue. They share the engine's
// $readn, $ferr and open files with the waiting run.

// scriptRun is a script run that may stop to ask.
type scriptRun struct {
	ask    chan script.Prompt
	answer chan string
	done   chan []script.Action
	box    modal // the input box it's waiting on, or nil
}

// scriptAsking runs f, the engine call of an event or a timer, so that
// it can ask.
func (a *App) scriptAsking(f func() []script.Action) tea.Cmd {
	if a.scriptWait != nil {
		a.scriptQueue = append(a.scriptQueue, f)
		return nil
	}
	sr := &scriptRun{ask: make(chan script.Prompt), answer: make(chan string), done: make(chan []script.Action, 1)}
	a.scripts.SetAsk(sr.asker)
	go func() { sr.done <- f() }()
	return a.scriptResume(sr)
}

// asker is the engine's Ask for this run: it blocks the run's goroutine
// until the answer comes.
func (sr *scriptRun) asker(p script.Prompt) string {
	sr.ask <- p
	return <-sr.answer
}

// scriptResume waits for the run to finish or ask.
func (a *App) scriptResume(sr *scriptRun) tea.Cmd {
	select {
	case actions := <-sr.done:
		a.scriptWait = nil
		a.scripts.SetAsk(nil)
		cmds := []tea.Cmd{a.runScript(actions)}
		for a.scriptWait == nil && len(a.scriptQueue) > 0 {
			f := a.scriptQueue[0]
			a.scriptQueue = a.scriptQueue[1:]
			cmds = append(cmds, a.scriptAsking(f))
		}
		return tea.Batch(cmds...)
	case p := <-sr.ask:
		a.scriptWait = sr
		a.scripts.SetAsk(nil) // what runs while it waits can't ask
		answered := false
		reply := func(a *App, v string) tea.Cmd {
			if answered {
				return nil
			}
			answered = true
			sr.box = nil
			a.scripts.SetAsk(sr.asker)
			sr.answer <- v
			return a.scriptResume(sr)
		}
		// Cancel answers "": $$? stops the script.
		f := promptBox(p, func(a *App, v string, _ bool) tea.Cmd { return reply(a, v) })
		sr.box = f
		a.whenFree(func(a *App) tea.Cmd { a.openDialog(f); return nil })
		return nil
	}
}

// keepAsking reopens the input box a waiting script asked, if something
// else took its place and has closed: the script can't go on without it.
func (a *App) keepAsking() {
	if sr := a.scriptWait; sr != nil && sr.box != nil && a.dlg == nil && len(a.menus) == 0 && len(a.waiting) == 0 {
		a.openDialog(sr.box)
	}
}
