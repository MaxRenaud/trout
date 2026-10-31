package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

const hookScript = `on *:JOIN:#:if ($nick != $me) msg $chan hi $nick
ctcp *:PING:?:notice $nick pong! | halt
alias hello say hello from an alias $1-
on *:TEXT:!addr:#:msg $chan $address($nick,2)
on *:INPUT:#:if ($1 == !nope) halt
menu nicklist {
  Wave:me waves at $1
}
menu menubar {
  Say hi:say hi from the menubar
}
on ^*:PART:#:echo $chan custom part: $nick | haltdef
on *:CONNECT:echo -s connected to $network
on *:OPEN:?:*:msg $nick auto-reply
alias windowy aline @x hi | splay ding.wav | echo -a after
`

// TestScriptsHooks loads a script and drives each of trout's hooks for it
// against a fake bouncer: IRC events, a CTCP's /halt, ^ events, on OPEN and
// on CONNECT, typed lines (on INPUT, aliases, //), popups, timers, saved
// variables, /remote off and /unload.
func TestScriptsHooks(t *testing.T) {
	a, sent := fakeSoju(t)
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true,
		Nicks: []state.Nick{{Prefix: "@", Name: "max"}}})
	a.activate(w)
	feed := func(lines ...string) {
		t.Helper()
		for _, l := range lines {
			m, err := ircmsg.ParseLine(l)
			if err != nil {
				t.Fatal(err)
			}
			a.Update(ircLineMsg{n, m})
		}
	}
	typed := func(win *state.Window, text string) {
		a.activate(win)
		a.edit(win).Buf = []rune(text)
		a.submit(win)
	}
	expect := func(what string, want ...string) {
		t.Helper()
		for _, s := range want {
			select {
			case got := <-sent:
				if got != s {
					t.Errorf("%s: sent %q, want %q", what, got, s)
				}
			case <-time.After(3 * time.Second):
				t.Errorf("%s: never sent %q", what, s)
			}
		}
		select {
		case got := <-sent:
			t.Errorf("%s: also sent %q", what, got)
		case <-time.After(150 * time.Millisecond):
		}
	}
	lastLine := func(win *state.Window) string {
		if len(win.Lines) == 0 {
			return ""
		}
		return win.Lines[len(win.Lines)-1].Text
	}

	a.scriptMem = map[string]string{"hooks.mrc": hookScript}
	typed(w, "/load -rs hooks.mrc")
	if !slices.ContainsFunc(w.Lines, func(l state.Line) bool { return l.Text == "* Loaded hooks.mrc" }) ||
		lastLine(w) != "*   line 15: /aline: custom windows aren't supported" {
		t.Fatalf("loading: %q", lastLine(w))
	}
	if !slices.Contains(a.book().Scripts, "hooks.mrc") {
		t.Error("not in the config's scripts")
	}

	feed(":bob!b@h.example JOIN #go")
	expect("on JOIN", "PRIVMSG #go :hi bob")

	feed(":bob!b@h.example PRIVMSG max :\x01PING 123\x01")
	expect("a CTCP that /halts: no reply from trout", "NOTICE bob pong!")

	feed(":bob!b@h.example PRIVMSG #go :!addr")
	expect("$address from the address list", "PRIVMSG #go *!*@h.example")

	typed(w, "/hello world")
	expect("an alias", "PRIVMSG #go :hello from an alias world")
	typed(w, "//echo -a I am $me")
	if got := lastLine(w); got != "I am max" {
		t.Errorf("//echo: %q", got)
	}
	typed(w, "!nope")
	expect("on INPUT halts")
	typed(w, "fine")
	expect("on INPUT lets it through", "PRIVMSG #go fine")

	feed(":bob!b@h.example PART #go :bye")
	if got := lastLine(w); got != "custom part: bob" {
		t.Errorf("a ^ event's text instead of trout's: %q", got)
	}
	for _, l := range w.Lines {
		if strings.Contains(l.Text, "Parts") {
			t.Errorf("trout's part line was shown: %q", l.Text)
		}
	}

	feed(":soju 376 max :End of MOTD")
	if got := lastLine(n.Status()); got != "connected to Libera" {
		t.Errorf("on CONNECT: %q", got)
	}

	feed(":carol!c@h PRIVMSG max :hello?")
	expect("on OPEN for a new query", "PRIVMSG carol auto-reply")
	feed(":carol!c@h PRIVMSG max :again")
	expect("no on OPEN for the same query")

	a.activate(w)
	a.nickMenu(w, "bob", 0, 0)
	if !runMenuItem(a, "Wave") {
		t.Fatal("no Wave in the nicklist popup")
	}
	expect("a nicklist popup item", "PRIVMSG #go :\x01ACTION waves at bob\x01")
	for _, it := range a.menuBarItems(4) {
		if it.Label == "Say hi" {
			it.Action(a)
		}
	}
	expect("a menubar item", "PRIVMSG #go :hi from the menubar")

	typed(w, "/windowy")
	expect("mIRC's window and sound commands don't reach the server")
	if got := lastLine(n.Status()); got != "* /aline: custom windows aren't supported" {
		t.Errorf("/aline: %q", got)
	}
	if got := lastLine(w); got != "after" {
		t.Errorf("after /aline: %q", got)
	}
	typed(w, "/ping bob")
	select {
	case got := <-sent:
		if !strings.HasPrefix(got, "PRIVMSG bob :\x01PING ") {
			t.Errorf("/ping: sent %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Error("/ping sent nothing")
	}

	typed(w, "/timer 1 0 msg #go tick")
	a.scriptTick()
	expect("a timer", "PRIVMSG #go tick")

	typed(w, "/set %count 3")
	if !strings.Contains(a.scriptMem["vars.ini"], "%count 3") {
		t.Errorf("vars.ini = %q", a.scriptMem["vars.ini"])
	}
	typed(w, "/auser 5 bob")
	if !strings.Contains(a.scriptMem["users.ini"], "5:bob") {
		t.Errorf("users.ini = %q", a.scriptMem["users.ini"])
	}

	typed(w, "/remote off")
	feed(":dave!d@h JOIN #go")
	expect("/remote off")
	typed(w, "/remote on")

	typed(w, "/unload -rs hooks.mrc")
	feed(":erin!e@h JOIN #go")
	expect("unloaded")
	if slices.Contains(a.book().Scripts, "hooks.mrc") {
		t.Error("still in the config's scripts")
	}

	// Files of aliases and popup items, and mIRC's variables file.
	a.scriptMem["aliases.txt"] = "shout say $upper($1-)\nwhisper {\n  say $lower($1-)\n}"
	a.scriptMem["nicks.txt"] = "Hug:me hugs $1"
	a.scriptMem["old-vars.ini"] = "[variables]\nn0=%imported yes\n"
	typed(w, "/load -a aliases.txt")
	typed(w, "/load -pn nicks.txt")
	typed(w, "/load -rv old-vars.ini")
	if !slices.Contains(a.book().Scripts, "-a aliases.txt") || !slices.Contains(a.book().Scripts, "-pn nicks.txt") {
		t.Errorf("scripts = %q", a.book().Scripts)
	}
	typed(w, "/shout hey")
	expect("an alias from an aliases file", "PRIVMSG #go HEY")
	a.nickMenu(w, "bob", 0, 0)
	if !runMenuItem(a, "Hug") {
		t.Error("no Hug in the nicklist popup")
	}
	expect("a popup from a popups file", "PRIVMSG #go :\x01ACTION hugs bob\x01")
	if a.scripts.Vars()["imported"] != "yes" {
		t.Errorf("variables = %v", a.scripts.Vars())
	}
}

// runMenuItem runs the open menu's item with this label, anywhere in it.
func runMenuItem(a *App, label string) bool {
	var find func([]MenuItem) bool
	find = func(items []MenuItem) bool {
		for _, it := range items {
			if it.Label == label && it.Action != nil {
				it.Action(a)
				return true
			}
			if find(it.Sub) {
				return true
			}
		}
		return false
	}
	for _, m := range a.menus {
		if find(m.Items) {
			return true
		}
	}
	return false
}

// TestScriptsFolder runs scripts from a real config: loaded at startup
// from the scripts folder, with on START, vars.ini and users.ini written
// there, and files a script can't write outside it.
func TestScriptsFolder(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(folder, "start.mrc"), []byte(`on *:START:{
  inc %starts
  write notes.txt started
  write ..\escape.txt nope
}
alias who echo -a $level(boss) $readini(settings.ini,main,greeting)`), 0o600)
	os.WriteFile(filepath.Join(folder, "settings.ini"), []byte("[main]\r\ngreeting=hello\r\n"), 0o600)
	os.WriteFile(filepath.Join(folder, "users.ini"), []byte("[users]\r\nn0=10:boss\r\n"), 0o600)
	os.WriteFile(filepath.Join(folder, "vars.ini"), []byte("[variables]\r\nn0=%starts 4\r\n"), 0o600)
	cfg := &config.Config{Path: filepath.Join(dir, "config.toml"), Scripts: []string{"start.mrc"}}
	a := New(Options{Config: cfg, Bouncer: config.Bouncer{Address: "irc+insecure://127.0.0.1:1", User: "max"}})
	a.startScripts()

	vars, _ := os.ReadFile(filepath.Join(folder, "vars.ini"))
	if !strings.Contains(string(vars), "%starts 5") {
		t.Errorf("vars.ini = %q", vars)
	}
	notes, _ := os.ReadFile(filepath.Join(folder, "notes.txt"))
	if string(notes) != "started\r\n" {
		t.Errorf("notes.txt = %q", notes)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Error("a script wrote outside the scripts folder")
	}
	if !slices.ContainsFunc(a.home.Status().Lines, func(l state.Line) bool { return strings.Contains(l.Text, "escape.txt") }) {
		t.Error("no error for writing outside the folder")
	}
	actions, ok := a.scripts.Command(a.home.Name, "", "who", false, a.scriptHost())
	if !ok {
		t.Fatal("no alias who")
	}
	a.runScript(actions)
	if got := a.active.Lines[len(a.active.Lines)-1].Text; got != "10 hello" {
		t.Errorf("who: %q", got)
	}
}

