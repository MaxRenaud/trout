package script

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

// evalHost is a fake Host for the evaluator's tests: we're trout on
// #trout, with ops and voices.
func evalHost() *fakeHost {
	return &fakeHost{me: "trout", net: "TestNet", active: "#trout",
		chans: map[string][]Nick{"#trout": {{"trout", "@"}, {"alice", "@"}, {"bob", "+"}, {"carol", ""}}},
		now:   time.Date(2026, 10, 29, 15, 4, 5, 0, time.UTC)}
}

// evalIn evaluates text in an alias called with params, on #trout, with
// the variables set.
func evalIn(t *testing.T, e *Engine, text string, params ...string) string {
	t.Helper()
	r := e.newRun(evalHost())
	fr := &frame{network: "TestNet", window: "#trout", channel: "#trout", params: params}
	v, err := r.eval(fr, text)
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return v
}

// evalVars are the variables evalCases see, and evalParams their $1-.
var (
	evalVars = map[string]string{"x": "%y", "y": "Hiya!", "a.bob": "seen", "a.two": "seen too",
		"colored": "\x02bold\x02 \x034red\x03 \x0304,05x \x1funder", "n": "5", "sp": "a b"}
	evalParams = []string{"one", "two", "three", "four"}
)

// evalCases are lines and what they evaluate to, in an alias on #trout.
// TestMIRC (-tags mirc) checks them against real mIRC.
var evalCases = []struct{ in, want string }{
	{"plain  text   here", "plain text here"},
	{"$1 and $2", "one and two"},
	{"$2-", "two three four"},
	{"$2-3", "two three"},
	{"$0", "4"},
	{"$9", ""},
	{"$1's joke", "one's joke"},
	{"Hel $+ lo th $+ ere $+ !", "Hello there!"},
	{"a $null b", "a b"},
	{"#", "#trout"},
	{"#$1", "#one"},
	{"%n apples", "5 apples"},
	{"%nope", ""},
	{"%sp", "a b"},
	// Evaluation brackets, from mIRC's Aliases help: one evaluation,
	// then a second with another bracket.
	{"[ %x ]", "%y"},
	{"[ [ %x ] ]", "Hiya!"},
	{"%x", "%y"},
	{"% [ $+ [ $1 ] ]", ""},
	{"%a. [ $+ bob ]", "seen"},
	{"%a. [ $+ [ $2 ] ]", "seen too"},
	{"$nick", ""},
	{"$calc(1 + 2 * 3)", "7"},
	{"$calc((1 + 2) * 3)", "9"},
	{"$calc(2 ^ 3 ^ 2)", "64"}, // left to right, checked in mIRC
	{"$calc(10 / 4)", "2.5"},
	{"$calc(10 / 3)", "3.333333"},
	{"$calc(7 % 3)", "1"},
	{"$calc(1 / 0)", "0"},
	{"$calc(-2 + %n)", "3"},
	{"$len(#trout)", "6"},
	{"$left(goodbye,4) $left(goodbye,-3)", "good good"},
	{"$right(othello,5) $right(othello,-2)", "hello hello"},
	{"$mid(othello,3,4) $mid(othello,3) $mid(othello,-4)", "hell hello ello"},
	{"$upper(hello) $lower(HELLO)", "HELLO hello"},
	{"$replace(abcdefg,cd,xyz)", "abxyzefg"},
	{"$replace(abcdefg,a,A,b,B,c,C,d,D)", "ABCDefg"},
	{"$replace(Hello,L,_)", "He__o"},
	{"$replacecs(Hello,L,_)", "Hello"},
	{"$remove(abcdefg,cd)", "abefg"},
	{"$remove(abcdefg,a,c,e,g)", "bdf"},
	{"$str(ho,3)", "hohoho"},
	{"$pos(hello there,e,1) $pos(hello there,e,2) $pos(hello there,e,0)", "2 9 3"},
	{"$pos(hello there,a,1)", ""},
	{"$count(hello,el) $count(hello,l)", "1 2"},
	{"$chr(65) $asc(A)", "A 65"},
	{"$strip(%colored)", "bold red x under"},
	{"$gettok(a.b.c.d.e,3,46)", "c"},
	{"$gettok(a.b.c.d.e,9,46)", ""},
	{"$gettok(a.b.c.d.e,2-,46)", "b.c.d.e"},
	{"$gettok(a.b.c.d.e,2-4,46)", "b.c.d"},
	{"$gettok(a.b.c.d.e,-1,46)", "e"},
	{"$gettok(a.b.c.d.e,0,46)", "5"},
	{"$gettok(a..b,2,46)", "b"},
	{"$numtok(a b c,32)", "3"},
	{"$addtok(a.b.c,d,46) $addtok(a.b.c.d,c,46)", "a.b.c.d a.b.c.d"},
	{"$deltok(a.b.c.d,3,46) $deltok(a.b.c.d,2-3,46)", "a.b.d a.d"},
	{"$findtok(a.b.c.d,c,1,46)", "3"},
	{"$findtok(a.b.c.d,e,1,46)", ""},
	{"$instok(a.b.d,c,3,46) $instok(a.b.d,c,9,46)", "a.b.c.d a.b.d.c"},
	{"$istok(a b c,B,32) $istokcs(a b c,B,32)", "$true $false"},
	{"$matchtok(one two three, e, 0, 32) $matchtok(one two three, e, 2, 32)", "2 three"},
	{"$wildtok(one two three, t*, 0, 32) $wildtok(one two three, t*e, 1, 32)", "2 three"},
	{"$puttok(a.b.c.d,e,2,46)", "a.e.c.d"},
	{"$remtok(a.b.c.d,b,1,46) $remtok(a.b.c.d,e,1,46) $remtok(a.c.c.d,c,1,46)", "a.c.d a.b.c.d a.c.d"},
	{"$remtok(a.c.c.d,c,0,46)", "a.d"},
	{"$reptok(a.b.c.d,b,e,1,46) $reptok(a.b.c.d,f,e,1,46) $reptok(a.b.a.c,a,e,2,46)", "a.e.c.d a.b.c.d a.b.e.c"},
	{"$sorttok(e.d.c.b.a,46) $sorttok(1.3.5.2.4,46,nr)", "a.b.c.d.e 5.4.3.2.1"},
	{"$int(3.14159) $round(3.14159,2) $abs(-1)", "3 3.14 1"},
	{"$iif(1 == 2, yes, no) $iif(1 == 2, yes) $iif(%n > 2,big,small)", "no big"},
	{"$iif(%nope,set,unset)", "unset"},
	{"$eval(%x,0) $eval(%x,1) $eval(%x,2) $(%x,2)", "%x %y Hiya! Hiya!"},
	{"$+(a,b,c)", "abc"},
	{"$me on $network", "trout on TestNet"},
	{"$nick(#trout,0) $nick(#trout,2) $nick(#trout,bob) $nick(#trout,0,o) $nick(#trout,1,v)", "4 alice 3 2 bob"},
	{"$time $date $adate", "15:04:05 29/10/2026 10/29/2026"},
	{"$asctime(0,yyyy-mm-dd HH:nn:ss)", "1970-01-01 00:00:00"},
	{"$asctime(dddd d mmmm yy h:nn TT doo)", "Thursday 29 October 26 3:04 PM 29th"},
	{"$fulldate", "Thu Oct 29 15:04:05 2026"},
	{"$ctime", "1793286245"},
	{"$duration(0) $duration(1) $duration(3700) $duration(694861)", "0secs 1sec 1hr 1min 40secs 1wk 1day 1hr 1min 1sec"},
	{"$duration(3700,2) $duration(3700,3)", "1hr 1min 01:01:40"},
	{"$rand(5,5) $rand(b,b)", "5 b"},
}

