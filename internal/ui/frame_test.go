package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

var sgrRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestFrames renders a few UI states and checks that every line fills the
// screen exactly. Run with -v to see the frames as plain text.
func TestFrames(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	frame := func(name string) {
		lines := strings.Split(a.View(), "\n")
		if len(lines) != a.h {
			t.Fatalf("%s: %d lines, want %d", name, len(lines), a.h)
		}
		var b strings.Builder
		for i, l := range lines {
			plain := sgrRe.ReplaceAllString(l, "")
			if w := runewidth.StringWidth(plain); w != a.w {
				t.Errorf("%s: line %d is %d cells wide, want %d", name, i, w, a.w)
			}
			b.WriteString(plain + "\n")
		}
		t.Logf("%s:\n%s", name, b.String())
	}
	click := func(btn tea.MouseButton, x, y int) {
		a.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: btn})
		a.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: btn})
	}
	findNick := func() Region {
		for _, r := range a.regions {
			if r.Kind == HitNick && r.S == "bob" {
				return r
			}
		}
		t.Fatal("no nicklist entry for bob")
		return Region{}
	}

	frame("channel")

	// A long left press opens the popup; releasing early doesn't.
	r := findNick()
	a.Update(tea.MouseMsg{X: r.X + 2, Y: r.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	a.Update(tea.MouseMsg{X: r.X + 2, Y: r.Y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	a.Update(holdMsg{a.holdSeq})
	if len(a.menus) != 0 {
		t.Fatal("a short click opened the popup")
	}
	a.lastHit = Region{} // don't let the next press count as a double-click
	a.Update(tea.MouseMsg{X: r.X + 2, Y: r.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	a.Update(holdMsg{a.holdSeq})
	if len(a.menus) != 1 {
		t.Fatal("a long press didn't open the popup")
	}
	a.Update(tea.MouseMsg{X: r.X + 2, Y: r.Y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	a.closeMenus()

	// Right-click bob, hover Control to open its submenu, then use Query.
	r = findNick()
	click(tea.MouseButtonRight, r.X+2, r.Y)
	m := a.menus[0]
	a.Update(tea.MouseMsg{X: m.X + 3, Y: m.Y + 5, Action: tea.MouseActionMotion})
	if len(a.menus) != 2 {
		t.Fatalf("hovering Control opened %d menus, want 2", len(a.menus))
	}
	frame("nick popup")
	a.Update(tea.MouseMsg{X: m.X + 3, Y: m.Y + 3, Action: tea.MouseActionMotion})
	click(tea.MouseButtonLeft, m.X+3, m.Y+3)
	if a.active.Kind != state.KQuery || a.active.Name != "bob" {
		t.Fatalf("Query opened %q, want a query with bob", a.active.Name)
	}
	frame("query")

	a.themeIdx = 1
	a.activate(a.nets[0].Wins[0])
	a.openMenuBar(5)
	frame("dark status + Window menu")

	a.themeIdx = 0
	a.openDialog(aboutBox())
	frame("about box")
	a.dlg.key(a, "o")
	if a.dlg != nil {
		t.Fatal("pressing O didn't close the message box")
	}

	a.openOptions(optDisplay)
	frame("options")
	a.dlg.key(a, "esc")
	a.openChannelsList()
	frame("channels list")
	// Dragging the thumb to the bottom scrolls to the end, leaving the
	// selection alone.
	for _, r := range a.regions {
		if r.Kind == HitDlg && r.S == "thumb" {
			a.Update(tea.MouseMsg{X: r.X, Y: r.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			a.Update(tea.MouseMsg{X: r.X, Y: r.Y + 20, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
			a.Update(tea.MouseMsg{X: r.X, Y: r.Y + 20, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
		}
	}
	if l := a.dlg.(*listBox); l.Top != len(l.Rows)-l.visible(a) {
		t.Fatalf("after dragging the thumb down, Top = %d, want %d", l.Top, len(l.Rows)-l.visible(a))
	}
	a.dlg.key(a, "enter") // joins the first channel, #libera
	if a.active.Name != "#libera" {
		t.Fatalf("Join opened %q, want #libera", a.active.Name)
	}

	// The line marker shows up between what you'd seen and what's new.
	goNuts := a.nets[0].Wins[1]
	a.activate(goNuts)
	a.cycleKind(state.KQuery)
	a.addLine(goNuts, state.RoleNormal, "<alice> new since you left")
	a.activate(goNuts)
	if _, marker := a.windowRows(goNuts, 80); marker < 0 {
		t.Fatal("no line marker after new lines arrived")
	}

	// Dragging across two rows selects text, timestamps and all.
	a.View()
	a.Update(tea.MouseMsg{X: a.msgX, Y: a.msgY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	a.Update(tea.MouseMsg{X: a.msgX + 4, Y: a.msgY + 1, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	frame("selection")
	if got := a.selectedText(); !strings.Contains(got, "\n") || !strings.HasPrefix(got, "[") {
		t.Fatalf("selected %q, want two lines starting with a timestamp", got)
	}
	a.drag, a.sel = dragNone, nil // don't run the clipboard command from a test

	// The bouncer wizard: Hetzner, first region, Install, run the fake
	// install to the end, Finish.
	a.openSetup()
	a.dlg.key(a, "down")
	frame("wizard: where")
	a.dlg.key(a, "enter")
	frame("wizard: region")
	a.dlg.key(a, "enter")
	s := a.dlg.(*setupWizard)
	for s.step == stepInstall {
		a.Update(wizardTickMsg{s.seq})
	}
	frame("wizard: done")
	a.dlg.key(a, "enter")
	if a.dlg != nil || len(a.bouncers) != 1 || a.bouncer != 1 {
		t.Fatal("Finish didn't add the bouncer and switch to it")
	}
	if !strings.Contains(a.winTitle(a.nets[0].Wins[0]), "via trout-bouncer") {
		t.Fatal("status window title doesn't name the bouncer")
	}

	// Manage it: Remove asks first, then switches back to None.
	a.openOptions(optBouncer)
	frame("options: bouncer")
	a.openBouncers()
	frame("bouncers")
	a.dlg.key(a, "delete")
	frame("remove bouncer")
	a.dlg.key(a, "y")
	if len(a.bouncers) != 0 || a.bouncer != 0 {
		t.Fatal("removing the bouncer in use didn't switch back to None")
	}
	if _, ok := a.dlg.(*bouncerBox); !ok {
		t.Fatal("confirming Remove didn't return to the Bouncers window")
	}
	a.dlg.key(a, "esc")

	// Ctrl+K opens the color picker; click red, skip the background, type.
	a.activate(a.nets[0].Wins[1])
	a.View()
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	frame("color picker")
	for _, r := range a.regions {
		if r.Kind == HitSwatch && r.I == 4 {
			click(tea.MouseButtonLeft, r.X+1, r.Y)
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	if got := string(a.active.Edit.Buf); got != "\x0304hi" {
		t.Fatalf("editbox holds %q, want \"\\x0304hi\"", got)
	}
}