// TestScriptsDialog walks Tools > Scripts: loaded scripts with their
// problems, the Users list and the variables.
func TestScriptsDialog(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{"ok.mrc": "alias hi echo -a hi", "odd.mrc": "on *:FOO:#:echo\nalias x echo $window(@x)"}
	w := a.active
	for _, s := range []string{"ok.mrc", "odd.mrc"} {
		a.edit(w).Buf = []rune("/load -rs " + s)
		a.submit(w)
	}
	a.openScripts()
	l, ok := a.dlg.(*listBox)
	if !ok {
		t.Fatalf("dialog = %T", a.dlg)
	}
	if want := []string{padRight("ok.mrc", 32) + "ok", padRight("odd.mrc", 32) + "2 problems"}; !slices.Equal(l.Rows, want) {
		t.Errorf("rows = %q", l.Rows)
	}
	l.Sel = 1
	l.press(a, "Problems")
	if d, ok := a.dlg.(*msgBox); !ok || !strings.Contains(strings.Join(d.Lines, "\n"), "on FOO isn't supported") {
		t.Errorf("problems = %#v", a.dlg)
	}
	a.dlg = nil

	a.edit(w).Buf = []rune("/auser 5,friend *!*@h.example")
	a.submit(w)
	a.edit(w).Buf = []rune("/set %greeting hi there")
	a.submit(w)
	a.openScriptUsers()
	if l := a.dlg.(*listBox); !slices.Equal(l.Rows, []string{"5,=friend:*!*@h.example"}) {
		t.Errorf("users = %q", l.Rows)
	}
	a.openScriptVars()
	if l := a.dlg.(*listBox); !slices.Equal(l.Rows, []string{padRight("%greeting", 24) + "hi there"}) {
		t.Errorf("variables = %q", l.Rows)
	}
	l = a.dlg.(*listBox)
	l.press(a, "Remove")
	if l := a.dlg.(*listBox); len(l.Rows) != 0 {
		t.Errorf("after Remove: %q", l.Rows)
	}
}

