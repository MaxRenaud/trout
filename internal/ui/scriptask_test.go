package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

// TestScriptAsks: $? in an event shows an input box with the question
// evaluated and waits there, the rest of the handler going on with the
// answer; another event that comes meanwhile waits its turn; a dialog
// that takes the box's place gives it back; and Cancel on $$? stops the
// handler.
func TestScriptAsks(t *testing.T) {
	a, sent := fakeSoju(t)
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	ch := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true,
		Nicks: []state.Nick{{Name: "max"}, {Name: "bob"}, {Name: "ann"}}})
	a.activate(ch)
	a.scriptMem = map[string]string{"ask.mrc": `on *:TEXT:ask*:#:{
  var %a = $$?="Reply to $nick $+ ?"
  msg $chan $nick $+ : %a
}
on *:TEXT:sure*:#:msg $chan $?!="Sure, $nick $+ ?" $?*="Password?"`}
	a.edit(ch).Buf = []rune("/load -rs ask.mrc")
	a.submit(ch)
	feed := func(line string) {
		t.Helper()
		m, _ := ircmsg.ParseLine(line)
		a.Update(ircLineMsg{n, m})
	}
	box := func(want string) *formBox {
		t.Helper()
		f, ok := a.dlg.(*formBox)
		if !ok || f.Title != "Input Request" || len(f.Lines) != 1 || f.Lines[0] != want {
			t.Fatalf("want an input box %q, have %#v", want, a.dlg)
		}
		return f
	}
	toServer := func() string {
		for got := range sent {
			if strings.HasPrefix(got, "PRIVMSG #go") {
				return got
			}
		}
		return ""
	}

	feed(":bob!b@h PRIVMSG #go :ask me")
	f := box("Reply to bob?")
	feed(":ann!a@h PRIVMSG #go :ask too") // waits for bob's
	box("Reply to bob?")

	// Something else takes the box's place; once it's gone, the box is back.
	a.openDialog(message("Something else"))
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	f = box("Reply to bob?")
	f.Fields[0].Edit.Buf = []rune("later")
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := toServer(); got != "PRIVMSG #go :bob: later" {
		t.Fatalf("after answering bob: %q", got)
	}

	// Then ann's; Cancel on $$? stops it, and nothing's sent.
	f = box("Reply to ann?")
	a.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if a.dlg != nil || a.scriptWait != nil || len(a.scriptQueue) != 0 {
		t.Fatalf("after Cancel: dialog %T, waiting %v", a.dlg, a.scriptWait != nil)
	}
	feed(":bob!b@h PRIVMSG #go :ask again")
	box("Reply to bob?").Fields[0].Edit.Buf = []rune("ok")
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := toServer(); got != "PRIVMSG #go :bob: ok" {
		t.Fatalf("Cancel sent something, or the next one didn't ask: %q", got)
	}

	// $?! is Yes or No, $?* a masked line.
	feed(":bob!b@h PRIVMSG #go :sure?")
	yn, ok := a.dlg.(*msgBox)
	if !ok || yn.Lines[0] != "Sure, bob?" || len(yn.Buttons) != 2 || yn.Buttons[0] != "Yes" {
		t.Fatalf("$?!: %#v", a.dlg)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pw := box("Password?")
	if !pw.Fields[0].Mask {
		t.Error("$?* isn't masked")
	}
	pw.Fields[0].Edit.Buf = []rune("hunter2")
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := toServer(); got != "PRIVMSG #go :$true hunter2" {
		t.Fatalf("$?! and $?*: %q", got)
	}
}
