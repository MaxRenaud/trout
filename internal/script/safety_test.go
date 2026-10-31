package script

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

// These are the ways a script, or someone else's text, could crash or
// freeze the client, or run code it shouldn't.

// dispatchText sends an on TEXT event from bob on #trout and returns what
// the scripts did, as journey lines.
func dispatchText(e *Engine, h *fakeHost, text string) []string {
	actions, _ := e.Dispatch(Event{Kind: "TEXT", Network: "TestNet", Nick: "bob", Address: "b@h",
		Chan: "#trout", Text: text}, h)
	var out []string
	for _, a := range actions {
		switch {
		case a.Echo != nil:
			out = append(out, a.Echo.Window+": "+a.Echo.Text)
		case a.Command != "":
			out = append(out, a.Command)
		}
	}
	return out
}

func load(t *testing.T, e *Engine, name, src string) {
	t.Helper()
	for _, p := range e.Load(name, src) {
		if p.Fatal {
			t.Fatalf("%s: %v", name, p)
		}
	}
}

// TestRandHuge: $rand with numbers from someone else, however big, doesn't
// panic.
func TestRandHuge(t *testing.T) {
	e := New(Options{})
	load(t, e, "dice.mrc", `on *:TEXT:!roll *:#:msg # $rand($2,$3)`)
	for _, text := range []string{
		"!roll -99999999999999999999 99999999999999999999",
		"!roll 1 99999999999999999999",
		"!roll 5 5",
	} {
		got := dispatchText(e, evalHost(), text)
		if len(got) != 1 || !strings.HasPrefix(got[0], "/msg #trout ") {
			t.Errorf("%s: %q", text, got)
		}
	}
}

// TestBareIdentifiers: identifiers used without their arguments are
// errors, not panics.
func TestBareIdentifiers(t *testing.T) {
	for name := range builtins {
		e := New(Options{})
		load(t, e, "t.mrc", "alias t noop $"+name)
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("$%s: panic: %v", name, p)
				}
			}()
			e.Alias("TestNet", "#trout", "t", "", evalHost())
		}()
	}
}

