package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

func TestHighlightRules(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.memBook.Highlights = []config.HighlightRule{
		{Words: "gen*cs", MatchText: true, Color: 4, Beep: false},
		{Words: "carol", MatchNick: true, Color: -1, Beep: true, In: "#go"},
	}
	feed(":bob!b@h PRIVMSG #go :generics are here", ":carol!c@h PRIVMSG #go :hi all", ":bob!b@h PRIVMSG #go :plain")
	l := ch.Lines[len(ch.Lines)-3:]
	if l[0].Role != state.RoleHighlight || l[0].Color != 5 || !l[0].NoBeep {
		t.Fatalf("wildcard word rule: %+v", l[0])
	}
	if l[1].Role != state.RoleHighlight || l[1].Color != 0 || l[1].NoBeep {
		t.Fatalf("nick rule: %+v", l[1])
	}
	if l[2].Role == state.RoleHighlight {
		t.Fatal("a plain line highlighted")
	}
}

func TestNickColorRules(t *testing.T) {
	a, n, _ := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.memBook.NickColors = []config.NickColorRule{{Mask: "ali*", Color: 9}, {Mask: "*", Modes: "@", Color: 4}}
	if a.nickRuleColor(ch, "alice") != 9 || a.nickRuleColor(ch, "bob") != -1 {
		t.Fatal("mask rule")
	}
	ch.Nicks = append(ch.Nicks, state.Nick{Prefix: "@", Name: "op"})
	if a.nickRuleColor(ch, "op") != 4 {
		t.Fatal("mode rule")
	}
}

func TestIgnoreTypes(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.ircCommand(ch, "ignore -c spammer")
	before := len(ch.Lines)
	feed(":spammer!s@h PRIVMSG #go :buy", ":spammer!s@h PRIVMSG max :still here")
	if len(ch.Lines) != before || n.Find(state.KQuery, "spammer") == nil {
		t.Fatal("a channel-only ignore should hide channel lines but not private ones")
	}
	a.ircCommand(ch, "ignore -u60 dave")
	if e, ok := a.ignoreMatch(n, mustParse(t, ":dave!d@h PRIVMSG #go :x")); !ok || time.Until(e.until) < 50*time.Second {
		t.Fatalf("/ignore -u60: %+v", e)
	}
	a.setInList(listIgnore, n, "bob!*@* pcntid until="+itoa(time.Now().Add(-time.Second).Unix()), true)
	feed(":bob!b@h PRIVMSG #go :timed out")
	if lastLine(ch) != "<bob> timed out" {
		t.Fatal("an expired timed ignore still ignored")
	}
}

// TestControl auto-ops someone listed on join, and undoes a deop of a
// protected op.
func TestControl(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	n.ChanModes = "beI,k,l,imnpst"
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true,
		Nicks: []state.Nick{{Prefix: "@", Name: "max"}}})
	a.memBook.Control = []config.ControlEntry{{Mask: "bob", Kinds: "o", Channels: []string{"#go"}},
		{Mask: "carol!*@*", Kinds: "p"}}
	m := ":bob!b@h JOIN #go"
	feed(m)
	a.Update(controlMsg{n, "#go", "bob", "+o"})
	expectSent(t, sent, "MODE #go +o bob")
	w.AddNick(state.Nick{Prefix: "@", Name: "carol"})
	feed(":evil!e@h MODE #go -o carol")
	expectSent(t, sent, "MODE #go -o+o evil carol")
}

func TestAddressBook(t *testing.T) {
	a, n, _ := realApp(t)
	a.activate(n.Status())
	a.openAddressBook()
	d := a.dlg.(*abookBox)
	d.press(a, "Add...")
	f := a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune("alice")
	f.Fields[1].Edit.Buf = []rune("Alice Liddell")
	a.dlg.key(a, "enter")
	if len(a.memBook.Addresses) != 1 || a.memBook.Addresses[0].Name != "Alice Liddell" {
		t.Fatalf("addresses %+v", a.memBook.Addresses)
	}
	a.dlg.key(a, "right") // Notify
	d.press(a, "Add...")
	f = a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune("bob")
	f.Fields[1].Edit.Buf = []rune("from work")
	f.Fields[2].On = true
	a.dlg.key(a, "enter")
	if got := strings.Join(a.netList(listNotify, n), "|"); got != "+bob from work" {
		t.Fatalf("notify list %q", got)
	}
	if rows := d.rows(a); len(rows) != 1 || !strings.Contains(rows[0], "whois") {
		t.Fatalf("notify tab rows %q", rows)
	}
	d.press(a, "Remove")
	if len(a.netList(listNotify, n)) != 0 {
		t.Fatal("Remove kept the notify entry")
	}
}

