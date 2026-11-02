package ui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

const customScript = `alias friends {
  window -lk0 @friends
  aline -s @friends carol
  aline @friends alice
  aline 4 @friends bob
}
menu @friends {
  dclick:echo -s dclick $1 $line(@friends,$1)
  lbclick:echo -s picked $sline(@friends,1).ln
  Remove:dline @friends $sline(@friends,1).ln
}
alias chat window -e @chat /said
alias said echo -a you said: $1-
on *:INPUT:@chat:echo -s input $1-
on *:CLOSE:@:echo -s closed $target
on *:TEXT:*:#:{
  if (!$window(@hl)) window -n @hl
  aline -h 4 @hl < $+ $nick $+ > $1-
}
alias secret window -h @secret
`

// TestCustomWindows drives scripts' @windows in the client: a listbox
// clicked, double-clicked and keyed through, its popup without the mouse
// events, an editbox with a default command and on INPUT, a log a script
// fills while it's minimized, a hidden one, and closing (on CLOSE).
func TestCustomWindows(t *testing.T) {
	a, _ := fakeSoju(t)
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	ch := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true,
		Nicks: []state.Nick{{Prefix: "@", Name: "max"}}})
	a.activate(ch)
	typed := func(win *state.Window, text string) {
		t.Helper()
		a.activate(win)
		a.edit(win).Buf = []rune(text)
		a.submit(win)
	}
	status := func() string { return lastLine(n.Status()) }
	a.scriptMem = map[string]string{"custom.mrc": customScript}
	typed(ch, "/load -rs custom.mrc")
	if got := lastLine(ch); got != "* Loaded custom.mrc" {
		t.Fatalf("loading: %q", got)
	}

	// A listbox: it comes to the front, its button without the @ (-k0).
	typed(ch, "/friends")
	f := a.active
	if f.Kind != state.KCustom || f.Name != "@friends" || !f.Custom.Listbox || len(f.Lines) != 3 ||
		!f.Lines[0].Sel || f.Lines[2].Color != 5 {
		t.Fatalf("/friends: active %q, %+v", f.Name, f.Lines)
	}
	if view := a.View(); !strings.Contains(view, " friends ") || strings.Contains(view, "@friends") {
		t.Error("the switchbar shows the @ (-k0 hides it)")
	}
	line := func(i int) Region {
		for _, r := range a.regions {
			if r.Kind == HitCustom && r.I == i && r.Win == f {
				return r
			}
		}
		t.Fatalf("no region for line %d", i+1)
		return Region{}
	}
	click := func(r Region, b tea.MouseButton) {
		a.Update(tea.MouseMsg{X: r.X + 2, Y: r.Y, Action: tea.MouseActionPress, Button: b})
		a.Update(tea.MouseMsg{X: r.X + 2, Y: r.Y, Action: tea.MouseActionRelease, Button: b})
	}
	click(line(1), tea.MouseButtonLeft)
	if got := status(); got != "picked 2" || f.Lines[0].Sel || !f.Lines[1].Sel {
		t.Fatalf("click on alice: status %q", got)
	}
	click(line(1), tea.MouseButtonLeft)
	if got := status(); got != "dclick 2 alice" {
		t.Fatalf("double click: %q", got)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := status(); got != "picked 3" || !f.Lines[2].Sel {
		t.Fatalf("Down: %q", got)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := status(); got != "dclick 3 bob" {
		t.Fatalf("Enter: %q", got)
	}
	a.View()
	click(line(0), tea.MouseButtonRight)
	if len(a.menus) != 1 || len(a.menus[0].Items) != 1 || a.menus[0].Items[0].Label != "Remove" {
		t.Fatalf("popup: %+v", a.menus)
	}
	a.menus[0].Items[0].Action(a)
	if len(f.Lines) != 2 || f.Lines[0].Text != "alice" {
		t.Fatalf("Remove: %+v", f.Lines)
	}

	// An editbox, with a default command and on INPUT.
	typed(ch, "/chat")
	c := a.active
	if c.Name != "@chat" || !c.Custom.Editbox {
		t.Fatalf("/chat: active %q", c.Name)
	}
	typed(c, "hello there")
	if got := status(); got != "input hello there" {
		t.Errorf("on INPUT: %q", got)
	}
	if got := lastLine(c); got != "you said: hello there" {
		t.Errorf("the default command: %q", got)
	}

	// A log filled while minimized: not in front, its button colored (-h).
	a.activate(ch)
	m, _ = ircmsg.ParseLine(":bob!b@h PRIVMSG #go :hi all")
	a.Update(ircLineMsg{n, m})
	hl := a.customWin("@hl")
	if hl == nil || a.active != ch || len(hl.Lines) != 1 || hl.Lines[0].Text != "<bob> hi all" ||
		hl.Lines[0].Color != 5 || hl.Act != state.ActMsg {
		t.Fatalf("@hl: %+v", hl)
	}

	// Hidden: nowhere to be seen.
	typed(ch, "/secret")
	if s := a.customWin("@secret"); s == nil || a.active == s || slices.Contains(a.order(), s) {
		t.Error("-h: the window shows")
	}

	// The user closing one runs on CLOSE; a script closing one doesn't.
	a.closeWindow(f)
	if got := status(); got != "closed @friends" || a.customWin("@friends") != nil {
		t.Errorf("closing: %q", got)
	}
	typed(ch, "//window -c @chat")
	if got := status(); got == "closed @chat" || a.customWin("@chat") != nil {
		t.Errorf("/window -c: %q", got)
	}
}
