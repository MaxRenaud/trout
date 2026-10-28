package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/demo"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

func TestSplitText(t *testing.T) {
	long := strings.Repeat("trout ", 100) + strings.Repeat("é", 300)
	parts := splitText(long, 200)
	if strings.ReplaceAll(strings.Join(parts, ""), " ", "") != strings.ReplaceAll(long, " ", "") {
		t.Fatal("splitting lost or added text")
	}
	for i, p := range parts {
		if len(p) > 200 || !utf8.ValidString(p) {
			t.Fatalf("part %d is %d bytes, valid UTF-8 %v", i, len(p), utf8.ValidString(p))
		}
	}
	if parts[0][len(parts[0])-1] == ' ' || !strings.HasSuffix(parts[0], "trout") {
		t.Errorf("the first part should end at a word: %q", parts[0][len(parts[0])-10:])
	}
}

// TestLongMessage sends a message too long for one IRC line as two.
func TestLongMessage(t *testing.T) {
	a, sent := fakeSoju(t)
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})
	a.say(w, strings.Repeat("word ", 150))
	for i := range 2 {
		select {
		case got := <-sent:
			if !strings.HasPrefix(got, "PRIVMSG #go :word") || len(got) > 510 {
				t.Fatalf("line %d: %d bytes: %.40q", i, len(got), got)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d lines sent", i)
		}
	}
}

