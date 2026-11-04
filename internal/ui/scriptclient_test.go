package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/config"
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
alias files echo -s files $findfile(logs,*.txt,0) $findfile(logs,*.txt,1)
alias up echo -s up $uptime(mirc,3) $iif($uptime(system) > $uptime(mirc),yes,no) $iif($calc($ticks - $uptime(system)) < 1000,yes,no) $uptime(server)`,
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
	// $uptime: trout's just started; the computer's been up longer, and
	// $ticks is its uptime; the demo's network has no connection.
	a.edit(st).Buf = []rune("/up")
	a.submit(st)
	if got := last(); got != "up 0 yes yes 0" {
		t.Errorf("$uptime: %q", got)
	}
}

// TestDefenderNote: the first /load on Windows says what Defender does to
// some scripts, once; the setting remembers it, in config.toml. Elsewhere
// it's not said.
func TestDefenderNote(t *testing.T) {
	defer func(g string) { goos = g }(goos)
	for _, sys := range []string{"linux", "windows"} {
		goos = sys
		cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		a := New(Options{Config: cfg})
		a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
		dir := a.scriptsDir()
		if err := errors.Join(os.MkdirAll(dir, 0o755), os.WriteFile(filepath.Join(dir, "a.mrc"), []byte("alias a echo -a a"), 0o644),
			os.WriteFile(filepath.Join(dir, "b.mrc"), []byte("alias b echo -a b"), 0o644)); err != nil {
			t.Fatal(err)
		}
		st := a.home.Status()
		a.activate(st)
		load := func(f string) string {
			a.edit(st).Buf = []rune("/load -rs " + f)
			a.submit(st)
			return lastLine(st)
		}
		first, second := load("a.mrc"), load("b.mrc")
		noted := strings.Contains(first, "Microsoft Defender")
		if noted != (sys == "windows") || strings.Contains(second, "Defender") || a.settings().DefenderNoted != noted {
			t.Errorf("%s: first %q, second %q, setting %v", sys, first, second, a.settings().DefenderNoted)
		}
	}
}
