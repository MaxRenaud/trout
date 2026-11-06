package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// TestTooSmall: below 60x16 the note fills the whole screen, wrapped, and
// the last frame's menubar and editbox can't be clicked or typed into.
func TestTooSmall(t *testing.T) {
	a, n, _ := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	a.View()
	var menu Region
	for _, r := range a.regions {
		if r.Kind == HitMenuBar {
			menu = r
			break
		}
	}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	lines := plainLines(a.View())
	if len(lines) != 10 {
		t.Fatalf("%d lines, want 10", len(lines))
	}
	for i, l := range lines {
		if w := runewidth.StringWidth(l); w != 40 {
			t.Errorf("line %d is %d cells wide, want 40: %q", i, w, l)
		}
	}
	if !strings.Contains(lines[1], "60x16") {
		t.Errorf("the note isn't wrapped: %q", lines)
	}
	a.Update(tea.MouseMsg{X: menu.X, Y: menu.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	a.Update(tea.MouseMsg{X: menu.X, Y: menu.Y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if len(a.menus) != 0 {
		t.Error("a click opened a menu nobody can see")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	if got := string(a.edit(ch).Buf); got != "" {
		t.Errorf("typing reached the editbox: %q", got)
	}
}

// TestDialogCoversThumb: pressing a dialog's blank part over the message
// pane's scrollbar thumb and dragging doesn't scroll the channel behind.
func TestDialogCoversThumb(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	for i := range 300 {
		feed(fmt.Sprintf(":bob!b@h PRIVMSG #go :line %d", i))
	}
	a.View()
	var thumb Region
	for _, r := range a.regions {
		if r.Kind == HitMsgScroll && r.S == "thumb" {
			thumb = r
		}
	}
	if thumb.W == 0 {
		t.Fatal("no thumb")
	}
	// A message box big enough to cover the thumb, with nothing to click
	// where it is.
	var lines []string
	for range 12 {
		lines = append(lines, "A long line of text that makes this box wide and tall enough.")
	}
	a.openDialog(&msgBox{Title: "Cover", Lines: lines, Buttons: []string{"OK"}})
	a.View()
	x, y := thumb.X, thumb.Y
	if r, ok := a.hit(x, y); !ok || r.Kind != HitMsgScroll {
		t.Fatalf("the test needs the dialog's blank part over the thumb, got %+v", r)
	}
	a.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	a.Update(tea.MouseMsg{X: x, Y: 0, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	a.Update(tea.MouseMsg{X: x, Y: 0, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if ch.Scroll != 0 {
		t.Errorf("the channel behind the dialog scrolled to %d", ch.Scroll)
	}
	if a.dlg == nil {
		t.Error("the dialog closed")
	}
}
