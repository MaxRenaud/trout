package script

import (
	"slices"
	"strings"
	"testing"
	"time"
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