func TestEval(t *testing.T) {
	e := New(Options{Rand: rand.New(rand.NewPCG(1, 2))})
	e.Restore(evalVars)
	for _, c := range evalCases {
		if got := evalIn(t, e, c.in, evalParams...); got != c.want {
			t.Errorf("%s = %q, want %q", c.in, got, c.want)
		}
	}
}

// condVars are the variables condCases see.
var condVars = map[string]string{"n": "5", "zero": "0", "f": "$false", "text": "how now brown cow"}

// condCases are if conditions and whether they're true, on #trout.
// TestMIRC (-tags mirc) checks the ones that don't need a nicklist
// against real mIRC.
var condCases = []struct {
	in   string
	want bool
}{
	{"(1 == 1)", true},
	{"(1 == 2)", false},
	{"(01 == 1)", true},
	{"(abc == ABC)", true},
	{"(abc === ABC)", false},
	{"(1 != 2)", true},
	{"(%n > 3)", true},
	{"(%n >= 5) && (%n <= 5)", true},
	{"((%n > 0) && (%n < 10))", true},
	{"(%n < 3) || (%n == 5)", true},
	{"(%n < 3) || (%n == 6)", false},
	{"(10 < 9)", false},
	{"(apple < banana)", true},
	{"(%n)", true},
	{"(%zero)", false},
	{"(%f)", false},
	{"(%nope)", false},
	{"(!%nope)", true},
	{"(!%n)", false},
	{"(%nope == $null)", true},
	{"(now isin %text)", true},
	{"(NOW isin %text)", true},
	{"(NOW isincs %text)", false},
	{"(now !isin %text)", false},
	{"(*brown* iswm %text)", true},
	{"(how & cow iswm %text)", false},
	{"(how*cow iswm %text)", true},
	{"(5 isnum)", true},
	{"(5 isnum 1-10)", true},
	{"(11 isnum 1-10)", false},
	{"(abc isnum)", false},
	{"(4 // 12)", true},
	{"(5 // 12)", false},
	{"(5 \\\\ 12)", true},
	{"(3 & 1)", true},
	{"(a isletter)", true},
	{"(ab isalnum) && (ab1 !isalpha)", true},
	{"(alice isop #trout)", true},
	{"(bob isop #trout)", false},
	{"(bob isvoice #trout)", true},
	{"(carol isreg #trout)", true},
	{"(dave ison #trout)", false},
	{"(#trout ischan)", true},
	{"($len(%text) > 10)", true},
	{"($gettok(%text,2,32) == now)", true},
	{"($calc(1 + 1) == 2)", true},
	{"%n == 5", true},
}