// TestComputedCommandInjection: a computed command that's one of the
// engine's own doesn't evaluate what someone said a second time.
func TestComputedCommandInjection(t *testing.T) {
	e := New(Options{})
	load(t, e, "loud.mrc", `on *:TEXT:*:#:$iif(1,echo,notice) -a $1-
alias secret return LEAKED`)
	got := dispatchText(e, evalHost(), "$secret and [ $secret ] and %x")
	if want := []string{"#trout: $secret and [ $secret ] and %x"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	e = New(Options{})
	// The whole line is evaluated first, so the variable's name is built:
	// "%said" alone would be its value.
	load(t, e, "set.mrc", `on *:TEXT:*:#:$iif(1,set,x) % $+ said $1-
alias secret return LEAKED`)
	dispatchText(e, evalHost(), "[ $secret ]")
	if got := e.Vars()["said"]; got != "[ $secret ]" {
		t.Errorf("%%said = %q", got)
	}
}

// TestEvalBudget: $eval(x,N) with a huge N is stopped by the step budget,
// quickly.
func TestEvalBudget(t *testing.T) {
	start := time.Now()
	got := runAlias(t, `alias t echo -a $eval(%x,999999999)`, "")
	if len(got) != 1 || !strings.Contains(got[0], "script stopped") {
		t.Errorf("got %q", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v", d)
	}
}

// TestBudgetPerScript: a script that never ends doesn't stop another
// script's handler for the same event.
func TestBudgetPerScript(t *testing.T) {
	e := New(Options{MaxSteps: 1000})
	load(t, e, "a.mrc", `on *:TEXT:*:#:while (1) noop`)
	load(t, e, "b.mrc", `on *:TEXT:*:#:msg # still here`)
	got := dispatchText(e, evalHost(), "hi")
	want := []string{": * script stopped: it ran more than 1000 steps (line 1, a.mrc)", "/msg #trout still here"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRegsubWholeMatch: in $regsub's subtext, \0 is the whole match.
func TestRegsubWholeMatch(t *testing.T) {
	got := runAlias(t, `alias t noop $regsub(hello,/(l)/,[\0],%r) | echo -a %r`, "")
	if want := []string{"#trout: he[l]lo"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestPopupLabelRun: building a popup is a run like any other: -u0
// variables are unset after, and errors are shown.
func TestPopupLabelRun(t *testing.T) {
	e := New(Options{})
	load(t, e, "p.mrc", `menu channel {
  $bar:echo bar
  $hash(x,y):echo hash
}
alias bar {
  set -u0 %tmp 1
  return Bar
}`)
	items, actions := e.Popups("channel", "TestNet", "#trout", nil, evalHost())
	if len(items) != 1 || items[0].Label != "Bar" {
		t.Errorf("items = %+v", items)
	}
	if _, ok := e.Vars()["tmp"]; ok {
		t.Error("tmp is still set")
	}
	if !slices.ContainsFunc(actions, func(a Action) bool {
		return a.Echo != nil && strings.Contains(a.Echo.Text, "$hash isn't supported yet")
	}) {
		t.Errorf("no error shown: %+v", actions)
	}
}

// TestTempStaysTemp: set -e variables stay unsaved when they change.
func TestTempStaysTemp(t *testing.T) {
	e := New(Options{})
	load(t, e, "t.mrc", `alias t {
  set -e %sess 0
  inc %sess
  %sess = %sess + 1
  set %kept 1
}`)
	actions, _ := e.Alias("TestNet", "#trout", "t", "", evalHost())
	for _, a := range actions {
		if a.Save != nil {
			if _, ok := a.Save.Vars["sess"]; ok {
				t.Errorf("saved %%sess: %v", a.Save.Vars)
			}
			if a.Save.Vars["kept"] != "1" {
				t.Errorf("didn't save %%kept: %v", a.Save.Vars)
			}
		}
	}
}

// TestFilesAcrossRuns: a run reads back what it wrote; later runs read
// the client's file, which the engine doesn't keep a copy of.
func TestFilesAcrossRuns(t *testing.T) {
	e := New(Options{})
	load(t, e, "log.mrc", `alias w write log.txt $1-
alias both {
  write log.txt c
  echo -a $lines(log.txt) $read(log.txt,n,3)
}`)
	h := evalHost()
	run := func(name, args string) []string {
		actions, _ := e.Alias("TestNet", "#trout", name, args, h)
		var out []string
		for _, a := range actions {
			switch {
			case a.File != nil:
				h.writeFile(a.File)
			case a.Echo != nil:
				out = append(out, a.Echo.Text)
			}
		}
		return out
	}
	run("w", "a")
	run("w", "b")
	if got := run("both", ""); !slices.Equal(got, []string{"3 c"}) {
		t.Errorf("got %q", got)
	}
	if h.files["log.txt"] != "a\r\nb\r\nc\r\n" {
		t.Errorf("file = %q", h.files["log.txt"])
	}
}

// TestRegexCache: a regex event's pattern is compiled once, not for every
// message.
func TestRegexCache(t *testing.T) {
	e := New(Options{})
	load(t, e, "r.mrc", `on $*:TEXT:/^!hi (\S+)/i:#:msg # hi $regml(1)`)
	for range 50 {
		dispatchText(e, evalHost(), "!hi there")
	}
	if len(e.regexes) != 1 {
		t.Errorf("%d compiled patterns", len(e.regexes))
	}
	if got := dispatchText(e, evalHost(), "!HI you"); !slices.Equal(got, []string{"/msg #trout hi you"}) {
		t.Errorf("got %q", got)
	}
}

// TestReviewThree covers the third review's findings.
func TestReviewThree(t *testing.T) {
	t.Run("scid -1 is the active connection", func(t *testing.T) {
		got := runAlias(t, `alias t scid -1 echo -a on $!network`, "")
		if want := []string{"#trout: on TestNet"}; !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("a write after a remove starts afresh", func(t *testing.T) {
		e := New(Options{})
		load(t, e, "f.mrc", `alias t remove log.txt | write log.txt NEW | echo -a $read(log.txt,n,1) $lines(log.txt)`)
		h := evalHost()
		h.files = map[string]string{"log.txt": "OLD\r\n"}
		actions, _ := e.Alias("TestNet", "#trout", "t", "", h)
		if got := echoes(actions); !slices.Equal(got, []string{"NEW 1"}) {
			t.Errorf("got %q", got)
		}
	})
	t.Run("q is a quiet where it isn't a prefix", func(t *testing.T) {
		e := New(Options{})
		load(t, e, "m.mrc", `on *:OWNER:#:echo -a owner $opnick
on *:MODE:#:echo -a mode $1-`)
		h := evalHost()
		h.info = NetInfo{ISupport: map[string]string{"PREFIX": "(ov)@+", "CHANMODES": "bq,k,l,imnpst"}}
		ev := Event{Kind: "RAWMODE", Network: "TestNet", Nick: "alice", Address: "a@h", Chan: "#trout",
			Text: "+q *!*@spam", Params: []string{"#trout", "+q", "*!*@spam"}}
		actions, _ := e.Dispatch(ev, h)
		if got := echoes(actions); !slices.Equal(got, []string{"mode +q *!*@spam"}) {
			t.Errorf("Libera: got %q", got)
		}
		h.info.ISupport["PREFIX"] = "(qov)~@+"
		actions, _ = e.Dispatch(ev, h)
		if got := echoes(actions); !slices.Equal(got, []string{"owner *!*@spam"}) {
			t.Errorf("with owners: got %q", got)
		}
	})
	t.Run("a missing default level is 1, and START isn't gated", func(t *testing.T) {
		e := New(Options{})
		load(t, e, "l.mrc", `on 1:TEXT:*:#:msg # heard
on 1:START:echo -a started`)
		e.RestoreUsers(nil, 0)
		if got := dispatchText(e, evalHost(), "hi"); !slices.Equal(got, []string{"/msg #trout heard"}) {
			t.Errorf("got %q", got)
		}
		e.RestoreUsers(nil, 5)
		if got := echoes(e.Init("l.mrc", true, evalHost())); !slices.Equal(got, []string{"started"}) {
			t.Errorf("START: got %q", got)
		}
	})
	t.Run("renaming to another case keeps the file", func(t *testing.T) {
		e := New(Options{})
		load(t, e, "r.mrc", `alias t rename -o a.txt A.txt | echo -a $isfile(A.txt) $read(A.txt,n,1)`)
		h := evalHost()
		h.files = map[string]string{"a.txt": "data\r\n"}
		actions, _ := e.Alias("TestNet", "#trout", "t", "", h)
		if got := echoes(actions); !slices.Equal(got, []string{"$true data"}) {
			t.Errorf("got %q", got)
		}
	})
	t.Run("ruser levels nick", func(t *testing.T) {
		got := runAlias(t, `alias t auser friend,5 bob | auser 5 friend | ruser friend bob | echo -a $level(bob) $level(friend)`, "")
		if want := []string{"#trout: =5 5"}; !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("server notices", func(t *testing.T) {
		for _, c := range []struct {
			line string
			own  bool
			want string
		}{
			{":irc.example NOTICE * :hi", false, "SNOTICE"},
			{"NOTICE AUTH :*** Looking up your hostname", false, "SNOTICE"},
			{"NOTICE bob :mine", true, "NOTICE"},
			{":bob NOTICE trout :no address", false, "NOTICE"},
		} {
			m, _ := ircmsg.ParseLine(c.line)
			if got := EventFromMessage("TestNet", m, c.own).Kind; got != c.want {
				t.Errorf("%s: %s, want %s", c.line, got, c.want)
			}
		}
	})
	t.Run("channel windows aren't queries", func(t *testing.T) {
		if windowLoc("?", "#trout") || !windowLoc("?", "bob") || !windowLoc("*", "#trout") || !windowLoc("=", "=bob") {
			t.Error("wrong location")
		}
	})
	t.Run("rewrites of a file are one action", func(t *testing.T) {
		e := New(Options{})
		load(t, e, "w.mrc", `alias t {
  fopen -o h out.txt
  var %i = 1
  while (%i <= 50) { fwrite -n h line %i | inc %i }
  fclose h
}`)
		actions, _ := e.Alias("TestNet", "#trout", "t", "", evalHost())
		var writes []*File
		for _, a := range actions {
			if a.File != nil {
				writes = append(writes, a.File)
			}
		}
		if len(writes) != 1 || strings.Count(writes[0].Data, "\n") != 50 {
			t.Errorf("%d writes", len(writes))
		}
	})
}

// echoes is the text of the Echo actions.
func echoes(actions []Action) []string {
	var out []string
	for _, a := range actions {
		if a.Echo != nil {
			out = append(out, a.Echo.Text)
		}
	}
	return out
}

// TestPopupsIni: a popups.ini, mIRC's file for all five popups, loads each
// section as its popup; Check finds problems without loading anything.
func TestPopupsIni(t *testing.T) {
	e := New(Options{})
	src := "[mpopup]\r\nn0=Server\r\nn1=.Lusers:/lusers\r\n[lpopup]\r\nn0=Whois:/whois $$1\r\n[xpopup]\r\nn0=Odd:/echo odd\r\n"
	if ps := e.Check("popups.ini", src, "popups"); len(ps) != 1 || ps[0].Line != 6 {
		t.Errorf("Check: %v", ps)
	}
	if items, _ := e.Popups("status", "TestNet", "", nil, evalHost()); len(items) != 0 {
		t.Errorf("Check loaded something: %+v", items)
	}
	e.LoadAs("popups.ini", src, "popups")
	status, _ := e.Popups("status", "TestNet", "", nil, evalHost())
	if len(status) != 1 || status[0].Label != "Server" || len(status[0].Items) != 1 || status[0].Items[0].Label != "Lusers" {
		t.Errorf("status = %+v", status)
	}
	nicks, _ := e.Popups("nicklist", "TestNet", "#trout", []string{"bob"}, evalHost())
	if len(nicks) != 1 || nicks[0].Label != "Whois" {
		t.Errorf("nicklist = %+v", nicks)
	}
	if chans, _ := e.Popups("channel", "TestNet", "#trout", nil, evalHost()); len(chans) != 0 {
		t.Errorf("channel = %+v", chans)
	}
}

// TestSetVars: the Variables tab replaces the variables, but leaves the
// ones that go when trout quits.
func TestSetVars(t *testing.T) {
	e := New(Options{})
	e.Restore(map[string]string{"a": "1", "b": "2"})
	load(t, e, "t.mrc", "alias t set -e %sess 1")
	e.Command("TestNet", "", "t", false, evalHost())
	e.SetVars(map[string]string{"b": "3", "c": "4"})
	if got := e.Vars(); !maps.Equal(got, map[string]string{"b": "3", "c": "4"}) {
		t.Errorf("vars = %v", got)
	}
	if _, ok := e.vars["sess"]; !ok {
		t.Error("the -e variable went")
	}
}

// TestIniLineNumbers: an ini-format script's lines are numbered as the
// Scripts Editor shows them, n0= being line 1, like mIRC's.
func TestIniLineNumbers(t *testing.T) {
	e := New(Options{})
	ps := e.Check("remote.ini", "[script]\r\nn0=alias x echo -a x\r\nn1=on *:FOO:#:echo\r\n", "")
	if len(ps) != 1 || ps[0].Line != 2 {
		t.Errorf("problems = %v", ps)
	}
}

// TestListen: /ctcps, /events and /raw off stop their kind of event, and
// $remote says which are on.
func TestListen(t *testing.T) {
	e := New(Options{})
	load(t, e, "l.mrc", "on *:TEXT:*:#:echo -a text\nctcp *:PING:*:echo -a ping\nraw 001:*:echo -a raw\nalias listening return $remote")
	fired := func(ev Event) bool {
		actions, _ := e.Dispatch(ev, evalHost())
		return slices.ContainsFunc(actions, func(a Action) bool { return a.Echo != nil })
	}
	text := Event{Kind: "TEXT", Network: "TestNet", Nick: "bob", Chan: "#trout", Text: "hi"}
	ping := Event{Kind: "CTCP", Network: "TestNet", Nick: "bob", Text: "PING 1"}
	raw := Event{Kind: "RAW", Network: "TestNet", Command: "001", Params: []string{"me", "welcome"}}
	remote := func() string {
		actions, _ := e.Command("TestNet", "", "echo -a $listening", true, evalHost())
		return actions[0].Echo.Text
	}
	if !fired(text) || !fired(ping) || !fired(raw) || remote() != "7" {
		t.Fatalf("not all on to start with: text %v ping %v raw %v $remote %s", fired(text), fired(ping), fired(raw), remote())
	}
	e.Listen(true, false, true)
	if fired(text) || !fired(ping) || !fired(raw) || remote() != "5" {
		t.Errorf("events off: text %v ping %v raw %v $remote %s", fired(text), fired(ping), fired(raw), remote())
	}
	e.Listen(false, true, false)
	if !fired(text) || fired(ping) || fired(raw) || remote() != "2" {
		t.Errorf("ctcps and raw off: text %v ping %v raw %v $remote %s", fired(text), fired(ping), fired(raw), remote())
	}
}

// TestIdentifierWarning: with it on, an identifier that doesn't exist
// stops the script with an error instead of being $null.
func TestIdentifierWarning(t *testing.T) {
	e := New(Options{})
	load(t, e, "w.mrc", "alias w echo -a x $+ $nosuch $+ y")
	echo := func() string {
		actions, _ := e.Command("TestNet", "", "w", false, evalHost())
		return actions[0].Echo.Text
	}
	if got := echo(); got != "xy" {
		t.Errorf("off: %q", got)
	}
	e.IdentifierWarning(true)
	if got := echo(); !strings.Contains(got, "No such identifier: $nosuch") {
		t.Errorf("on: %q", got)
	}
	// Typed in the editbox, it stays as typed, warning or not (checked in
	// mIRC 7.86); the aliases the line calls are scripts.
	for _, warn := range []bool{true, false} {
		e.IdentifierWarning(warn)
		actions, _ := e.Command("TestNet", "", "echo -a x $nosuch y $nosuch(1,2) $len(ab)", true, evalHost())
		if got := actions[0].Echo.Text; got != "x $nosuch y $nosuch(1,2) 2" {
			t.Errorf("typed, warning %v: %q", warn, got)
		}
	}
}

// TestRenameOrder: File > Rename and Order in the Scripts Editor.
func TestRenameOrder(t *testing.T) {
	e := New(Options{})
	for _, n := range []string{"a.mrc", "b.mrc", "c.mrc"} {
		load(t, e, n, "on *:TEXT:*:#:echo -a "+n)
	}
	e.Rename("b.mrc", "bee.mrc")
	e.Order([]string{"c.mrc", "bee.mrc"})
	if got, want := e.Scripts(), []string{"c.mrc", "bee.mrc", "a.mrc"}; !slices.Equal(got, want) {
		t.Errorf("scripts = %q, want %q", got, want)
	}
	actions, _ := e.Dispatch(Event{Kind: "TEXT", Network: "TestNet", Nick: "bob", Chan: "#trout", Text: "x"}, evalHost())
	if len(actions) != 3 || actions[0].Echo.Text != "c.mrc" {
		t.Errorf("actions = %+v", actions)
	}
}

// TestLookup: Help > Search in the Scripts Editor.
func TestLookup(t *testing.T) {
	for name, want := range map[string]string{
		"$nick":    "$nick works in trout.",
		"$window":  "isn't supported yet",
		"$dll":     "DLLs aren't supported",
		"$frobble": "isn't an identifier",
		"/hadd":    "a scripting command",
		"aline":    "custom windows",
		"msg":      "as if you typed it",
	} {
		if got := Lookup(name); !strings.Contains(got, want) {
			t.Errorf("%s: %q", name, got)
		}
	}
	if !slices.Contains(Identifiers(), "nick") || !slices.Contains(Commands(), "hadd") {
		t.Error("lists")
	}
}
