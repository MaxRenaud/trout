package ui

import (
	"maps"
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

// TestScriptsEditor walks the Scripts Editor: each tab saves its file in
// mIRC's format and loads it, Cancel forgets, and Users and Variables
// replace the lists.
func TestScriptsEditor(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{}
	w := a.active
	paste := func(s string) { a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: true}) }
	press := func(k tea.KeyType) { a.Update(tea.KeyMsg{Type: k}) }
	editor := func() *scriptEditor {
		t.Helper()
		ed, ok := a.dlg.(*scriptEditor)
		if !ok {
			t.Fatalf("dialog = %T", a.dlg)
		}
		return ed
	}
	typed := func(line string) {
		a.edit(w).Buf = []rune(line)
		a.submit(w)
	}
	lastLine := func() string { return w.Lines[len(w.Lines)-1].Text }

	// Aliases: aliases.ini, loaded with -a.
	a.openScriptsTab(edAliases)
	ed := editor()
	if got := ed.title(); got != "Scripts Editor - aliases.ini" {
		t.Errorf("title = %q", got)
	}
	paste("/hi echo -a hello")
	press(tea.KeyEnter)
	paste("/hi2 echo -a two")
	ed.press(a, "OK")
	if a.dlg != nil {
		t.Fatalf("still open: %T", a.dlg)
	}
	if got, want := a.scriptMem["aliases.ini"], "[aliases]\r\nn0=/hi echo -a hello\r\nn1=/hi2 echo -a two\r\n"; got != want {
		t.Errorf("aliases.ini = %q, want %q", got, want)
	}
	if !slices.Contains(a.book().Scripts, "-a aliases.ini") {
		t.Errorf("scripts = %q", a.book().Scripts)
	}
	typed("/hi2")
	if got := lastLine(); got != "two" {
		t.Errorf("/hi2: %q", got)
	}

	// Remote: remote.ini, with a pasted multi-line alias, kept when the
	// editor opens again; a second save reloads it.
	a.openScriptsTab(edRemote)
	ed = editor()
	paste("alias bye {\r\n  echo -a bye now\r\n}")
	ed.press(a, "OK")
	if got := a.scriptMem["remote.ini"]; got != "[script]\r\nn0=alias bye {\r\nn1=  echo -a bye now\r\nn2=}\r\n" {
		t.Errorf("remote.ini = %q", got)
	}
	typed("/bye")
	if got := lastLine(); got != "bye now" {
		t.Errorf("/bye: %q", got)
	}
	a.openScripts()
	ed = editor()
	if ed.tab != edRemote || ed.doc().text() != "alias bye {\n  echo -a bye now\n}" {
		t.Errorf("reopened on %v: %q", ed.tab, ed.doc().text())
	}
	a.key(tea.KeyMsg{Type: tea.KeyCtrlEnd})
	press(tea.KeyEnter)
	paste("alias hey echo -a hey")
	a.key(tea.KeyMsg{Type: tea.KeyCtrlS})
	if a.dlg != ed {
		t.Errorf("Save closed the editor: %T", a.dlg)
	}
	typed("/hey")
	if got := lastLine(); got != "hey" {
		t.Errorf("/hey: %q", got)
	}
	ed.press(a, "Cancel")

	// Popups: each View is one of popups.ini's sections.
	a.openScriptsTab(edPopups)
	ed = editor()
	if got := ed.title(); got != "Scripts Editor - Status" {
		t.Errorf("title = %q", got)
	}
	items, _, _ := ed.stripMenu(a, 2) // View
	var labels []string
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	if want := []string{"1 Status", "2 Channel", "3 Query", "4 Nickname List", "5 Menu Bar"}; !slices.Equal(labels, want) {
		t.Errorf("View = %q", labels)
	}
	// The Nickname List is trout's nick menu: replacing it replaces that.
	items[3].Action(a)
	if got := ed.doc().text(); !strings.HasPrefix(got, "Info:/uwho $1\nWhois:/whois $$1") {
		t.Errorf("Nickname List starts %q", got[:min(len(got), 40)])
	}
	a.key(tea.KeyMsg{Type: tea.KeyCtrlA})
	paste("Hello:/echo -a hi $$1")
	ed.press(a, "OK")
	if got := a.scriptMem["popups.ini"]; !strings.Contains(got, "[lpopup]\r\nn0=Hello:/echo -a hi $$1\r\n[") ||
		!strings.Contains(got, "[bpopup]\r\nn0=Commands\r\n") {
		t.Errorf("popups.ini = %q", got)
	}
	if slices.ContainsFunc(a.book().Scripts, func(s string) bool { return strings.Contains(s, "popups.ini") }) {
		t.Errorf("popups.ini in the scripts: %q", a.book().Scripts)
	}
	if got := a.scriptMenu("nicklist", w, []string{"bob"}); len(got) != 1 || got[0].Label != "Hello" {
		t.Errorf("nicklist popup = %+v", got)
	}
	got := a.scriptMenu("nicklist", w, []string{"bob"})
	got[0].Action(a)
	if l := w.Lines[len(w.Lines)-1].Text; l != "hi bob" {
		t.Errorf("Hello: %q", l)
	}

	// Users and Variables replace the lists; a line that isn't right
	// keeps the editor open.
	typed("/auser 5 old!*@old.example")
	a.openScriptsTab(edUsers)
	ed = editor()
	a.key(tea.KeyMsg{Type: tea.KeyCtrlA})
	paste("5,friend:*!*@h.example")
	ed.setTab(a, edVars)
	paste("greeting hi")
	ed.press(a, "OK")
	if m, ok := a.dlg.(*msgBox); !ok || !strings.Contains(strings.Join(m.Lines, "\n"), "line 1: not %name value") {
		t.Fatalf("dialog = %#v", a.dlg)
	}
	a.dlg.key(a, "enter")
	ed = editor()
	a.key(tea.KeyMsg{Type: tea.KeyHome})
	paste("%")
	ed.press(a, "OK")
	if got := a.scripts.Users(); !slices.Equal(got, []string{"5,friend:*!*@h.example"}) {
		t.Errorf("users = %q", got)
	}
	if got := a.scriptMem["users.ini"]; got != "[users]\r\nn0=5,friend:*!*@h.example\r\n" {
		t.Errorf("users.ini = %q", got)
	}
	if got := a.scripts.Vars(); !maps.Equal(got, map[string]string{"greeting": "hi"}) {
		t.Errorf("vars = %v", got)
	}

	// Cancel asks to save first (Options > Ask to Save Files): its
	// Cancel goes back, No forgets. Undo and Redo.
	a.openScriptsTab(edRemote)
	ed = editor()
	paste("junk")
	ed.press(a, "Cancel")
	if m, ok := a.dlg.(*msgBox); !ok || m.Lines[0] != "Save the changes to your scripts?" {
		t.Fatalf("Cancel: %#v", a.dlg)
	}
	a.dlg.key(a, "esc") // its Cancel: back to the editor
	if a.dlg != ed {
		t.Fatalf("Cancel, Cancel: %T", a.dlg)
	}
	a.key(tea.KeyMsg{Type: tea.KeyCtrlZ})
	if strings.Contains(ed.doc().text(), "junk") {
		t.Errorf("undo: %q", ed.doc().text())
	}
	a.key(tea.KeyMsg{Type: tea.KeyCtrlY})
	if !strings.Contains(ed.doc().text(), "junk") {
		t.Errorf("redo: %q", ed.doc().text())
	}
	press(tea.KeyEscape)
	a.dlg.key(a, "right")
	a.dlg.key(a, "enter") // No
	if a.dlg != nil || strings.Contains(a.scriptMem["remote.ini"], "junk") {
		t.Errorf("No: %T, %q", a.dlg, a.scriptMem["remote.ini"])
	}
	// Yes saves; with the option off, Cancel just forgets.
	a.openScriptsTab(edRemote)
	paste("alias yes echo -a yes")
	ed = editor()
	ed.press(a, "Cancel")
	a.dlg.key(a, "enter") // Yes
	if a.dlg != nil || !strings.Contains(a.scriptMem["remote.ini"], "alias yes") {
		t.Errorf("Yes: %T, %q", a.dlg, a.scriptMem["remote.ini"])
	}
	a.book().ScriptOpts.NoAskSave = true
	a.openScriptsTab(edRemote)
	paste("junk")
	editor().press(a, "Cancel")
	if a.dlg != nil || strings.Contains(a.scriptMem["remote.ini"], "junk") {
		t.Errorf("Cancel without asking: %T, %q", a.dlg, a.scriptMem["remote.ini"])
	}
}