// TestScriptsReviewFixes covers the hook-up review's findings.
func TestScriptsReviewFixes(t *testing.T) {
	a, sent := fakeSoju(t)
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})
	a.activate(w)
	feed := func(line string) {
		m, _ := ircmsg.ParseLine(line)
		a.Update(ircLineMsg{n, m})
	}
	quiet := func(what string) {
		t.Helper()
		select {
		case got := <-sent:
			t.Errorf("%s: sent %q", what, got)
		case <-time.After(150 * time.Millisecond):
		}
	}
	a.scriptMem = map[string]string{"r.mrc": `on ^*:OPEN:?:*:if (spam isin $1-) halt
on *:TEXT:*:?:msg $nick hi
on ^*:TEXT:*:?:if (hide isin $1-) haltdef
on *:CLOSE:?:echo -s closed with $nick
on *:INPUT:#:.timer 1 60 echo -a later
alias tryload load -rs /etc/hosts
on ^*:OP:#:echo $chan opped $opnick | haltdef`}
	if cmd, ok := a.scriptAdmin("load -rs r.mrc", false); !ok {
		t.Fatal("no /load")
	} else {
		a.later(cmd)
	}

	// ^OPEN halting: no window, no on TEXT.
	feed(":spammer!s@h PRIVMSG max :buy spam now")
	if n.Find(state.KQuery, "spammer") != nil {
		t.Error("the query opened")
	}
	quiet("on TEXT after a halted ^OPEN")

	// A hidden line doesn't beep or become the last query.
	a.beep, a.bellDue = true, false
	q := a.query(n, "carol", "")
	feed(":carol!c@h PRIVMSG max :hide this")
	<-sent // on TEXT's hi
	if a.bellDue || a.lastQuery == q {
		t.Error("a hidden line beeped or became the last query")
	}

	// on CLOSE's $nick is the query's.
	a.closeWindow(q)
	a.takeScriptCmds()
	if got := n.Status().Lines[len(n.Status().Lines)-1].Text; got != "closed with carol" {
		t.Errorf("on CLOSE: %q", got)
	}

	// on INPUT's timer is scheduled, not dropped.
	a.edit(w).Buf = []rune("hello")
	if a.submit(w) == nil || a.scriptTickAt.IsZero() {
		t.Error("on INPUT's commands were dropped")
	}
	<-sent // the PRIVMSG

	// ^OP runs, and hides trout's text.
	before := len(w.Lines)
	feed(":bob!b@h MODE #go +o alice")
	if got := w.Lines[len(w.Lines)-1].Text; got != "opped alice" || len(w.Lines) != before+1 {
		t.Errorf("^OP: %q (%d new lines)", got, len(w.Lines)-before)
	}

	// A script can't /load from outside the scripts folder.
	actions, _ := a.scripts.Command(n.Name, "#go", "tryload", false, a.scriptHost())
	a.runScript(actions)
	if got := a.firstStatus().Lines[len(a.firstStatus().Lines)-1].Text; !strings.Contains(got, "only load files from the scripts folder") {
		t.Errorf("/load from a script: %q", got)
	}

	// $idle counts from the start.
	if idle := a.scriptHost().Info(n.Name).Idle; idle > time.Minute {
		t.Errorf("$idle = %v", idle)
	}

	// /timestamp -f sets the format, and leaves them on or off.
	stamps := a.stamps
	a.command(w, "timestamp -f [HH:nn:ss]")
	if a.stampFormat != "[HH:nn:ss]" || a.stamps != stamps {
		t.Errorf("format %q, stamps %v", a.stampFormat, a.stamps)
	}
}

// TestScriptsLoadButton: Tools > Scripts > Load... loads the script.
func TestScriptsLoadButton(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{"x.mrc": "alias x echo -a x"}
	a.openScripts()
	a.dlg.(*listBox).press(a, "Load...")
	f := a.dlg.(*formBox)
	f.Fields[0].Edit = state.EditBox{Buf: []rune("x.mrc")}
	a.dlg.key(a, "enter")
	if !slices.Contains(a.book().Scripts, "x.mrc") {
		t.Errorf("scripts = %q", a.book().Scripts)
	}
}
