package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestScriptClientIdents: on ACTIVE when you change windows ($active and
// $lactive), on APPACTIVE when the terminal gains or loses focus, $color
// from the scheme and the Colors dialog, and $findfile in the scripts
// folder.
func TestScriptClientIdents(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{"c.mrc": `on *:ACTIVE:*:echo -s active $active last $lactive
on *:APPACTIVE:echo -s app $appactive
alias colors echo -s colors $color(normal) $color(action) $color(highlight).dd $color(1)
alias files echo -s files $findfile(logs,*.txt,0) $findfile(logs,*.txt,1)`,
		"logs/a.txt": "x", "logs/old/b.txt": "y"}
	st := a.nets[0].Status()
	a.activate(st)
	a.Update(nil)
	a.edit(st).Buf = []rune("/load -rs c.mrc")
	a.submit(st)
	last := func() string { return lastLine(st) }

	ch := a.nets[0].Wins[1]
	a.activate(ch)
	a.Update(nil)
	if got := last(); got != "active "+ch.Name+" last Status Window" {
		t.Errorf("on ACTIVE: %q", got)
	}
	a.activate(st)
	a.Update(nil)
	if got := last(); got != "active Status Window last "+ch.Name {
		t.Errorf("back to status: %q", got)
	}
	a.Update(tea.BlurMsg{})
	if got := last(); got != "app $false" {
		t.Errorf("on APPACTIVE: %q", got)
	}
	a.Update(tea.FocusMsg{})
	if got := last(); got != "app $true" {
		t.Errorf("focus: %q", got)
	}

	a.themeIdx = 0 // Classic: mIRC's colors, but red highlights
	a.colors = nil
	a.edit(st).Buf = []rune("/colors")
	a.submit(st)
	if got := last(); got != "colors 1 6 04 0" {
		t.Errorf("Classic's colors: %q", got)
	}
	a.colors = parseColors("action=12")
	a.edit(st).Buf = []rune("/colors")
	a.submit(st)
	if got := last(); !strings.HasPrefix(got, "colors 1 12 ") {
		t.Errorf("with action=12: %q", got)
	}
	a.edit(st).Buf = []rune("/files")
	a.submit(st)
	if got := last(); got != "files 2 logs/a.txt" {
		t.Errorf("$findfile: %q", got)
	}
}
