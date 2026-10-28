package ui

import (
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
	"github.com/MaxRenaud/trout/internal/termkeys"
)

// On a Mac, trout's Alt shortcuts need the terminal's Option key to send
// Alt; by default it types characters like œ. Like WeeChat or Emacs,
// trout asks for that terminal setting, but it checks first, and fixes
// it where it safely can: in iTerm2 with a copy of your profile used
// only while trout runs, in kitty and Ghostty with a line in their
// config. "Don't remind me again" stops the question (Options > Other
// brings it back).

// optionKeyStart is the startup check: switch iTerm2's profile if you
// said to, or ask once.
func (a *App) optionKeyStart() tea.Cmd {
	t := a.term
	switch {
	case t.Name == "":
		return nil
	case t.Fix == termkeys.FixITermProfile && a.optionKey == "fix":
		return a.useITermProfile()
	case t.OK || a.optionKey != "" || a.dlg != nil:
		return nil
	}
	a.openOptionKey()
	return nil
}

// useITermProfile switches this iTerm2 tab to trout's copy of its
// profile, and remembers to switch back when trout quits.
func (a *App) useITermProfile() tea.Cmd {
	t := a.term
	changed, err := termkeys.InstallITermProfile(a.termHome, t.Profile)
	if err != nil {
		a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't set up iTerm2's Option key: "+err.Error())
		return nil
	}
	a.itermRestore = termkeys.SetITermProfile(t.Profile)
	out, seq := a.bellOut, termkeys.SetITermProfile(termkeys.ITermProfile(t.Profile))
	write := func(time.Time) tea.Msg {
		if out != nil {
			out.Write([]byte(seq))
		}
		return nil
	}
	if changed { // iTerm2 takes a moment to notice a new dynamic profile
		return tea.Tick(1500*time.Millisecond, write)
	}
	return func() tea.Msg { return write(time.Time{}) }
}

// stopITermProfile switches the tab back to its own profile now.
func (a *App) stopITermProfile() {
	if a.itermRestore != "" && a.bellOut != nil {
		a.bellOut.Write([]byte(a.itermRestore))
	}
	a.itermRestore = ""
}

// openOptionKey explains, and offers what can be done for this terminal.
func (a *App) openOptionKey() {
	t := a.term
	types := "In " + t.Name + ", your Option key types characters (œ, ∫, ø) instead."
	d := message("trout's shortcuts use Alt: Alt+1..9, Alt+O, Alt+Enter and more.", "")
	d.Title = "Option Key"
	d.Buttons, d.Cancel = []string{"OK"}, 0
	switch {
	case t.Fix == termkeys.FixITermProfile && a.optionKey == "fix":
		d.Lines = append(d.Lines, "In this tab, trout uses a copy of your \""+t.Profile+"\" profile",
			"whose left Option key is Alt, and switches back when it quits.")
		d.Buttons, d.Cancel = []string{"Turn Off", "Close"}, 1
	case t.OK:
		d.Lines = append(d.Lines, "Your Option key sends Alt in "+t.Name+" already.")
	case t.Fix == termkeys.FixITermProfile:
		d.Lines = append(d.Lines, types, "",
			"trout can make the left Option key Alt in this tab while it runs,",
			"using a copy of your \""+t.Profile+"\" profile. Your iTerm2 settings",
			"stay as they are, and the right Option key still types œ.")
		d.Buttons, d.Cancel = []string{"Fix It", "Not Now"}, 1
		d.Check = "Don't remind me again"
	case t.Fix == termkeys.FixConfigLine:
		d.Lines = append(d.Lines, types, "",
			"trout can add this line to "+tildePath(t.Config)+":",
			"    "+t.Line,
			t.After)
		d.Buttons, d.Cancel = []string{"Add It", "Not Now"}, 1
		d.Check = "Don't remind me again"
	default:
		d.Lines = append(d.Lines, types, "", "To make the left Option key Alt:")
		for _, s := range t.Steps {
			d.Lines = append(d.Lines, "    "+s)
		}
		d.Lines = append(d.Lines, "", "Until then, Ctrl+J starts a new line in the editbox.")
		d.Check = "Don't remind me again"
	}
	d.OnClose = func(a *App, button int) tea.Cmd {
		switch d.Buttons[button] {
		case "Fix It":
			a.optionKey = "fix"
			return a.useITermProfile()
		case "Add It":
			if err := termkeys.AddLine(t); err != nil {
				a.addLine(a.home.Status(), state.RoleQuit, "* Couldn't change "+tildePath(t.Config)+": "+err.Error())
				return nil
			}
			a.addLine(a.home.Status(), state.RoleInfo, "* Added "+t.Line+" to "+tildePath(t.Config)+". "+t.After)
		case "Turn Off":
			a.optionKey = "never"
			a.stopITermProfile()
			return nil
		}
		if d.Checked {
			a.optionKey = "never"
		}
		return nil
	}
	a.openDialog(d)
}

// tildePath shortens a path in the home folder to ~/...
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(p, home); ok {
			return "~" + rest
		}
	}
	return p
}