func TestPerform(t *testing.T) {
	a, n, sent, _ := optionsApp(t)
	if got := performLine("/msg NickServ identify s3cret"); got != "PRIVMSG NickServ :identify s3cret" {
		t.Fatalf("performLine = %q", got)
	}
	a.openPerform(n)
	l := a.dlg.(*listBox)
	l.press(a, "Add...")
	f := a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune("/join #go")
	a.dlg.key(a, "enter")
	expectSent(t, sent, `PRIVMSG BouncerServ :network update Libera -connect-command "JOIN #go"`)
	l = a.dlg.(*listBox)
	l.press(a, "Remove")
	expectSent(t, sent, `PRIVMSG BouncerServ :network update Libera -connect-command ""`)
}

func TestPrivateMessageModes(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.activate(ch)
	a.privateTo = 2 // the active window
	feed(":carol!c@h PRIVMSG max :psst")
	if lastLine(ch) != "*carol* psst" || n.Find(state.KQuery, "carol") != nil {
		t.Fatalf("active mode: %q", lastLine(ch))
	}
	a.privateTo = 1 // one Messages window
	feed(":dave!d@h PRIVMSG max :hey")
	if w := n.Find(state.KQuery, messagesWindow); w == nil || lastLine(w) != "<dave> hey" {
		t.Fatal("no Messages window line")
	}
	ch.Edit.Buf = nil
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if string(ch.Edit.Buf) != "/msg dave " {
		t.Fatalf("Tab replies with %q", string(ch.Edit.Buf))
	}

	// An open query still gets its own messages, both ways.
	q := n.Add(&state.Window{Kind: state.KQuery, Name: "erin"})
	feed(":max!m@h PRIVMSG erin :hi", ":erin!e@h PRIVMSG max :yo")
	if got := lineTexts(q); len(got) < 2 || !strings.Contains(got[len(got)-2], "hi") || !strings.Contains(got[len(got)-1], "yo") {
		t.Fatalf("the erin query got %q", got)
	}
}

func TestOtherOptions(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	w := a.nets[0].Wins[1]
	a.activate(w)
	a.tabSuffix = ": "
	w.Edit.Buf, w.Edit.Pos = []rune("ali"), 3
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	if string(w.Edit.Buf) != "alice: " {
		t.Fatalf("Tab gave %q", string(w.Edit.Buf))
	}
	a.cmdPrefix = "!"
	w.Edit.Buf, w.Edit.Pos = []rune("!clear"), 6
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(w.Lines) != 0 {
		t.Fatal("the ! prefix didn't run /clear")
	}
	b := New(Options{Bouncer: config.Bouncer{Address: "irc+insecure://x:1", User: "max"}})
	b.confirmExit = true
	if b.quit() != nil {
		t.Fatal("quitting didn't ask")
	}
	if _, ok := b.dlg.(*msgBox); !ok || b.quit() == nil {
		t.Fatal("no Quit dialog, or Ctrl+C twice didn't quit")
	}
}

func TestSwitchbarSortAndFill(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.query(a.nets[0], "aaron", "")
	a.sortSwitch = true
	order := a.switchOrder()
	if order[1].Name != "#go-nuts" || order[3].Name != "aaron" {
		t.Fatalf("sorted: %s %s", order[1].Name, order[3].Name)
	}
	a.fillSwitch = true
	btns, _ := a.switchLayout(3)
	last := btns[len(btns)-1]
	if last.x+last.width < a.w-1-2-3 {
		t.Fatalf("filled row ends at %d of %d", last.x+last.width, a.w)
	}
}

func TestHighlightMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, _ := config.Load(path)
	cfg.Settings.Highlight = "trout, fish"
	cfg.Save()
	cfg, err := config.Load(path)
	if err != nil || len(cfg.Highlights) != 2 || cfg.Highlights[1].Words != "fish" || cfg.Settings.Highlight != "" {
		t.Fatalf("migrated %+v, %v", cfg.Highlights, err)
	}
}

func mustParse(t *testing.T, line string) ircmsg.Message {
	m, err := ircmsg.ParseLine(line)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