func TestMentions(t *testing.T) {
	for text, want := range map[string]bool{
		"max: hi": true, "hey @max!": true, "ping Max": true, "the maximum": false,
		"maxwell": false, "max_": false, "\x02max\x02 look": true, "": false,
	} {
		if got := mentions(text, "max"); got != want {
			t.Errorf("mentions(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestCaseMapping(t *testing.T) {
	n := state.NewNetwork("Rizon")
	n.Add(&state.Window{Kind: state.KQuery, Name: "[EWG]"})
	if n.Find(state.KQuery, "{ewg}") == nil {
		t.Error("rfc1459: {ewg} should find [EWG]")
	}
	n.CaseMapping = "ascii"
	if n.Find(state.KQuery, "{ewg}") != nil || n.Find(state.KQuery, "[ewg]") == nil {
		t.Error("ascii: only letters fold")
	}
}

// TestPaste sends a pasted block as one message per line, after asking.
func TestPaste(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	w := a.nets[0].Wins[1]
	a.activate(w)
	before := len(w.Lines)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\ntwo\r\nthree\n"), Paste: true})
	d, ok := a.dlg.(*msgBox)
	if !ok || !strings.Contains(d.Lines[0], "Paste 3 lines") {
		t.Fatalf("a 3-line paste opened %T", a.dlg)
	}
	a.dlg.key(a, "y")
	if len(w.Lines) != before+3 || !strings.HasSuffix(lastLine(w), "three") {
		t.Fatalf("sent %d lines, last %q", len(w.Lines)-before, lastLine(w))
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("just one\n"), Paste: true})
	if a.dlg != nil || string(w.Edit.Buf) != "just one" {
		t.Fatalf("a one-line paste: dialog %T, editbox %q", a.dlg, string(w.Edit.Buf))
	}
}

// TestNotifyISON polls with ISON on a network without MONITOR.
func TestNotifyISON(t *testing.T) {
	a, n, feed := realApp(t)
	a.setNotify(n, "alice", true)
	a.setNotify(n, "bob", true)
	feed(":srv 303 max :Alice")
	if !n.Online["alice"] || n.Online["bob"] || !strings.HasPrefix(lastLine(n.Status()), "* alice is on Libera at ") {
		t.Fatalf("after ISON: %v, %q", n.Online, lastLine(n.Status()))
	}
	feed(":srv 303 max :")
	if n.Online["alice"] {
		t.Fatal("alice still online after an empty ISON")
	}
}

func TestColors(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.toolAction("Colors")
	d, ok := a.dlg.(*colorsBox)
	if !ok {
		t.Fatalf("the Colors button opened %T", a.dlg)
	}
	d.sel = 3 // Action text
	a.dlg.key(a, "right")
	idx := a.colors[state.RoleAction]
	if a.theme().Roles[state.RoleAction] != themes[0].Pal[idx] {
		t.Fatal("the choice didn't change the action color")
	}
	s := a.settings().Colors
	if s != "action="+itoa(int64(idx)) {
		t.Fatalf("saved as %q", s)
	}
	b := New(Options{Demo: true})
	b.colors = parseColors(s)
	if b.theme().Roles[state.RoleAction] != a.theme().Roles[state.RoleAction] {
		t.Fatal("the saved colors didn't load")
	}
	a.dlg.key(a, "r")
	if len(a.colors) != 0 {
		t.Fatal("Reset kept the choices")
	}
}

func TestWallopsAndChghost(t *testing.T) {
	a, n, feed := realApp(t)
	q := a.query(n, "alice", "a@old")
	feed(":oper!o@h WALLOPS :maintenance at noon", ":alice!a@old CHGHOST alice new.host")
	if lastLine(n.Status()) != "!oper! maintenance at noon" || q.Host != "alice@new.host" {
		t.Fatalf("status %q, host %q", lastLine(n.Status()), q.Host)
	}
}

func TestHighlightWords(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.memBook.Highlights = []config.HighlightRule{{Words: "trout, release notes", MatchText: true, Color: -1, Beep: true}}
	feed(":bob!b@h PRIVMSG #go :new release notes are out", ":bob!b@h PRIVMSG #go :troutfishing")
	if ch.Lines[len(ch.Lines)-2].Role != state.RoleHighlight || lastLine(ch) == "" || ch.Lines[len(ch.Lines)-1].Role == state.RoleHighlight {
		t.Fatal("highlight words should match as words")
	}
}

// TestHistorySearch searches the bouncer from Ctrl+F, lists the results,
// and Go To loads the history around one and highlights it.
func TestHistorySearch(t *testing.T) {
	a, sent := fakeSoju(t)
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	a.control.Caps["soju.im/search"] = true
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})
	a.activate(w)
	a.View()
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-sent:
			if got != want {
				t.Fatalf("sent %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("never sent %q", want)
		}
	}
	feed := func(lines ...string) {
		for _, l := range lines {
			m, _ := ircmsg.ParseLine(l)
			a.Update(ircLineMsg{n, m})
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	f := a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune("generics")
	f.Fields[1].On = true
	a.dlg.key(a, "enter")
	expect("SEARCH in=#go;text=generics;limit=200")
	feed("BATCH +s soju.im/search",
		"@batch=s;time=2025-03-01T10:00:00.000Z :bob!b@h PRIVMSG #go :generics landed",
		"BATCH -s")
	l, ok := a.dlg.(*listBox)
	if !ok || len(l.Keys) != 1 || !strings.Contains(l.Rows[0], "<bob> generics landed") {
		t.Fatalf("results: %T %+v", a.dlg, a.dlg)
	}
	a.dlg.key(a, "enter")
	expect("CHATHISTORY AROUND #go timestamp=2025-03-01T10:00:00.000Z 100")
	feed("BATCH +h chathistory #go",
		"@batch=h;time=2025-03-01T09:59:00.000Z :alice!a@h PRIVMSG #go :before",
		"@batch=h;time=2025-03-01T10:00:00.000Z :bob!b@h PRIVMSG #go :generics landed",
		"BATCH -h")
	if a.find == nil || w.Lines[a.find.line].Text != "<bob> generics landed" || a.active != w {
		t.Fatalf("Go To didn't highlight the result: %+v", a.find)
	}
}

func TestCertFallback(t *testing.T) {
	a, _, _ := realApp(t)
	a.bnc.Login = "certificate"
	a.Update(ircDownMsg{a.home, fmt.Errorf("registering: %w: bad cert", irc.ErrLogin)})
	if !a.certFailed || a.dlg != nil || a.stopped {
		t.Fatalf("a refused certificate: failed %v, dialog %T, stopped %v; want a retry with the password", a.certFailed, a.dlg, a.stopped)
	}
	a.Update(ircDownMsg{a.home, fmt.Errorf("registering: %w: bad password", irc.ErrLogin)})
	if _, ok := a.dlg.(*formBox); !ok {
		t.Fatal("a refused password too didn't open the Connect dialog")
	}
}

func TestGetWindowCloses(t *testing.T) {
	a, n, _ := realApp(t)
	a.dccClose = true
	tr := &transfer{net: n, nick: "bot", file: "f", size: 1, started: time.Now()}
	tr.win = n.Add(&state.Window{Kind: state.KTransfer, Name: "Get f"})
	a.transfers = append(a.transfers, tr)
	a.Update(dccDoneMsg{tr, 1, nil})
	if n.Find(state.KTransfer, "Get f") != nil {
		t.Fatal("a finished Get window stayed open with \"close when done\" on")
	}
}

func TestChannelListOnDisk(t *testing.T) {
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	a := New(Options{Config: cfg, Bouncer: config.Bouncer{Name: "b", Address: "irc+insecure://x:1", User: "max"}})
	n := state.NewNetwork("Rizon")
	a.chanLists[n] = &chanList{rows: []demo.ChanInfo{{Name: "#a", Users: 3}}, at: time.Now()}
	a.saveChannelList(n)
	b := New(Options{Config: cfg, Bouncer: config.Bouncer{Name: "b", Address: "irc+insecure://x:1", User: "max"}})
	if c := b.loadChannelList(n); c == nil || len(c.rows) != 1 || c.rows[0].Name != "#a" {
		t.Fatalf("loaded %+v", c)
	}
}

func TestChatOffer(t *testing.T) {
	a, n, feed := realApp(t)
	a.dccWarn = false
	feed(":bob!b@h PRIVMSG max :\x01DCC CHAT chat 134744072 5000\x01")
	d, ok := a.dlg.(*msgBox)
	if !ok || d.Title != "DCC Chat" {
		t.Fatalf("a chat offer opened %T", a.dlg)
	}
	a.dlg.key(a, "d")
	a.dccChat = 2
	feed(":bob!b@h PRIVMSG max :\x01DCC CHAT chat 134744072 5000\x01")
	if a.dlg != nil || !strings.Contains(lastLine(n.Status()), "Ignored a DCC chat") {
		t.Fatal("Ignore all chat offers still asked")
	}
}