func TestCond(t *testing.T) {
	e := New(Options{})
	e.Restore(condVars)
	for _, c := range condCases {
		r := e.newRun(evalHost())
		fr := &frame{network: "TestNet", channel: "#trout"}
		got, err := r.cond(fr, c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
		} else if got != c.want {
			t.Errorf("%s = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestCondValues checks $v1 and $v2, from mIRC's if-then-else help.
func TestCondValues(t *testing.T) {
	r := New(Options{}).newRun(evalHost())
	fr := &frame{}
	if _, err := r.cond(fr, "(text isin sometext)"); err != nil {
		t.Fatal(err)
	}
	if fr.v1 != "text" || fr.v2 != "sometext" {
		t.Errorf("$v1 = %q, $v2 = %q", fr.v1, fr.v2)
	}
}

func TestWildMatch(t *testing.T) {
	for _, c := range []struct {
		pattern, text string
		want          bool
	}{
		{"*", "anything at all", true},
		{"hello", "hello", true},
		{"hello", "hello there", false},
		{"hello*", "hello there", true},
		{"*there", "hello there", true},
		{"*ll*", "hello there", true},
		{"h?llo", "hello", true},
		{"h?llo", "hllo", false},
		{"!seen &", "!seen bob", true},
		{"!seen &", "!seen", false},
		{"!seen &", "!seen bob alice", false},
		{"& &", "two words", true},
		{"HELLO", "hello", true},
		{"*a*b", "xaxxb", true},
		{"*a*b", "xaxxbx", false},
		{"a*b*c", "abbbc", true},
		{"*& bob", "hi bob", true},
		{"?é", "aé", true},
		{"**", "", true},
		{"&", "", false},
	} {
		if got := wildMatch(c.pattern, c.text, false); got != c.want {
			t.Errorf("%q matching %q = %v, want %v", c.pattern, c.text, got, c.want)
		}
	}
}

// runAlias loads src and runs /t, returning what it did as journey lines.
func runAlias(t *testing.T, src string, args string) []string {
	t.Helper()
	e := New(Options{})
	for _, p := range e.Load("t.mrc", src) {
		if p.Fatal {
			t.Fatalf("problem: %v", p)
		}
	}
	actions, ok := e.Alias("TestNet", "#trout", "t", args, evalHost())
	if !ok {
		t.Fatal("no alias t")
	}
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

func TestStatements(t *testing.T) {
	for _, c := range []struct {
		name, src, args string
		want            []string
	}{
		{"while", `alias t {
  var %i = 1
  while (%i <= 3) {
    echo -a %i
    inc %i
  }
}`, "", []string{"#trout: 1", "#trout: 2", "#trout: 3"}},
		{"break and continue", `alias t {
  var %i = 0
  while (1) {
    inc %i
    if (%i == 2) continue
    if (%i > 3) break
    echo -a %i
  }
}`, "", []string{"#trout: 1", "#trout: 3"}},
		{"goto", `alias t {
  if ($1 == 1) goto one
  elseif ($1 == 2) goto two
  else goto unknown
  :one
  echo -a One
  halt
  :two
  echo -a Two
  halt
  :unknown
  echo -a Unknown number!
  halt
}`, "2", []string{"#trout: Two"}},
		{"goto loop", `alias t {
  var %x = 1
  :next
  echo -a item %x
  inc %x
  if (%x < 3) goto next
}`, "", []string{"#trout: item 1", "#trout: item 2"}},
		{"goto out of a loop", `alias t {
  while (1) {
    goto done
  }
  :done
  echo -a out
}`, "", []string{"#trout: out"}},
		{"missing label", `alias t goto nowhere`, "", []string{": * /goto: 'nowhere' not found"}},
		{"one line", `alias t if ($1) { echo -a yes } | else { echo -a no } | echo -a after`, "", []string{"#trout: no", "#trout: after"}},
		{"inline if then pipe", `alias t if ($1) echo -a yes | echo -a always`, "", []string{"#trout: always"}},
		{"nested", `alias t {
  if ($1 > 0) {
    if ($1 < 5) echo -a small
    else echo -a big
  }
  else echo -a none
}`, "7", []string{"#trout: big"}},
		{"else if", `alias t {
  if ($1 == a) echo -a A
  else if ($1 == b) echo -a B
  else echo -a other
}`, "b", []string{"#trout: B"}},
		{"var list", `alias t {
  var %a = 1, %b, %c = $calc(1 + 1), %d = $gettok(x y,2,32)
  echo -a %a %b %c %d
}`, "", []string{"#trout: 1 2 y"}},
		{"locals stay local", `alias t {
  var %v = local
  u
  echo -a %v
}
alias u echo -a in u: %v`, "", []string{"#trout: in u:", "#trout: local"}},
		{"set math", `alias t {
  set %m 2 + 3
  set -n %raw 2 + 3
  %eq = %m * 2
  echo -a %m %raw %eq
  unset %m %raw %eq
}`, "", []string{"#trout: 5 2 + 3 10"}},
		{"dynamic names", `alias t {
  set %a [ $+ b ] 1
  set %a [ $+ c ] 2
  echo -a ab = %ab ac = %ac
  unset %a*
  echo -a gone: %ab %ac .
}`, "", []string{"#trout: ab = 1 ac = 2", "#trout: gone: ."}},
		{"computed command", `alias t $iif($1 == loud,msg,notice) bob hi`, "loud", []string{"/msg bob hi"}},
		{"quiet", `alias t .ignore bob`, "", []string{"/.ignore bob"}},
		{"tokenize", `alias t {
  tokenize 44 a,b,,c
  echo -a $0 $2 $3
}`, "", []string{"#trout: 3 b c"}},
		{"return value", `alias t {
  double 4
  echo -a $result
}
alias double return $calc($1 * 2)`, "", []string{"#trout: 8"}},
		{"isid", `alias t echo -a $f $f(1) | f
alias f {
  if ($isid) return id
  echo -a command
}`, "", []string{"#trout: id id", "#trout: command"}},
		{"halt stops callers", `alias t {
  f
  echo -a not reached
}
alias f halt`, "", nil},
		{"echo windows", `alias t {
  echo -s status
  echo #other in other
  echo 4 -a red
  echo -a 4 is text
  echo just text
}`, "", []string{": status", "#other: in other", "#trout: red", "#trout: 4 is text", "#trout: just text"}},
		{"comments", `/* a comment
that goes on */
; another
alias t {
  ; inside
  echo -a hi
}`, "", []string{"#trout: hi"}},
		{"continued lines", `alias t {
  echo -a This is an example of a long $&
    line that has been split
}`, "", []string{"#trout: This is an example of a long line that has been split"}},
		// Checked in mIRC: an identifier that doesn't exist is $null, and the name
		// runs to the end of the word.
		{"unknown identifier", `alias t echo -a x $nosuch y`, "", []string{"#trout: x y"}},
		{"text after a name", `alias t echo -a $1! $1's $me! $me's $len(ab)x`, "a b", []string{"#trout: a! a's 2"}},
		{"mIRC's but not trout's", `alias t echo -a $readini(x.ini,a,b)`, "", []string{": * $readini isn't supported yet (line 1, t.mrc)"}},
		{"nothing to echo", `alias t echo -a $null`, "", []string{": * /echo: insufficient parameters (line 1, t.mrc)"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := runAlias(t, c.src, c.args)
			if !slices.Equal(got, c.want) {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// TestGroups turns a #group's events on and off.
func TestGroups(t *testing.T) {
	e := New(Options{})
	if p := e.Load("g.mrc", `#greet off
on *:JOIN:#:msg # hi $nick
#greet end
alias on enable #gr*
alias off disable #greet`); len(p) > 0 {
		t.Fatal(p)
	}
	h := evalHost()
	join := Event{Kind: "JOIN", Network: "TestNet", Nick: "bob", Chan: "#trout"}
	count := func() int { a, _ := e.Dispatch(join, h); return len(a) }
	if n := count(); n != 0 {
		t.Errorf("off: %d actions", n)
	}
	e.Alias("TestNet", "#trout", "on", "", h)
	if n := count(); n != 1 {
		t.Errorf("on: %d actions", n)
	}
	e.Alias("TestNet", "#trout", "off", "", h)
	if n := count(); n != 0 {
		t.Errorf("off again: %d actions", n)
	}
}

// TestOneHandlerPerScript: a script runs one handler per event, the first
// matching one, like mIRC.
func TestOneHandlerPerScript(t *testing.T) {
	e := New(Options{})
	src := `on *:TEXT:hello*:#:msg # first
on *:TEXT:*:#:msg # second`
	e.Load("a.mrc", src)
	e.Load("b.mrc", strings.ReplaceAll(src, "first", "third"))
	actions, _ := e.Dispatch(Event{Kind: "TEXT", Network: "TestNet", Nick: "bob", Chan: "#trout", Text: "hello there"}, evalHost())
	var got []string
	for _, a := range actions {
		got = append(got, a.Command)
	}
	if want := []string{"/msg #trout first", "/msg #trout third"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseProblems(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"alias t {\n echo hi\n", "line 1: this { has no matching }"},
		{"on *:JOIN:#:{ echo hi } }", ""},
		{"on *:FOO:#:echo", "line 1: on FOO isn't supported"},
		{"on friend:TEXT:*:#:echo", "line 1: the named level friend needs the Users list, which trout doesn't have; this event never triggers"},
		{"on >=2:KICK:#:echo", "line 1: comparing levels needs the Users list, which trout doesn't have"},
		{"on $*:TEXT:/^hi/:#:echo", ""},
		{"raw 001:*:echo", ""},
		{"raw 001:echo", "line 1: a raw event needs numeric:matchtext:commands"},
		{"#g on\nalias x echo", "#g has no #g end"},
		{"hello world", "line 1: not an alias, on or ctcp definition: hello world"},
		{"alias x run notepad.exe", "line 1: /run: running programs isn't supported"},
		{"[script]\nn0=alias x echo -a ini\nn1=on *:JOIN:#:echo hi", ""},
	} {
		var got []string
		for _, p := range New(Options{}).Load("t.mrc", c.src) {
			got = append(got, p.String())
		}
		if strings.Join(got, "; ") != c.want {
			t.Errorf("%q: got %q, want %q", c.src, got, c.want)
		}
	}
}

// TestWildMatchLongLine: a pattern with many * against a long line that
// almost matches finishes quickly; a recursive matcher took ages.
func TestWildMatchLongLine(t *testing.T) {
	line := strings.Repeat("a", 5000)
	start := time.Now()
	if wildMatch("*a*a*a*a*a*a*b", line, false) {
		t.Error("matched")
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("took %v", d)
	}
}

// TestHashNumericNames: hash commands take names, even numbers; only $hget
// and $hfind take N.
func TestHashNumericNames(t *testing.T) {
	got := runAlias(t, `alias t {
  hmake abc
  hmake 2
  hadd 2 k v
  hadd abc k other
  echo -a $hget(2,k) $hget(abc,k) $hget(0)
}`, "")
	if want := []string{"#trout: v other 2"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnextend(t *testing.T) {
	for in, want := range map[string]string{
		"a b # comment\nc": "abc",
		"[ ]x":             "[ ]x",
		"[#]\\d+":          "[#]\\d+",
		"[]a ]b":           "[]a ]b",
		"[^] ]c d":         "[^] ]cd",
		"\\ a":             "\\ a",
	} {
		if got := unextend(in); got != want {
			t.Errorf("unextend(%q) = %q, want %q", in, got, want)
		}
	}
}