// TestScriptsEditorFiles: File > Load... and New on the Remote tab, the
// ✓ button, and a loaded script's initialization, which waits until the
// editor has closed, like mIRC's.
func TestScriptsEditorFiles(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{"x.mrc": "on *:LOAD:set %loaded 1\nalias x echo -a x", "odd.mrc": "on *:FOO:#:echo"}
	a.openScriptsTab(edRemote)
	ed := a.dlg.(*scriptEditor)
	ed.loadPrompt(a)
	f := a.dlg.(*formBox)
	f.Fields[0].Edit = state.EditBox{Buf: []rune("x.mrc")}
	a.dlg.key(a, "enter")
	if a.dlg != ed || ed.doc().file != "x.mrc" || !ed.doc().load {
		t.Fatalf("after Load: %T, %+v", a.dlg, ed.doc())
	}
	if slices.Contains(a.book().Scripts, "x.mrc") {
		t.Error("loaded before OK")
	}
	ed.newDoc(a)
	if got := ed.doc().file; got != "script1.mrc" {
		t.Errorf("New: %q", got)
	}
	ed.typed(a, []rune("alias new1 echo -a new"))
	ed.press(a, "OK")
	if want := []string{"x.mrc", "script1.mrc"}; !slices.Equal(a.book().Scripts, want) {
		t.Errorf("scripts = %q, want %q", a.book().Scripts, want)
	}
	if m, ok := a.dlg.(*msgBox); !ok || m.Title != "Script Warning" {
		t.Fatalf("no Script Warning: %T", a.dlg)
	}
	a.dlg.key(a, "enter")
	if got := a.scripts.Vars()["loaded"]; got != "1" {
		t.Errorf("on LOAD didn't run: %q", got)
	}
	if got := a.scriptMem["script1.mrc"]; got != "alias new1 echo -a new\n" {
		t.Errorf("script1.mrc = %q", got)
	}

	// The check button shows what trout can't run.
	a.openScriptsTab(edRemote)
	ed = a.dlg.(*scriptEditor)
	ed.loadPrompt(a)
	a.dlg.(*formBox).Fields[0].Edit = state.EditBox{Buf: []rune("odd.mrc")}
	a.dlg.key(a, "enter")
	ed.check(a)
	if m, ok := a.dlg.(*msgBox); !ok || !strings.Contains(strings.Join(m.Lines, "\n"), "on FOO isn't supported") {
		t.Errorf("check: %#v", a.dlg)
	}
	a.dlg.key(a, "enter")

	// Save As loads the script under its new name instead.
	ed.cur[edRemote] = ed.findDoc(edRemote, "script1.mrc")
	ed.saveAsPrompt(a)
	a.dlg.(*formBox).Fields[0].Edit = state.EditBox{Buf: []rune("renamed.mrc")}
	a.dlg.key(a, "enter")
	if ed.doc().file != "renamed.mrc" || ed.title() != "Scripts Editor - renamed.mrc" {
		t.Errorf("Save As: %q", ed.doc().file)
	}

	// File > Unload takes a script out on OK.
	ed.cur[edRemote] = ed.findDoc(edRemote, "x.mrc")
	ed.unload(a, ed.doc())
	ed.press(a, "OK")
	if want := []string{"renamed.mrc", "odd.mrc"}; !slices.Equal(a.book().Scripts, want) {
		t.Errorf("scripts = %q, want %q", a.book().Scripts, want)
	}
	if got := a.scriptMem["renamed.mrc"]; got != "alias new1 echo -a new\n" {
		t.Errorf("renamed.mrc = %q", got)
	}
	if _, ok := a.scripts.Alias(a.active.Net.Name, "", "new1", "", a.scriptHost()); !ok {
		t.Error("new1 went with script1.mrc")
	}

	// popups.ini is loaded again whichever of its menus changed first.
	a.openScriptsTab(edPopups)
	ed = a.dlg.(*scriptEditor)
	ed.typed(a, []rune("x"))
	ed.key(a, "backspace") // Status: changed back
	ed.cur[edPopups] = ed.docs[1]
	ed.key(a, "ctrl+a")
	ed.typed(a, []rune("Hello:/echo -a hello"))
	ed.press(a, "OK")
	if got := a.scriptMenu("channel", a.active, nil); len(got) != 1 || got[0].Label != "Hello" {
		t.Errorf("channel popup = %+v", got)
	}
}

// TestScriptsEditorKeys: the editor's menus open over it, the mouse puts
// the caret, and text is selected, copied and found.
func TestScriptsEditorKeys(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	a.scriptMem = map[string]string{"remote.ini": "[script]\r\nn0=alias one echo -a 1\r\nn1=alias two echo -a 2\r\n"}
	a.openScriptsTab(edRemote)
	ed := a.dlg.(*scriptEditor)
	a.View()

	// Alt+E is the Edit menu, over the editor; Esc closes only the menu.
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e"), Alt: true})
	if len(a.menus) != 1 || !a.dlgMenu || a.menus[0].Items[0].Label != "Undo" {
		t.Fatalf("menus = %+v", a.menus)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRight})
	if a.menuBar != 2 || a.menus[0].Items[0].Label != "1 remote.ini" {
		t.Errorf("Right: menu %d, %+v", a.menuBar, a.menus[0].Items)
	}
	if !strings.Contains(a.View(), "remote.ini") {
		t.Error("menu not drawn")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if len(a.menus) != 0 || a.dlg != ed {
		t.Fatalf("Esc: menus %d, %T", len(a.menus), a.dlg)
	}

	// A click on line 2, column 7 puts the caret there.
	a.View()
	a.Update(tea.MouseMsg{X: ed.tx + 6, Y: ed.ty + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	a.Update(tea.MouseMsg{X: ed.tx + 6, Y: ed.ty + 1, Action: tea.MouseActionRelease})
	d := ed.doc()
	if d.row != 1 || d.col != 6 {
		t.Errorf("caret at %d:%d", d.row, d.col)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	a.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	a.Update(tea.KeyMsg{Type: tea.KeyShiftRight})
	if got := d.selText(); got != "two" {
		t.Errorf("selected %q", got)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if got := string(d.lines[1]); got != "alias x echo -a 2" {
		t.Errorf("typed over the selection: %q", got)
	}

	// Find goes round to the top.
	ed.find = "ONE"
	ed.findNext(a)
	if d.row != 0 || d.selText() != "one" {
		t.Errorf("find: %d %q", d.row, d.selText())
	}
	if got := ed.title(); got != "Scripts Editor - remote.ini" {
		t.Errorf("title = %q", got)
	}
	if !strings.Contains(a.View(), "File: "+filepath.Join("scripts", "remote.ini")) {
		t.Error("no status line")
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

// TestScriptsEditorMenus walks the menus mIRC's editor has: Index's
// submenus, Listen, the Options, Check Brackets, More Files, Rename,
// Delete, Order, Monitor File Changes and Help.
func TestScriptsEditorMenus(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{
		"remote.ini": "[script]\r\nn0=alias envtest echo -a from an alias\r\nn1=on *:TEXT:!echotest:#:envtest\r\nn2=on *:JOIN:#:{\r\nn3=  echo -a hi\r\nn4=}\r\n",
		"b.mrc":      "on *:LOAD:set %b loaded\nalias b echo -a b",
	}
	w := a.active
	for _, e := range []string{"remote.ini", "b.mrc"} {
		a.loadScript(e, true)
		a.dlg = nil
	}
	a.openScriptsTab(edRemote)
	ed := a.dlg.(*scriptEditor)
	menu := func(name string) []MenuItem {
		items, _, _ := ed.stripMenu(a, slices.Index(edMenuNames[:], name))
		return items
	}
	labels := func(items []MenuItem) []string {
		var out []string
		for _, it := range items {
			out = append(out, it.Label)
		}
		return out
	}

	// Index: a submenu for each kind of section; choosing goes there.
	idx := menu("Index")
	if got := labels(idx); !slices.Equal(got, []string{"alias", "on"}) {
		t.Fatalf("Index = %q", got)
	}
	if got := labels(idx[1].Sub); !slices.Equal(got, []string{"*:TEXT:!echotest:#:envtest", "*:JOIN:#:"}) {
		t.Errorf("Index > on = %q", got)
	}
	idx[1].Sub[1].Action(a)
	if ed.doc().row != 2 {
		t.Errorf("Index went to line %d", ed.doc().row+1)
	}

	// Check Brackets finds the { that's never closed.
	ed.doc().moveTo(2, 13, false)
	ed.typed(a, []rune(" {"))
	ed.key(a, "ctrl+h")
	if m, ok := a.dlg.(*msgBox); !ok || m.Lines[0] != "The brackets don't match: line 3." {
		t.Fatalf("Check Brackets: %#v", a.dlg)
	}
	a.dlg.key(a, "enter")
	if ed.doc().selText() != "on *:JOIN:#:{ {" {
		t.Errorf("selected %q", ed.doc().selText())
	}
	ed.key(a, "ctrl+z")

	// Find Selection: Find starts with what's selected.
	ed.doc().moveTo(1, 3, false)
	ed.doc().moveTo(1, 9, true)
	ed.key(a, "ctrl+f")
	if f := a.dlg.(*formBox); string(f.Fields[0].Edit.Buf) != "*:TEXT" {
		t.Errorf("Find: %q", string(f.Fields[0].Edit.Buf))
	}
	a.dlg.key(a, "esc")

	// Listen: Events off stops on TEXT; /events on turns it back on.
	menu("Listen")[1].Action(a)
	if !a.book().ScriptOpts.EventsOff || slices.Contains(labels(menu("Listen")), "") {
		t.Errorf("Events: %+v", a.book().ScriptOpts)
	}
	if menu("Listen")[1].Check {
		t.Error("Events still checked")
	}
	a.dlg = nil
	typed := func(line string) {
		a.edit(w).Buf = []rune(line)
		a.submit(w)
	}
	typed("/events on")
	typed("/raw off")
	if o := a.book().ScriptOpts; o.EventsOff || !o.RawOff {
		t.Errorf("/events on, /raw off: %+v", o)
	}
	if got := w.Lines[len(w.Lines)-1].Text; got != "* Raw is off" {
		t.Errorf("/raw off said %q", got)
	}
	typed("/raw on")
	typed("//echo -a $remote")
	if got := w.Lines[len(w.Lines)-1].Text; got != "7" {
		t.Errorf("$remote = %q", got)
	}

	// Options: Line Numbers off, kept in the config.
	a.openScriptsTab(edRemote)
	ed = a.dlg.(*scriptEditor)
	opts := menu("Options")
	if got := labels(opts); !slices.Equal(got, []string{"Initialization Warning", "Ask to Save Files", "Monitor File Changes",
		"Backup File on Save", "Find Selection", "Line Numbers", "", "Identifier Warning", "", "Default Level: 1"}) {
		t.Errorf("Options = %q", got)
	}
	opts[5].Action(a)
	if !a.book().ScriptOpts.NoLineNumbers || menu("Options")[5].Check {
		t.Error("Line Numbers didn't turn off")
	}
	opts[7].Action(a) // Identifier Warning
	a.dlg = nil
	a.scriptMem["w.mrc"] = "alias w echo -a $nosuchthing"
	typed("/load -rs w.mrc")
	typed("/w")
	if st := a.firstStatus(); !strings.Contains(st.Lines[len(st.Lines)-1].Text, "No such identifier: $nosuchthing") {
		t.Errorf("Identifier Warning: %q", st.Lines[len(st.Lines)-1].Text) // errors go to status, like mIRC's
	}
	typed("/unload -rs w.mrc")

	// Rename: the file and the loaded script, now; Order puts it first.
	a.openScriptsTab(edRemote)
	ed = a.dlg.(*scriptEditor)
	ed.cur[edRemote] = ed.findDoc(edRemote, "b.mrc")
	ed.renamePrompt(a)
	a.dlg.(*formBox).Fields[0].Edit = state.EditBox{Buf: []rune("bee.mrc")}
	a.dlg.key(a, "enter")
	if _, ok := a.scriptMem["b.mrc"]; ok || a.scriptMem["bee.mrc"] == "" || !slices.Equal(a.book().Scripts, []string{"remote.ini", "bee.mrc"}) {
		t.Errorf("Rename: %q, files %v", a.book().Scripts, slices.Sorted(maps.Keys(a.scriptMem)))
	}
	ed.openOrder(a)
	l := a.dlg.(*listBox)
	l.Sel = 1
	l.press(a, "Move Up")
	if want := []string{"bee.mrc", "remote.ini"}; !slices.Equal(a.book().Scripts, want) || !slices.Equal(a.scripts.Scripts(), append([]string{"popups.ini"}, want...)) {
		t.Errorf("Order: %q, engine %q", a.book().Scripts, a.scripts.Scripts())
	}
	l.press(a, "Close")
	if a.dlg != ed {
		t.Fatalf("Order closed to %T", a.dlg)
	}

	// More Files picks one of the tab's files.
	ed.moreFiles(a)
	l = a.dlg.(*listBox)
	l.Sel = slices.Index(l.Rows, "remote.ini")
	l.press(a, "Open")
	if a.dlg != ed || ed.doc().file != "remote.ini" {
		t.Errorf("More Files: %T %q", a.dlg, ed.doc().file)
	}

	// Monitor File Changes: a file changed elsewhere is read again when
	// the terminal has the focus back.
	a.scriptMem["remote.ini"] = "[script]\r\nn0=alias changed echo -a changed\r\n"
	a.Update(tea.FocusMsg{})
	if got := ed.doc().text(); got != "alias changed echo -a changed" {
		t.Errorf("after the change: %q", got)
	}

	// Backup File on Save keeps the old file.
	a.book().ScriptOpts.Backup = true
	ed.typed(a, []rune("; "))
	ed.key(a, "ctrl+s")
	if a.scriptMem["remote.ini.bak"] != "[script]\r\nn0=alias changed echo -a changed\r\n" {
		t.Errorf("backup = %q", a.scriptMem["remote.ini.bak"])
	}

	// Delete unloads it and deletes the file.
	ed.cur[edRemote] = ed.findDoc(edRemote, "bee.mrc")
	ed.deletePrompt(a)
	a.dlg.key(a, "enter")
	if _, ok := a.scriptMem["bee.mrc"]; ok || slices.Contains(a.book().Scripts, "bee.mrc") || ed.findDoc(edRemote, "bee.mrc") != nil {
		t.Errorf("Delete: %q", a.book().Scripts)
	}

	// View on Users is the Remote tab's files; the title says Users.
	ed.setTab(a, edUsers)
	if ed.title() != "Scripts Editor - Users" || !slices.Equal(labels(menu("View")), []string{"1 remote.ini"}) {
		t.Errorf("Users: %q, View %q", ed.title(), labels(menu("View")))
	}

	// Help: Search says what trout does with a name.
	ed.helpTopic(a, "Search...")
	a.dlg.(*formBox).Fields[0].Edit = state.EditBox{Buf: []rune("$window")}
	a.dlg.key(a, "enter")
	if m, ok := a.dlg.(*msgBox); !ok || !strings.Contains(m.Lines[0], "isn't supported yet") {
		t.Errorf("Search: %#v", a.dlg)
	}
	a.dlg.key(a, "enter")
	ed.helpTopic(a, "Identifiers")
	if l, ok := a.dlg.(*listBox); !ok || !slices.Contains(l.Rows, "$nick") {
		t.Errorf("Identifiers: %#v", a.dlg)
	}
	a.dlg.key(a, "esc")
	if a.dlg != ed {
		t.Errorf("Identifiers closed to %T", a.dlg)
	}

	// Initialization Warning off: a new script's on LOAD runs without
	// asking.
	a.dlg = nil
	a.book().ScriptOpts.NoInitWarning = true
	a.scriptMem["c.mrc"] = "on *:LOAD:set %c loaded"
	typed("/load -rs c.mrc")
	if a.dlg != nil || a.scripts.Vars()["c"] != "loaded" {
		t.Errorf("no warning: %T, %%c = %q", a.dlg, a.scripts.Vars()["c"])
	}
}

// TestMenuClickStaysOpen: a click on a menubar, the main one or the
// Scripts Editor's, leaves its menu open (letting go doesn't close it);
// dragging to an item and letting go chooses it.
func TestMenuClickStaysOpen(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	click := func(x, y int) {
		a.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		a.Update(tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	}
	find := func(kind HitKind, s string, i int) Region {
		t.Helper()
		a.View()
		for _, r := range a.regions {
			if r.Kind == kind && r.S == s && r.I == i {
				return r
			}
		}
		t.Fatalf("no region %v %q %d", kind, s, i)
		return Region{}
	}
	r := find(HitMenuBar, "", 0)
	click(r.X+1, r.Y)
	if len(a.menus) != 1 {
		t.Fatalf("main File menu: %d menus open", len(a.menus))
	}
	a.closeMenus()

	a.openScriptsTab(edRemote)
	r = find(HitDlg, "strip", 5) // Options
	click(r.X+1, r.Y)
	if len(a.menus) != 1 || !a.dlgMenu {
		t.Fatalf("Options menu: %d menus open", len(a.menus))
	}
	m := a.menus[0]
	click(m.X+3, m.Y+6) // Line Numbers
	if len(a.menus) != 0 || !a.book().ScriptOpts.NoLineNumbers {
		t.Errorf("click on Line Numbers: menus %d, off %v", len(a.menus), a.book().ScriptOpts.NoLineNumbers)
	}

	// Press on the bar, drag to the item, let go.
	a.Update(tea.MouseMsg{X: r.X + 1, Y: r.Y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = a.menus[0]
	a.Update(tea.MouseMsg{X: m.X + 3, Y: m.Y + 6, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	a.Update(tea.MouseMsg{X: m.X + 3, Y: m.Y + 6, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if len(a.menus) != 0 || a.book().ScriptOpts.NoLineNumbers {
		t.Errorf("drag to Line Numbers: menus %d, off %v", len(a.menus), a.book().ScriptOpts.NoLineNumbers)
	}
}
