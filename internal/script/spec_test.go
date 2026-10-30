package script

// Spec-conformance tests, written from mIRC's help file and not from the
// implementation. Each case quotes the help text it is based on. Where the
// help is ambiguous, the case is left out.

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ergochat/irc-go/ircmsg"
)

// ---------------------------------------------------------------- helpers

// specRender turns actions into comparable strings: an echo is its text,
// a command is the command.
func specRender(actions []Action) []string {
	var out []string
	for _, a := range actions {
		switch {
		case a.Save != nil:
		case a.Echo != nil:
			out = append(out, strings.TrimSpace(a.Echo.Text))
		default:
			out = append(out, a.Command)
		}
	}
	return out
}

func specLoad(t *testing.T, e *Engine, name, src string) bool {
	t.Helper()
	ok := true
	for _, p := range e.Load(name, src) {
		t.Errorf("%s: load problem: %s", name, p.String())
		ok = false
	}
	return ok
}

func specEngine(vars map[string]string) *Engine {
	e := New(Options{Rand: rand.New(rand.NewPCG(7, 9))})
	e.Restore(vars)
	return e
}

// specRunHost loads src and runs /t with args, returning what it did.
func specRunHost(t *testing.T, h *fakeHost, src, args string, vars map[string]string) ([]string, *Engine) {
	t.Helper()
	e := specEngine(vars)
	if !specLoad(t, e, "spec.mrc", src) {
		return nil, e
	}
	actions, ok := e.Alias("TestNet", "#trout", "t", args, h)
	if !ok {
		t.Errorf("no alias t")
		return nil, e
	}
	return specRender(actions), e
}

func specSame(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

type specCase struct {
	name, src, args string
	vars            map[string]string
	want            []string
	vars2           map[string]string // global variables that must have these values afterwards
	gone            []string          // global variables that must not exist afterwards
}

func runSpecCases(t *testing.T, cases []specCase) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, e := specRunHost(t, evalHost(), c.src, c.args, c.vars)
			specSame(t, got, c.want)
			vars := e.Vars()
			for k, v := range c.vars2 {
				if g, ok := vars[k]; !ok || g != v {
					t.Errorf("%%%s = %q (set %v), want %q", k, g, ok, v)
				}
			}
			for _, k := range c.gone {
				if g, ok := vars[k]; ok {
					t.Errorf("%%%s = %q, want it unset", k, g)
				}
			}
		})
	}
}

// specEval evaluates each text as the line of an alias called with the
// params, with the given variables set.
func specEval(t *testing.T, vars map[string]string, params []string, cases [][2]string) {
	t.Helper()
	e := specEngine(vars)
	for _, c := range cases {
		t.Run(c[0], func(t *testing.T) {
			if got := evalIn(t, e, c[0], params...); got != c[1] {
				t.Errorf("%s = %q, want %q", c[0], got, c[1])
			}
		})
	}
}

// specCond runs `if COND` in an alias and reports whether it was true.
func specCond(t *testing.T, cond string, vars map[string]string, args string) bool {
	t.Helper()
	got, _ := specRunHost(t, evalHost(), "alias t {\n  if "+cond+" { echo -a T }\n  else { echo -a F }\n}", args, vars)
	if len(got) != 1 || (got[0] != "T" && got[0] != "F") {
		t.Errorf("if %s: got %q, want T or F", cond, got)
		return false
	}
	return got[0] == "T"
}

func specConds(t *testing.T, vars map[string]string, cases []struct {
	cond string
	want bool
},
) {
	for _, c := range cases {
		t.Run(c.cond, func(t *testing.T) {
			if got := specCond(t, c.cond, vars, "one two"); got != c.want {
				t.Errorf("if %s = %v, want %v", c.cond, got, c.want)
			}
		})
	}
}

// ------------------------------------------------------------ aliases

// Examples from the Aliases page, run as the alias would be typed.
func TestSpecAliasExamples(t *testing.T) {
	type ex struct {
		name, src, args string
		want            []string
	}
	for _, c := range []ex{
		// "/gb /join #gb" -- "typing /gb is the same as typing /join #gb".
		{"gb", `alias t /join #gb`, "", []string{"/join #gb"}},
		// "/j /join $1" -- "/j #gb is the same as /join #gb".
		{"j", `alias t /join $1`, "#gb", []string{"/join #gb"}},
		// "/yell /me $2 $1" -- "/yell There! Hello ... will be /me Hello There!".
		{"yell", `alias t /me $2 $1`, "There! Hello", []string{"/me Hello There!"}},
		// "/jj /join #$1" -- the # prefixes the parameter with a hash.
		{"hash param", `alias t /join #$1`, "gb", []string{"/join #gb"}},
		// "/give /me gives $$1 a $$2" -- "If you specify only one parameter
		// in the above command it will not be executed."
		{"give one param", `alias t /me gives $$1 a $$2`, "goat", nil},
		{"give no params", `alias t /me gives $$1 a $$2`, "", nil},
		{"give two params", `alias t /me gives $$1 a $$2`, "goat cookie", []string{"/me gives goat a cookie"}},
		// "/slap /me slaps $1 around with $2-" -- "/slap sheepy a large trout
		// ... will be /me slaps sheepy around with a large trout".
		{"slap", `alias t /me slaps $1 around with $2-`, "sheepy a large trout", []string{"/me slaps sheepy around with a large trout"}},
		// "You can also specify $2-5 which means use only parameters 2 to 5."
		{"range", `alias t /say $2-5`, "a b c d e f g", []string{"/say b c d e"}},
		// "/laugh /me laughs at $1's joke" -- "/laugh goat ... /me laughs at goat's joke".
		{"laugh", `alias t /me laughs at $1's joke`, "goat", []string{"/me laughs at goat's joke"}},
		// "/silly /say Hel $+ lo th $+ ere $+ !" -- "will say Hello there!".
		{"silly", `alias t /say Hel $+ lo th $+ ere $+ !`, "", []string{"/say Hello there!"}},
		// "/p /part #" -- "#blah ... the final command is /part #blah".
		{"part", `alias t /part #`, "", []string{"/part #trout"}},
		// "/op /mode # +o $1".
		{"op", `alias t /mode # +o $1`, "goat", []string{"/mode #trout +o goat"}},
		// "/dop /mode # -ooo $1 $2 $3" -- "/dop goat dog cat".
		{"dop", `alias t /mode # -ooo $1 $2 $3`, "goat dog cat", []string{"/mode #trout -ooo goat dog cat"}},
		// "/dkb /kick # $1 | /mode # +b $1" -- "For multiple commands you
		// should use a | character".
		{"dkb", `alias t /kick # $1 | /mode # +b $1`, "goat", []string{"/kick #trout goat", "/mode #trout +b goat"}},
		// "/poem { /msg $1 The Wendigo, ... }" -- the { } brackets make a
		// multi-line alias.
		{"poem", "alias t {\n /msg $1 The Wendigo, the Wendigo,\n /msg $1 Its eyes are ice and indigo...\n}", "bob",
			[]string{"/msg bob The Wendigo, the Wendigo,", "/msg bob Its eyes are ice and indigo..."}},
		// "In scripts, all lines are assumed to start with a command, so you
		// do not need to use the / command prefix."
		{"poem without slashes", "alias t {\n msg $1 The Wendigo, the Wendigo,\n msg $1 Its eyes are ice and indigo...\n}", "bob",
			[]string{"/msg bob The Wendigo, the Wendigo,", "/msg bob Its eyes are ice and indigo..."}},
		// "Anything appended to a $ parameter is appended to the final parameter."
		{"param suffix", `alias t /say $1-!`, "hello world", []string{"/say hello world!"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, _ := specRunHost(t, evalHost(), c.src, c.args, nil)
			specSame(t, got, c.want)
		})
	}
}

// The alias parameter identifiers, from the Aliases and Remote Identifiers pages.
func TestSpecParams(t *testing.T) {
	specEval(t, map[string]string{"x": "%y", "y": "Hiya!", "one": "uno"}, []string{"one", "two", "three", "four"}, [][2]string{
		// "$1 ... $N refer to individual parameters"
		{"$1", "one"},
		{"$4", "four"},
		// "$N- ... parameters N and onwards ... to refer to a whole line, you would use $1-"
		{"$1-", "one two three four"},
		{"$2-", "two three four"},
		// "$N-M ... parameters $N through to $M"
		{"$2-3", "two three"},
		// "$0 Returns the number of space-delimited tokens in the $1- line."
		{"$0", "4"},
		// A parameter that doesn't exist is $null.
		{"$9", ""},
		// "$2 $1" swaps.
		{"$2 $1", "two one"},
		{"$1's joke", "one's joke"},
		{"Hel $+ lo th $+ ere $+ !", "Hello there!"},
		// "The # sign refers to the channel you are currently on."
		{"#", "#trout"},
		{"#$1", "#one"},
		{"#gb", "#gb"},
		// "/say % [ $+ [ $1 ] ]" -- brackets first, so $1 names the variable.
		{"% [ $+ [ $1 ] ]", "uno"},
		// "/set %x %y; /set %y Hiya!; /echo [ [ %x ] ]"
		{"[ [ %x ] ]", "Hiya!"},
		// without brackets the contents of %x are not evaluated again.
		{"%x", "%y"},
	})
}

// "In scripts, all lines are assumed to start with a command" + the if, goto
// and number examples on the Aliases page, verbatim.
func TestSpecNumberAlias(t *testing.T) {
	ifElse := "alias t {\n if ($1 == 1) echo One\n elseif ($1 == 2) echo Two\n else echo Unknown number!\n}"
	gotoNum := "alias t {\n if ($1 == 1) goto one\n elseif ($1 == 2) goto two\n else goto unknown\n :one\n echo One\n halt\n :two\n echo Two\n halt\n :unknown\n echo Unknown number!\n halt\n}"
	var cases []specCase
	for _, s := range []struct{ n, src string }{{"if", ifElse}, {"goto", gotoNum}} {
		for arg, want := range map[string]string{"1": "One", "2": "Two", "3": "Unknown number!", "99": "Unknown number!"} {
			cases = append(cases, specCase{name: s.n + " " + arg, src: s.src, args: arg, want: []string{want}})
		}
	}
	runSpecCases(t, cases)
}

// Custom identifiers, from the Aliases page.
func TestSpecCustomIdentifiers(t *testing.T) {
	add := "alias add {\n %x = $1 + $2\n return %x\n}\n"
	addNeg := "alias add {\n %x = $1 + $2\n if ($prop == negative) return $calc(-1 * %x)\n return %x\n}\n"
	runSpecCases(t, []specCase{
		// "//echo Total is: $add(1,2)"
		{name: "add", src: add + "alias t echo -a Total is: $add(1,2)", want: []string{"Total is: 3"}},
		// "You can supply as many parameters as you want, ie. $add(1,2,...,N)."
		{name: "many params", src: "alias cnt return $0\nalias t echo -a $cnt(a,b,c,d,e)", want: []string{"5"}},
		// "//echo Total is: $add(1,2).negative"
		{name: "prop", src: addNeg + "alias t echo -a Total is: $add(1,2).negative", want: []string{"Total is: -3"}},
		{name: "no prop", src: addNeg + "alias t echo -a Total is: $add(1,2)", want: []string{"Total is: 3"}},
		// "Note: Built-in identifiers of the same name have priority."
		{name: "builtin priority", src: "alias len return custom\nalias t echo -a $len(abc)", want: []string{"3"}},
		// "/return ... optionally specify a return value which will be stored
		// in the $result identifier."
		{name: "result", src: "alias f return hello\nalias t {\n f\n echo -a got $result\n}", want: []string{"got hello"}},
		// "The /return command halts a currently executing script and allows
		// the calling routine to continue processing."
		{name: "return continues caller", src: "alias f {\n return 42\n echo -a not reached\n}\nalias t {\n f\n echo -a after $result\n}", want: []string{"after 42"}},
		// "The /return command halts a currently executing script" -- also out of loops.
		{name: "return from loop", src: "alias f {\n var %i = 0\n while (1) {\n  inc %i\n  if (%i == 3) return %i\n }\n}\nalias t {\n f\n echo -a $result\n}", want: []string{"3"}},
		// "The /halt command halts ... in aliases to halt an alias, and any
		// calling aliases, completely."
		{name: "halt calling aliases", src: "alias f halt\nalias t {\n echo -a before\n f\n echo -a not reached\n}", want: []string{"before"}},
		{name: "halt mid-alias", src: "alias t {\n echo -a a\n halt\n echo -a b\n}", want: []string{"a"}},
		// "$isid Returns $true if an alias was called as an identifier, otherwise $false."
		{name: "isid", src: "alias f return $iif($isid,identifier,command)\nalias g echo -a $iif($isid,identifier,command)\nalias t {\n echo -a $f\n g\n}", want: []string{"identifier", "command"}},
		// "An alias cannot call itself recursively" -- not tested, see TestSpecNoRecursion.
	})
}

// "If you specify the -l switch in the alias definition, the alias becomes
// accessible only by commands in the same script and invisible to the command
// line and other scripts."
func TestSpecLocalAlias(t *testing.T) {
	e := specEngine(nil)
	specLoad(t, e, "a.mrc", "alias -l helper return private\nalias pub echo -a $helper")
	// Another script that tries to use it may be rejected at load; either way it must not work.
	e.Load("b.mrc", "alias other echo -a x $helper")
	h := evalHost()
	if _, ok := e.Alias("TestNet", "#trout", "helper", "", h); ok {
		t.Errorf("-l alias is visible from the command line")
	}
	acts, ok := e.Alias("TestNet", "#trout", "pub", "", h)
	if !ok || !slices.Equal(specRender(acts), []string{"private"}) {
		t.Errorf("same-script use of -l alias: ok=%v got %q, want [private]", ok, specRender(acts))
	}
	acts, _ = e.Alias("TestNet", "#trout", "other", "", h)
	for _, s := range specRender(acts) {
		if strings.Contains(s, "private") {
			t.Errorf("another script can use a -l alias: %q", s)
		}
	}
}

// "An alias cannot call itself recursively".
func TestSpecNoRecursion(t *testing.T) {
	got, _ := specRunHost(t, evalHost(), "alias t {\n echo -a x\n t\n}", "", nil)
	n := 0
	for _, s := range got {
		if s == "x" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("an alias calling itself ran %d times (%q), want once", n, got)
	}
}

// Comments and $&, from the Aliases page.
func TestSpecCommentsAndContinuation(t *testing.T) {
	runSpecCases(t, []specCase{
		// "You can add comments to your scripts by using the ; semi-colon at
		// the start of a line, or /* and */ to enclose text."
		{name: "comments", src: ";This is a comment\n/*\nThis is a comment\n*/\nalias t {\n ;inside\n echo -a hi\n /* inline\n block */\n echo -a there\n}", want: []string{"hi", "there"}},
		// The $& longline example.
		{name: "longline", src: "alias t {\n echo This is an example of a long $&\n line that has been split into multiple lines $&\n to make it easier to edit\n}",
			want: []string{"This is an example of a long line that has been split into multiple lines to make it easier to edit"}},
	})
}

// "Repeats a loop containing a set of commands while the expression in
// brackets is true." and break/continue.
func TestSpecLoops(t *testing.T) {
	ten := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}
	runSpecCases(t, []specCase{
		{name: "help while example", src: "alias t {\n var %i = 1\n while (%i <= 10) {\n  echo -a %i\n  inc %i\n }\n}", want: ten},
		{name: "while false never runs", src: "alias t {\n while (1 == 2) {\n  echo -a never\n }\n echo -a done\n}", want: []string{"done"}},
		// "You can use /break to break out of the current loop, and /continue
		// to jump to the beginning of the loop."
		{name: "break and continue", src: "alias t {\n var %i = 0\n while (%i < 10) {\n  inc %i\n  if (%i == 2) continue\n  if (%i == 5) break\n  echo -a %i\n }\n echo -a end %i\n}", want: []string{"1", "3", "4", "end 5"}},
		// "Multiple while loops can be embedded."
		{name: "nested while, inner break", src: "alias t {\n var %a = 0\n while (%a < 2) {\n  inc %a\n  var %b = 0\n  while (1) {\n   inc %b\n   if (%b > 2) break\n   echo -a %a $+ . $+ %b\n  }\n }\n}", want: []string{"1.1", "1.2", "2.1", "2.2"}},
		// The listops goto loop from the If-then-else page, and the "loop"
		// example from Variables.
		{name: "variables loop example", src: "alias t {\n var %x = 1\n :next\n echo -a item %x\n inc %x\n if (%x < 10) goto next\n}",
			want: []string{"item 1", "item 2", "item 3", "item 4", "item 5", "item 6", "item 7", "item 8", "item 9"}},
	})
	// "/echo 2 %i" is a color then the text; the color isn't part of the text.
	e := specEngine(nil)
	specLoad(t, e, "w.mrc", "alias t {\n var %i = 1\n while (%i <= 3) {\n  echo 2 %i\n  inc %i\n }\n}")
	acts, _ := e.Alias("TestNet", "#trout", "t", "", evalHost())
	var texts []string
	for _, a := range acts {
		if a.Echo != nil {
			texts = append(texts, strings.TrimSpace(a.Echo.Text))
			if a.Echo.Color != 2 {
				t.Errorf("echo 2 %%i: color %d, want 2", a.Echo.Color)
			}
		}
	}
	specSame(t, texts, []string{"1", "2", "3"})
}

// ---------------------------------------------------------- variables

func TestSpecVariables(t *testing.T) {
	runSpecCases(t, []specCase{
		// "%i = 5 / %xyzi = 3.14159 / %count = $1 / %text = how now brown cow?"
		{name: "assign", src: "alias t {\n %i = 5\n %xyzi = 3.14159\n %count = $1\n %text = how now brown cow?\n}", args: "first second",
			vars2: map[string]string{"i": "5", "xyzi": "3.14159", "count": "first", "text": "how now brown cow?"}},
		// "%x = 5 + 1 / %x = 5 - %y / %x = %x * 2 / %x = %z / $2 / %x = $1 % 3 / %x = 2 ^ %w"
		{name: "math add", src: "alias t {\n %x = 5 + 1\n}", vars2: map[string]string{"x": "6"}},
		{name: "math sub", src: "alias t {\n %x = 5 - %y\n}", vars: map[string]string{"y": "2"}, vars2: map[string]string{"x": "3"}},
		{name: "math mul", src: "alias t {\n %x = %x * 2\n}", vars: map[string]string{"x": "4"}, vars2: map[string]string{"x": "8"}},
		{name: "math div", src: "alias t {\n %x = %z / $2\n}", vars: map[string]string{"z": "10"}, args: "a 4", vars2: map[string]string{"x": "2.5"}},
		{name: "math mod", src: "alias t {\n %x = $1 % 3\n}", args: "10", vars2: map[string]string{"x": "1"}},
		{name: "math pow", src: "alias t {\n %x = 2 ^ %w\n}", vars: map[string]string{"w": "3"}, vars2: map[string]string{"x": "8"}},
		// "If %y is the literal text 1 + 2: set %x %y ... x is equal to 3;
		// set -n %x %y ... x is equal to 1 + 2"
		{name: "set does math", src: "alias t {\n set %x %y\n}", vars: map[string]string{"y": "1 + 2"}, vars2: map[string]string{"x": "3"}},
		{name: "set -n", src: "alias t {\n set -n %x %y\n}", vars: map[string]string{"y": "1 + 2"}, vars2: map[string]string{"x": "1 + 2"}},
		// "-n prevents mathematical operations": without it set computes.
		{name: "set expression", src: "alias t {\n set %m 2 + 3\n set -n %raw 2 + 3\n}", vars2: map[string]string{"m": "5", "raw": "2 + 3"}},
		// "If a variable is referred to and it does not exist, it returns the value $null."
		{name: "unset is null", src: "alias t {\n if (%nope == $null) echo -a null\n else echo -a value\n echo -a x $+ %nope $+ x\n}", want: []string{"null", "xx"}},
		// "/var %x ... creates the local variable %x in the current routine
		// and can only be referenced from inside this routine."
		{name: "var list", src: "alias t {\n var %x = hello, %y, %z = $me\n echo -a %x / %z / $len(%y)\n}", want: []string{"hello / trout / 0"}, gone: []string{"x", "y", "z"}},
		{name: "var local to routine", src: "alias t {\n var %v = local\n u\n echo -a %v\n}\nalias u echo -a in u: %v", want: []string{"in u:", "local"}, gone: []string{"v"}},
		// "The -l switch makes the variable a local variable."
		{name: "set -l", src: "alias t {\n set -l %lv 5\n echo -a %lv\n}", want: []string{"5"}, gone: []string{"lv"}},
		// "The -g switch sets a global variable. Normally, if a local variable
		// already exists, it takes precedence, so this switch allows you to
		// override that."
		{name: "set -g overrides local", src: "alias t {\n var %v = local\n set -g %v global\n echo -a %v\n}", want: []string{"local"}, vars2: map[string]string{"v": "global"}},
		// "If you specify a zero for N, the variable is unset when the script finishes."
		{name: "set -u0", src: "alias t {\n set -u0 %tmp 7\n echo -a %tmp\n}", want: []string{"7"}, gone: []string{"tmp"}},
		// "The -i switch initializes a variable only if it does not already exist."
		{name: "set -i existing", src: "alias t {\n set -i %b1 new\n}", vars: map[string]string{"b1": "keep"}, vars2: map[string]string{"b1": "keep"}},
		{name: "set -i new", src: "alias t {\n set -i %c1 new\n}", vars2: map[string]string{"c1": "new"}},
		// "/unset ... If you specify a variable with wildcard characters then
		// all matching variables will be removed. /unset %test*"
		{name: "unset wildcard", src: "alias t {\n unset %test*\n}", vars: map[string]string{"test1": "a", "test2": "b", "testing": "c", "keep": "d"}, gone: []string{"test1", "test2", "testing"}, vars2: map[string]string{"keep": "d"}},
		{name: "unset two", src: "alias t {\n unset %a %b\n}", vars: map[string]string{"a": "1", "b": "2", "c": "3"}, gone: []string{"a", "b"}, vars2: map[string]string{"c": "3"}},
		// "/inc ... increases the value of %var by value." The while loop
		// example uses "inc %i" to count by one.
		{name: "inc dec", src: "alias t {\n set %c 5\n inc %c\n echo -a %c\n inc %c 10\n echo -a %c\n dec %c 3\n echo -a %c\n dec %c\n echo -a %c\n}", want: []string{"6", "16", "13", "12"}},
		// The vartest example, verbatim.
		{name: "dynamic variables", src: "alias t {\n set %a [ $+ b ] 1\n set %a [ $+ c ] 2\n set %a [ $+ d ] 3\n\n echo ab = %ab\n echo ac = %ac\n echo ad = %ad\n\n unset %a [ $+ b ] %a [ $+ c ] %a [ $+ d ]\n}",
			want: []string{"ab = 1", "ac = 2", "ad = 3"}, gone: []string{"ab", "ac", "ad"}},
		// A set variable survives the script (global); /unset removes it.
		{name: "global persists", src: "alias t {\n set %keep hello\n}", vars2: map[string]string{"keep": "hello"}},
	})
}

// -------------------------------------------------------- if-then-else

func TestSpecOperators(t *testing.T) {
	vars := map[string]string{"x": "5", "text": "how now brown cow", "zero": "0"}
	type c = struct {
		cond string
		want bool
	}
	specConds(t, vars, []c{
		// "== equal to"
		{`(1 == 1)`, true},
		{`(1 == 2)`, false},
		{`(abc == abc)`, true},
		// "=== equal to (case-sensitive)": so == is not.
		{`(ABC == abc)`, true},
		{`(ABC === abc)`, false},
		{`(abc === abc)`, true},
		// "!= not equal to"
		{`(1 != 2)`, true},
		{`(2 != 2)`, false},
		// "< <= > >=" numeric
		{`(1 < 2)`, true},
		{`(2 < 1)`, false},
		{`(2 < 2)`, false},
		{`(2 <= 2)`, true},
		{`(3 <= 2)`, false},
		{`(10 > 9)`, true},
		{`(9 > 10)`, false},
		{`(2 > 2)`, false},
		{`(2 >= 2)`, true},
		{`(3 >= 4)`, false},
		{`(-1 < 0)`, true},
		{`(1.5 < 2)`, true},
		{`(%x > 3)`, true},
		// "// v2 is a multiple of v1" ("if (4 // %i)" in GiveOps)
		{`(4 // 12)`, true},
		{`(4 // 8)`, true},
		{`(5 // 12)`, false},
		// "\\ v2 is not a multiple of v1"
		{`(5 \\ 12)`, true},
		{`(4 \\ 12)`, false},
		// "& bitwise comparison"
		{`(3 & 1)`, true},
		{`(6 & 2)`, true},
		{`(4 & 3)`, false},
		{`(5 & 2)`, false},
		// "isin string v1 is in string v2" / "isincs ... (case sensitive)"
		{`(text isin sometext)`, true},
		{`(abc isin xyz)`, false},
		{`(TEXT isin sometext)`, true},
		{`(TEXT isincs sometext)`, false},
		{`(text isincs sometext)`, true},
		{`(now isin %text)`, true},
		// "iswm wildcard string v1 matches string v2" / "iswmcs ... (case sensitive)"
		{`(h*o iswm hello)`, true},
		{`(h?llo iswm hello)`, true},
		{`(h?lo iswm hello)`, false},
		{`(*brown* iswm %text)`, true},
		{`(H*O iswm hello)`, true},
		{`(H*O iswmcs hello)`, false},
		{`(h*o iswmcs hello)`, true},
		// "isnum number v1 is a number in the range v2 which is in the form n1-n2 (v2 optional)"
		{`(5 isnum)`, true},
		{`(abc isnum)`, false},
		{`(5 isnum 1-10)`, true},
		{`(11 isnum 1-10)`, false},
		{`(0 isnum 1-10)`, false},
		// "isletter letter v1 is a letter in the list of letters in v2 (v2 optional)"
		{`(a isletter)`, true},
		{`(1 isletter)`, false},
		{`(b isletter abc)`, true},
		{`(d isletter abc)`, false},
		// "isalnum text contains only letters and numbers" etc.
		{`(ab1 isalnum)`, true},
		{`(ab_ isalnum)`, false},
		{`(abc isalpha)`, true},
		{`(ab1 isalpha)`, false},
		{`(abc islower)`, true},
		{`(aBc islower)`, false},
		{`(ABC isupper)`, true},
		{`(AbC isupper)`, false},
		// "ison nickname v1 is on channel v2" -- #trout has trout and alice
		// (ops), bob (voice), carol (regular).
		{`(carol ison #trout)`, true},
		{`(dave ison #trout)`, false},
		{`(alice isop #trout)`, true},
		{`(bob isop #trout)`, false},
		{`(carol isop #trout)`, false},
		{`(bob isvoice #trout)`, true},
		{`(carol isvoice #trout)`, false},
		{`(carol isreg #trout)`, true},
		{`(bob isreg #trout)`, false},
		{`(alice isreg #trout)`, false},
		// "ischan if v1 is a channel which you are on."
		{`(#trout ischan)`, true},
		{`(#elsewhere ischan)`, false},
		{`(bob ischan)`, false},
		// "To negate an operator you can prefix it with an ! exclamation mark."
		{`(abc !isin xyz)`, true},
		{`(abc !isin xabcx)`, false},
		{`(a* !iswm abc)`, false},
		{`(z* !iswm abc)`, true},
		{`(abc !isnum)`, true},
		{`(5 !isnum)`, false},
		{`(dave !ison #trout)`, true},
		{`(bob !isop #trout)`, true},
		{`(alice !isop #trout)`, false},
		// "You can combine comparisons by using the && for AND and || for OR"
		{`((1 == 1) && (2 == 2))`, true},
		{`((1 == 1) && (2 == 3))`, false},
		{`((1 == 2) || (2 == 2))`, true},
		{`((1 == 2) || (2 == 3))`, false},
		{`((%x > 0) && (%x < 10))`, true},
		{`((%x > 0) && (%x < 5))`, false},
		// "The ! not prefix ... if (%x == $null) echo x has no value; if (!%x) echo x has no value"
		{`(!%nope)`, true},
		{`(!%x)`, false},
		{`(%nope == $null)`, true},
		{`(%x == $null)`, false},
		{`(!$3)`, true},
		{`(!$1)`, false},
	})
}

// "$v1 will return "text" and $v2 will return "sometext"" for
// if (text isin sometext), and $ifmatch "returns "text"".
func TestSpecV1V2(t *testing.T) {
	runSpecCases(t, []specCase{
		{name: "v1 v2", src: "alias t {\n if (text isin sometext) echo -a $v1 $v2\n}", want: []string{"text sometext"}},
		{name: "ifmatch", src: "alias t {\n if (text isin sometext) echo -a $ifmatch\n}", want: []string{"text"}},
		{name: "v1 v2 compare", src: "alias t {\n if (5 > 3) echo -a $v1 and $v2\n}", want: []string{"5 and 3"}},
	})
}

// The If-then-else page's examples, verbatim.
func TestSpecIfExamples(t *testing.T) {
	number := "alias t {\n if (($1 > 0) && ($1 < 10)) {\n   if ($1 < 5) echo Number is less than five\n   else echo Number is greater than five\n }\n else echo Number is out of bounds\n}"
	var cases []specCase
	for _, c := range []struct{ arg, want string }{
		{"3", "Number is less than five"},
		{"1", "Number is less than five"},
		{"5", "Number is greater than five"},
		{"7", "Number is greater than five"},
		{"0", "Number is out of bounds"},
		{"10", "Number is out of bounds"},
		{"15", "Number is out of bounds"},
		{"-2", "Number is out of bounds"},
	} {
		cases = append(cases, specCase{name: "number " + c.arg, src: number, args: c.arg, want: []string{c.want}})
	}
	// "This alias lists the Ops on the current channel."  The two ops come
	// back in either order.
	cases = append(cases, specCase{name: "if without brackets: set and compare", src: "alias t {\n set %i 1\n :next\n set %nick $nick(#,%i)\n if %nick == $null goto done\n inc %i\n goto next\n :done\n echo -a counted $calc(%i - 1)\n}", want: []string{"counted 4"}})
	runSpecCases(t, cases)

	e := specEngine(nil)
	specLoad(t, e, "l.mrc", "alias t {\n echo 4 * Listing Ops on #\n set %i 1\n :next\n set %nick $nick(#,%i)\n if %nick == $null goto done\n if %nick isop # echo 3 %nick is an Op!\n inc %i\n goto next\n :done\n echo 4 * End of Ops list\n}")
	acts, ok := e.Alias("TestNet", "#trout", "t", "", evalHost())
	if !ok {
		t.Fatal("no alias t")
	}
	var texts []string
	colors := map[string]int{}
	for _, a := range acts {
		if a.Echo != nil {
			s := strings.TrimSpace(a.Echo.Text)
			texts = append(texts, s)
			colors[s] = a.Echo.Color
		}
	}
	if len(texts) != 4 {
		t.Fatalf("listops: got %q", texts)
	}
	if texts[0] != "* Listing Ops on #trout" || texts[3] != "* End of Ops list" {
		t.Errorf("listops header/footer: %q", texts)
	}
	mid := []string{texts[1], texts[2]}
	slices.Sort(mid)
	if !slices.Equal(mid, []string{"alice is an Op!", "trout is an Op!"}) {
		t.Errorf("listops ops: %q", mid)
	}
	if colors["* Listing Ops on #trout"] != 4 || colors["alice is an Op!"] != 3 {
		t.Errorf("listops colors: %v", colors)
	}
}

// The PING reply example: "%pt = $ctime - $2", "if (%pt < 0) set %pt 0".
func TestSpecPingReplyArithmetic(t *testing.T) {
	src := "alias t {\n %pt = $ctime - $1\n if (%pt < 0) set %pt 0\n if (%pt < 5) echo -a close\n elseif (%pt < 20) echo -a right\n else echo -a far\n}"
	runSpecCases(t, []specCase{
		{name: "2s", src: src, args: "1793286243", want: []string{"close"}},
		{name: "10s", src: src, args: "1793286235", want: []string{"right"}},
		{name: "100s", src: src, args: "1793286145", want: []string{"far"}},
		{name: "future", src: src, args: "1793286295", want: []string{"close"}},
	})
}

// "$iif(C,T,F)" etc.
func TestSpecIif(t *testing.T) {
	specEval(t, map[string]string{"n": "5", "zero": "0"}, []string{"one", "two"}, [][2]string{
		// "$iif(1 == 2, yes, no) returns "no""
		{"$iif(1 == 2, yes, no)", "no"},
		{"$iif(1 == 1, yes, no)", "yes"},
		// "$iif(1 == 2, yes) returns nothing"
		{"$iif(1 == 2, yes)", ""},
		{"$iif(1 == 1, yes)", "yes"},
		// "$iif() returns F if the conditional returns zero, $false, or $null.
		// For any other value $iif() returns T."
		{"$iif(0, yes, no)", "no"},
		{"$iif($false, yes, no)", "no"},
		{"$iif($null, yes, no)", "no"},
		{"$iif(%nope, yes, no)", "no"},
		{"$iif(%zero, yes, no)", "no"},
		{"$iif($true, yes, no)", "yes"},
		{"$iif(5, yes, no)", "yes"},
		{"$iif(abc, yes, no)", "yes"},
		{"$iif(%n > 3, big, small)", "big"},
		{"$iif(%n > 7, big, small)", "small"},
		{"$iif($1 == one, first, other)", "first"},
	})
}

// "$eval(text,N) Evaluates the contents of text N times. If N is not
// specified, the default is N = 1. If N is zero, text is not evaluated."
func TestSpecEval(t *testing.T) {
	specEval(t, map[string]string{"x": "%y", "y": "Hiya!"}, nil, [][2]string{
		{"$eval(%x,0)", "%x"},
		{"$eval(%x,1)", "%y"},
		{"$eval(%x)", "%y"},
		{"$eval(%x,2)", "Hiya!"},
	})
}

// --------------------------------------------------- text and numbers

func TestSpecTextIdentifiers(t *testing.T) {
	colors := "\x02bold\x02 \x034red\x03 \x0304,05both\x03 \x1funder\x1f \x16rev\x16 \x1dital\x1d \x1estrike\x1e"
	specEval(t, map[string]string{"colored": colors, "mixed": "Hello"}, []string{"one", "two"}, [][2]string{
		// "$abs(5) returns 5 / $abs(-1) returns 1"
		{"$abs(5)", "5"},
		{"$abs(-1)", "1"},
		{"$abs(-2.5)", "2.5"},
		// "$asc(A) returns 65 / $asc(*) returns 42"
		{"$asc(A)", "65"},
		{"$asc(*)", "42"},
		// "$chr(65) returns A / $chr(42) returns *"
		{"$chr(65)", "A"},
		{"$chr(42)", "*"},
		// "$count(hello,el) returns 1 / $count(hello,l) returns 2"
		{"$count(hello,el)", "1"},
		{"$count(hello,l)", "2"},
		// "$countcs() is the case-sensitive version."
		{"$count(Hello,L)", "2"},
		{"$countcs(Hello,L)", "0"},
		{"$countcs(Hello,l)", "2"},
		// "$int(3.14159) returns 3" -- "with no rounding"
		{"$int(3.14159)", "3"},
		{"$int(3.99)", "3"},
		{"$int(-3.7)", "-3"},
		// "$left(goodbye,4) returns good" / negative: "all but N characters"
		{"$left(goodbye,4)", "good"},
		{"$left(goodbye,-3)", "good"},
		// "$len(#mIRC) returns 5"
		{"$len(#mIRC)", "5"},
		{"$len(hello)", "5"},
		// "$lower(HELLO) returns hello" / "$upper(hello) returns HELLO"
		{"$lower(HELLO)", "hello"},
		{"$upper(hello)", "HELLO"},
		{"$upper(%mixed) $lower(%mixed)", "HELLO hello"},
		// "$mid(othello,3,4) returns hell"
		{"$mid(othello,3,4)", "hell"},
		{"$mid(othello,1,2)", "ot"},
		// "$pos(hello there,e,1) returns 2 / ,e,2 returns 9 / ,a,1 returns $null"
		{"$pos(hello there,e,1)", "2"},
		{"$pos(hello there,e,2)", "9"},
		{"$pos(hello there,a,1)", ""},
		// "If N is zero, it returns the number of times string appears in text."
		{"$pos(hello there,e,0)", "3"},
		// "You can use $poscs() for a case-sensitive version."
		{"$pos(Hello,L,1)", "3"},
		{"$poscs(Hello,L,1)", ""},
		{"$poscs(Hello,l,1)", "3"},
		// "$remove(abcdefg,cd) returns abefg / $remove(abcdefg,a,c,e,g) returns bdf"
		{"$remove(abcdefg,cd)", "abefg"},
		{"$remove(abcdefg,a,c,e,g)", "bdf"},
		{"$remove(Hello,L)", "Heo"},
		{"$removecs(Hello,L)", "Hello"},
		{"$removecs(Hello,l)", "Heo"},
		// "$replace(abcdefg,cd,xyz) returns abxyzefg / ...,a,A,b,B,c,C,d,D returns ABCDefg"
		{"$replace(abcdefg,cd,xyz)", "abxyzefg"},
		{"$replace(abcdefg,a,A,b,B,c,C,d,D)", "ABCDefg"},
		{"$replace(Hello,L,_)", "He__o"},
		{"$replacecs(Hello,L,_)", "Hello"},
		{"$replacecs(Hello,l,_)", "He__o"},
		// "$right(othello,5) returns hello" / negative: all but N
		{"$right(othello,5)", "hello"},
		{"$right(othello,-2)", "hello"},
		// "$round(3.14159,2) returns 3.14"
		{"$round(3.14159,2)", "3.14"},
		{"$round(3.14159,0)", "3"},
		{"$round(2.567,1)", "2.6"},
		// "$str(ho,3) returns hohoho"
		{"$str(ho,3)", "hohoho"},
		{"$str(x,1)", "x"},
		// "$strip(text) Returns text with bold, underline, reverse, italic,
		// strikethrough, and color control codes stripped out."
		{"$strip(%colored)", "bold red both under rev ital strike"},
		{"$strip(\x02hi\x02)", "hi"},
		{"$strip(\x034hi\x03)", "hi"},
		{"$strip(\x0304hi\x03 there)", "hi there"},
		// "$iif" aside, $null/empty:
		{"a $null b", "a b"},
		// "$me" and "$network"
		{"$me", "trout"},
		{"$network", "TestNet"},
	})
}

// "$rand(v1,v2) ... If you supply it with numbers ... a random number between
// v1 and v2 ... letters, a random letter between letters v1 and v2."
func TestSpecRand(t *testing.T) {
	e := specEngine(nil)
	seen := map[string]bool{}
	for range 300 {
		n := evalIn(t, e, "$rand(1,3)")
		if n != "1" && n != "2" && n != "3" {
			t.Fatalf("$rand(1,3) = %q", n)
		}
		seen[n] = true
		l := evalIn(t, e, "$rand(a,z)")
		if len(l) != 1 || l[0] < 'a' || l[0] > 'z' {
			t.Fatalf("$rand(a,z) = %q, want a lower case letter", l)
		}
		u := evalIn(t, e, "$rand(A,Z)")
		if len(u) != 1 || u[0] < 'A' || u[0] > 'Z' {
			t.Fatalf("$rand(A,Z) = %q, want an upper case letter", u)
		}
		z := evalIn(t, e, "$rand(0,2)")
		if z != "0" && z != "1" && z != "2" {
			t.Fatalf("$rand(0,2) = %q", z)
		}
		if evalIn(t, e, "$rand(4,4)") != "4" {
			t.Fatalf("$rand(4,4) is not 4")
		}
	}
	if len(seen) < 2 {
		t.Errorf("$rand(1,3) always gave %v", seen)
	}
}

func TestSpecCalc(t *testing.T) {
	specEval(t, map[string]string{"n": "5", "x": "1"}, nil, [][2]string{
		// "$calc(operations) Returns the result of the specified operations."
		{"$calc(1 + 2)", "3"},
		{"$calc(5 - 7)", "-2"},
		{"$calc(3 * 4)", "12"},
		{"$calc(10 / 4)", "2.5"},
		{"$calc(7 % 3)", "1"},
		{"$calc(2 ^ 3)", "8"},
		{"$calc(2 + 3 * 4)", "14"},
		{"$calc((2 + 3) * 4)", "20"},
		{"$calc(10 - 4 - 3)", "3"},
		{"$calc(%n * 2)", "10"},
		// "$calc(-1 * %x)" is used in the custom-identifier example.
		{"$calc(-1 * %n)", "-5"},
		// "$calc(3.14159 * (2 ^ %x % 3) - ...)": 2 ^ 1 % 3 is 2 either way.
		{"$calc(3.14159 * (2 ^ %x % 3))", "6.28318"},
		// "$round(N,D) and $int(N) ... handle precision of the decimal digits"
		{"$round($calc(10 / 3),2)", "3.33"},
		{"$int($calc(10 / 3))", "3"},
	})
}

// -------------------------------------------------------------- tokens

func TestSpecTokens(t *testing.T) {
	yes := func(cond string) string { return "$iif(" + cond + ",yes,no)" }
	specEval(t, nil, nil, [][2]string{
		// "$addtok(a.b.c,d,46) returns a.b.c.d / $addtok(a.b.c.d,c,46) returns a.b.c.d"
		{"$addtok(a.b.c,d,46)", "a.b.c.d"},
		{"$addtok(a.b.c.d,c,46)", "a.b.c.d"},
		// "$addtokcs() is the case-sensitive version."
		{"$addtok(a.b.c,C,46)", "a.b.c"},
		{"$addtokcs(a.b.c,C,46)", "a.b.c.C"},
		// "$deltok(a.b.c.d,3,46) returns a.b.d / (a.b.c.d,2-3,46) returns a.d"
		{"$deltok(a.b.c.d,3,46)", "a.b.d"},
		{"$deltok(a.b.c.d,2-3,46)", "a.d"},
		// "$findtok(a.b.c.d,c,1,46) returns 3 / (a.b.c.d,e,1,46) returns $null"
		{"$findtok(a.b.c.d,c,1,46)", "3"},
		{"$findtok(a.b.c.d,e,1,46)", ""},
		// "If you specify zero for N, it returns the total number of matching tokens."
		{"$findtok(a.b.c.b,b,0,46)", "2"},
		{"$findtok(a.b.c.b,b,2,46)", "4"},
		{"$findtok(a.B.c,b,1,46)", "2"},
		{"$findtokcs(a.B.c,b,1,46)", ""},
		{"$findtokcs(a.B.c,B,1,46)", "2"},
		// "$gettok(a.b.c.d.e,3,46) returns c / (...,9,46) returns $null"
		{"$gettok(a.b.c.d.e,3,46)", "c"},
		{"$gettok(a.b.c.d.e,9,46)", ""},
		// "$gettok(a.b.c.d.e,2-,46) returns 2nd token onwards b.c.d.e / 2-4 returns b.c.d"
		{"$gettok(a.b.c.d.e,2-,46)", "b.c.d.e"},
		{"$gettok(a.b.c.d.e,2-4,46)", "b.c.d"},
		{"$gettok(a.b.c.d.e,1,46)", "a"},
		{"$gettok(one two three,2,32)", "two"},
		// "$instok(a.b.d,c,3,46) returns a.b.c.d / (a.b.d,c,9,46) returns a.b.d.c"
		{"$instok(a.b.d,c,3,46)", "a.b.c.d"},
		{"$instok(a.b.d,c,9,46)", "a.b.d.c"},
		// "Inserts token ... even if it already exists in text."
		{"$instok(a.b.d,b,1,46)", "b.a.b.d"},
		// "$istok(text,token,C) Returns $true if token exists, otherwise $false."
		{yes("$istok(a b c,b,32)"), "yes"},
		{yes("$istok(a b c,d,32)"), "no"},
		{yes("$istok(a b c,B,32)"), "yes"},
		{yes("$istokcs(a b c,B,32)"), "no"},
		{yes("$istokcs(a b c,b,32)"), "yes"},
		// "$matchtok(one two three, e, 0, 32) returns 2 / (..., e, 2, 32) returns three"
		{"$matchtok(one two three, e, 0, 32)", "2"},
		{"$matchtok(one two three, e, 2, 32)", "three"},
		{"$matchtok(one two three, e, 1, 32)", "one"},
		{"$matchtok(One Two, o, 0, 32)", "2"},
		{"$matchtokcs(One Two, o, 0, 32)", "1"},
		// "$numtok(text,C) Returns number of tokens in text."
		{"$numtok(a.b.c.d,46)", "4"},
		{"$numtok(a b c,32)", "3"},
		// "$puttok(a.b.c.d,e,2,46) returns a.e.c.d"
		{"$puttok(a.b.c.d,e,2,46)", "a.e.c.d"},
		// "$remtok(a.b.c.d,b,1,46) returns a.c.d / (a.b.c.d,e,1,46) returns a.b.c.d /
		// (a.c.c.d,c,1,46) returns a.c.d"
		{"$remtok(a.b.c.d,b,1,46)", "a.c.d"},
		{"$remtok(a.b.c.d,e,1,46)", "a.b.c.d"},
		{"$remtok(a.c.c.d,c,1,46)", "a.c.d"},
		// "If N = 0, applies to all matching items."
		{"$remtok(a.c.c.d,c,0,46)", "a.d"},
		{"$remtok(a.B.c,b,1,46)", "a.c"},
		{"$remtokcs(a.B.c,b,1,46)", "a.B.c"},
		// "$reptok(a.b.c.d,b,e,1,46) returns a.e.c.d / (a.b.c.d,f,e,1,46) returns
		// a.b.c.d / (a.b.a.c,a,e,2,46) returns a.b.e.c"
		{"$reptok(a.b.c.d,b,e,1,46)", "a.e.c.d"},
		{"$reptok(a.b.c.d,f,e,1,46)", "a.b.c.d"},
		{"$reptok(a.b.a.c,a,e,2,46)", "a.b.e.c"},
		{"$reptok(a.b.a.c,a,e,0,46)", "e.b.e.c"},
		{"$reptok(a.B.c,b,x,1,46)", "a.x.c"},
		{"$reptokcs(a.B.c,b,x,1,46)", "a.B.c"},
		// "$sorttok(e.d.c.b.a,46) returns a.b.c.d.e / (1.3.5.2.4,46,nr) returns 5.4.3.2.1"
		{"$sorttok(e.d.c.b.a,46)", "a.b.c.d.e"},
		{"$sorttok(1.3.5.2.4,46,nr)", "5.4.3.2.1"},
		// "The default is an alphabetic sort ... n = numeric sort ... r = reverse sort"
		{"$sorttok(10.9.100,46)", "10.100.9"},
		{"$sorttok(10.9.100,46,n)", "9.10.100"},
		{"$sorttok(a.c.b,46,r)", "c.b.a"},
		// "$wildtok(one two three, t*, 0, 32) returns 2 / (..., t*e, 1, 32) returns three"
		{"$wildtok(one two three, t*, 0, 32)", "2"},
		{"$wildtok(one two three, t*e, 1, 32)", "three"},
		{"$wildtok(one two three, t*, 1, 32)", "two"},
		{"$wildtok(one two three, t*, 2, 32)", "three"},
		{"$wildtok(One Two, t*, 0, 32)", "1"},
		{"$wildtokcs(One Two, t*, 0, 32)", "0"},
	})
}

// ---------------------------------------------------------- time/date

func TestSpecTimeDate(t *testing.T) {
	// evalHost's clock is Thursday 2026-10-29 15:04:05 UTC.
	specEval(t, nil, nil, [][2]string{
		// "$time Returns the current time in hour:minute:second format."
		{"$time", "15:04:05"},
		// "$day Returns the name of the current day"
		{"$day", "Thursday"},
		// "$fulldate ... in the format: Wed Jun 26 21:41:02 1996"
		{"$fulldate", "Thu Oct 29 15:04:05 2026"},
		// "$date Returns the current date in day/month/year format. For the
		// date in US format you can use $adate."
		{"$left($date,6)", "29/10/"},
		{"$left($adate,6)", "10/29/"},
		{"$right($date,2) $right($adate,2)", "26 26"},
		// "$ctime Returns total number of seconds elapsed since 00:00:00 GMT, January 1, 1970"
		{"$ctime", "1793286245"},
		// "The identifiers $time(), $date(), and $gmt() can also be used with
		// the format specification"
		{"$time(HH:nn:ss)", "15:04:05"},
		{"$date(yyyy-mm-dd)", "2026-10-29"},
		// "$asctime(hh:nn:ss) returns the current time in this format"
		{"$asctime(HH:nn:ss)", "15:04:05"},
		// "$asctime(N,format)": the same instant as $time.
		{"$asctime($ctime,HH:nn:ss)", "15:04:05"},
		{"$asctime($ctime,dd/mm/yy)", "29/10/26"},
	})
}

// The format table in $asctime: yy yyyy m mm mmm mmmm d dd ddd dddd H HH n nn
// s ss t tt T TT oo z zz zzz. Saturday 7 March 2026, 21:08:07 and 09:08:07.
func TestSpecAsctimeFormats(t *testing.T) {
	for _, clock := range []struct {
		when  time.Time
		cases [][2]string
	}{
		{time.Date(2026, 3, 7, 21, 8, 7, 0, time.UTC), [][2]string{
			{"yy", "26"}, {"yyyy", "2026"},
			{"m", "3"}, {"mm", "03"}, {"mmm", "Mar"}, {"mmmm", "March"},
			{"d", "7"}, {"dd", "07"}, {"ddd", "Sat"}, {"dddd", "Saturday"},
			{"HH", "21"},
			{"n", "8"}, {"nn", "08"},
			{"s", "7"}, {"ss", "07"},
			{"t", "p"}, {"tt", "pm"}, {"T", "P"}, {"TT", "PM"},
			{"doo", "7th"},
			{"z", "+0"}, {"zz", "+0000"},
			{"dddd mmmm dd yyyy HH:nn:ss", "Saturday March 07 2026 21:08:07"},
		}},
		{time.Date(2026, 3, 1, 9, 8, 7, 0, time.UTC), [][2]string{
			{"t", "a"}, {"tt", "am"}, {"T", "A"}, {"TT", "AM"}, {"doo", "1st"}, {"HH", "09"},
		}},
		{time.Date(2026, 3, 2, 9, 8, 7, 0, time.UTC), [][2]string{{"doo", "2nd"}}},
		{time.Date(2026, 3, 3, 9, 8, 7, 0, time.UTC), [][2]string{{"doo", "3rd"}}},
		{time.Date(2026, 3, 4, 9, 8, 7, 0, time.UTC), [][2]string{{"doo", "4th"}}},
	} {
		h := evalHost()
		h.now = clock.when
		for _, c := range clock.cases {
			t.Run(clock.when.Format("2006-01-02_15:04")+"_"+c[0], func(t *testing.T) {
				for _, form := range []string{"$asctime($ctime," + c[0] + ")", "$asctime(" + c[0] + ")"} {
					got, _ := specRunHost(t, h, "alias t echo -a "+form, "", nil)
					if len(got) != 1 || got[0] != c[1] {
						t.Errorf("%s = %q, want %q", form, got, c[1])
					}
				}
			})
		}
	}
}

// "$duration(seconds,N) ... in a week/day/hour/minute/second format. The N
// parameter is optional. If N = 2, the result does not include the seconds
// value. If N = 3, the result is in hh:nn:ss format. Note: This identifier can
// also take its own output and change it back into seconds."
func TestSpecDuration(t *testing.T) {
	specEval(t, nil, nil, [][2]string{
		{"$duration(3700,3)", "01:01:40"},
		{"$duration(59,3)", "00:00:59"},
		{"$duration($duration(3700))", "3700"},
		{"$duration($duration(694861))", "694861"},
		{"$duration($duration(86400))", "86400"},
		{"$duration($duration(45))", "45"},
		// N = 2 leaves the seconds out: 3700s is 1hr 1min 40secs.
		{"$iif($pos($duration(3700,2),sec,1),has,none)", "none"},
		{"$iif($pos($duration(3700),sec,1),has,none)", "has"},
		{"$duration($duration(3700,2))", "3660"},
	})
}

// ------------------------------------------------------------- events

type specStep struct {
	raw    string
	ev     *Event
	want   []string
	halted bool
}

type specEvent struct {
	name, src string
	vars      map[string]string
	setup     func(h *fakeHost)
	steps     []specStep
}

const bob = ":bob!user@host.example "

func ctcpRaw(s string) string { return "\x01" + s + "\x01" }

func runSpecEvents(t *testing.T, cases []specEvent) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := specEngine(c.vars)
			if !specLoad(t, e, "spec.mrc", c.src) {
				return
			}
			h := evalHost()
			if c.setup != nil {
				c.setup(h)
			}
			for i, st := range c.steps {
				var ev Event
				if st.ev != nil {
					ev = *st.ev
				} else {
					m, err := ircmsg.ParseLine(st.raw)
					if err != nil {
						t.Fatalf("%q: %v", st.raw, err)
					}
					nick, _, _ := strings.Cut(m.Source, "!")
					h.apply(m)
					ev = EventFromMessage(h.net, m, strings.EqualFold(nick, h.me))
				}
				actions, halted := e.Dispatch(ev, h)
				got := specRender(actions)
				if !(len(got) == 0 && len(st.want) == 0) && !slices.Equal(got, st.want) || halted != st.halted {
					t.Errorf("step %d %q:\n got  %q halted=%v\n want %q halted=%v", i, st.raw, got, halted, st.want, st.halted)
				}
			}
		})
	}
}

func TestSpecTextEvents(t *testing.T) {
	// For the matchtext table: each pattern on #trout, with texts that must
	// and must not trigger it.
	matchCase := func(pattern string, hits, misses []string) specEvent {
		c := specEvent{name: "matchtext " + pattern, src: "on 1:TEXT:" + pattern + ":#:/msg $chan hit"}
		for _, s := range hits {
			c.steps = append(c.steps, specStep{raw: bob + "PRIVMSG #trout :" + s, want: []string{"/msg #trout hit"}})
		}
		for _, s := range misses {
			c.steps = append(c.steps, specStep{raw: bob + "PRIVMSG #trout :" + s})
		}
		return c
	}
	runSpecEvents(t, []specEvent{
		// "on 1:TEXT:*help*:#mirc,#irchelp:/msg $nick what is the problem?"
		{name: "help example", src: "on 1:TEXT:*help*:#mirc,#irchelp:/msg $nick what is the problem?", steps: []specStep{
			{raw: bob + "PRIVMSG #mirc :I need help please", want: []string{"/msg bob what is the problem?"}},
			{raw: bob + "PRIVMSG #irchelp :help", want: []string{"/msg bob what is the problem?"}},
			{raw: bob + "PRIVMSG #other :I need help please"},
			{raw: bob + "PRIVMSG trout :I need help please"},
			{raw: bob + "PRIVMSG #mirc :no assistance"},
		}},
		// "on 1:TEXT:hello*:#:/msg $chan Welcome to $chan $nick!" -- "any
		// channel for any line beginning with the word hello". In mIRC 7.86
		// "$nick!" is an identifier called nick!, which is $null (checked
		// with dev/windows/mirc-run), so the example says less than the help
		// suggests.
		{name: "hello example", src: "on 1:TEXT:hello*:#:/msg $chan Welcome to $chan $nick!", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :hello there", want: []string{"/msg #trout Welcome to #trout"}},
			{raw: bob + "PRIVMSG #trout :say hello"},
			{raw: bob + "PRIVMSG trout :hello there"},
		}},
		// "on 1:TEXT:*cookie*:#food:/describe $chan gives $nick a cookie :)"
		{name: "cookie example", src: "on 1:TEXT:*cookie*:#food:/describe $chan gives $nick a cookie :)", steps: []specStep{
			{raw: bob + "PRIVMSG #food :may I have a cookie now", want: []string{"/describe #food gives bob a cookie :)"}},
			{raw: bob + "PRIVMSG #food :nothing"},
			{raw: bob + "PRIVMSG #trout :cookie"},
		}},
		// "on 1:ACTION:moo:#:/msg $chan Aha, I see we have a cow among us."
		{name: "moo example", src: "on 1:ACTION:moo:#:/msg $chan Aha, I see we have a cow among us.", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("ACTION moo"), want: []string{"/msg #trout Aha, I see we have a cow among us."}},
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("ACTION moo loudly")},
			{raw: bob + "PRIVMSG #trout :moo"},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("ACTION moo")},
		}},
		// "on 1:NOTICE:*:?:/msg $nick I am AFK, back in a moment!" -- "any private notice"
		{name: "notice example", src: "on 1:NOTICE:*:?:/msg $nick I am AFK, back in a moment!", steps: []specStep{
			{raw: bob + "NOTICE trout :hello", want: []string{"/msg bob I am AFK, back in a moment!"}},
			{raw: bob + "NOTICE #trout :hello"},
			{raw: bob + "PRIVMSG trout :hello"},
		}},
		// "on TEXT triggers when you receive private and/or channel messages"; ACTION is its own event.
		{name: "text is not action", src: "on 1:TEXT:*:*:/msg $nick text", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("ACTION waves")},
			{raw: bob + "PRIVMSG trout :hi", want: []string{"/msg bob text"}},
		}},
		// "The location where this event occurs can be specified using: ? for
		// any private message; # for any channel message; #mirc for any
		// messages on channel #mirc; * for any private or channel messages"
		{name: "location *", src: "on 1:TEXT:*:*:/msg $nick got it", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :a", want: []string{"/msg bob got it"}},
			{raw: bob + "PRIVMSG trout :a", want: []string{"/msg bob got it"}},
		}},
		{name: "location ?", src: "on 1:TEXT:*:?:/msg $nick got it", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :a"},
			{raw: bob + "PRIVMSG trout :a", want: []string{"/msg bob got it"}},
		}},
		{name: "location #", src: "on 1:TEXT:*:#:/msg $nick got it", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :a", want: []string{"/msg bob got it"}},
			{raw: bob + "PRIVMSG #other :a", want: []string{"/msg bob got it"}},
			{raw: bob + "PRIVMSG trout :a"},
		}},
		{name: "location #mirc", src: "on 1:TEXT:*:#mirc:/msg $nick got it", steps: []specStep{
			{raw: bob + "PRIVMSG #mirc :a", want: []string{"/msg bob got it"}},
			{raw: bob + "PRIVMSG #trout :a"},
			{raw: bob + "PRIVMSG trout :a"},
		}},
		// The match text table: "* matches any text; & matches any word; text
		// matches if text contains only this word; text* starts with this
		// word; *text ends with this word; *text* contains this word anywhere"
		matchCase("*", []string{"anything at all", "x"}, nil),
		matchCase("hello", []string{"hello"}, []string{"hello there", "oh hello", "goodbye"}),
		matchCase("hello*", []string{"hello there", "hello"}, []string{"oh hello", "goodbye"}),
		matchCase("*bye", []string{"ok bye", "bye"}, []string{"bye now", "hello"}),
		matchCase("*moo*", []string{"a moo here", "moo", "moo moo"}, []string{"a cow here"}),
		matchCase("!seen &", []string{"!seen bob"}, []string{"!seen", "!seen bob alice", "seen bob"}),
		// "you can also use Variables in place of both the matchtext and the
		// channel parameters" -- "on 1:TEXT:%matchtext:%channel:/msg $nick You
		// just said $1- on channel %channel"
		{name: "variables", vars: map[string]string{"matchtext": "foo", "channel": "#trout"},
			src: "on 1:TEXT:%matchtext:%channel:/msg $nick You just said $1- on channel %channel", steps: []specStep{
				{raw: bob + "PRIVMSG #trout :foo", want: []string{"/msg bob You just said foo on channel #trout"}},
				{raw: bob + "PRIVMSG #trout :bar"},
				{raw: bob + "PRIVMSG #other :foo"},
			}},
		// Remote Identifiers: "on 1:TEXT:$(*hello $me $+ *):?:echo hello back!"
		{name: "$() matchtext", src: "on 1:TEXT:$(*hello $me $+ *):?:echo hello back!", steps: []specStep{
			{raw: bob + "PRIVMSG trout :oh hello trout, how are you", want: []string{"hello back!"}},
			{raw: bob + "PRIVMSG trout :hello bob"},
			{raw: bob + "PRIVMSG #trout :hello trout"},
		}},
		// $1-, $0, $2- and the other event identifiers.
		{name: "params", src: "on 1:TEXT:*:#:/msg $chan $0 words: $1 / $2- / $1-", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :one two three", want: []string{"/msg #trout 3 words: one / two three / one two three"}},
		}},
		{name: "address identifiers", src: "on 1:JOIN:#:echo $nick $address $site $wildsite $fulladdress $chan $me $network", steps: []specStep{
			{raw: ":dave!user@host.example JOIN #trout", want: []string{"dave user@host.example host.example *!*@host.example dave!user@host.example #trout trout TestNet"}},
		}},
		// "$chan Returns the name of the channel for a specific event. For all
		// non-channel events $chan will be $null."
		{name: "chan is null in a query", src: "on 1:TEXT:*:?:echo chan $len($chan) nick $nick", steps: []specStep{
			{raw: bob + "PRIVMSG trout :hi", want: []string{"chan 0 nick bob"}},
		}},
		{name: "halt in event", src: "on 1:TEXT:*:#:{\n msg # a\n halt\n msg # b\n}", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :hi", want: []string{"/msg #trout a"}, halted: true},
		}},
		{name: "no halt", src: "on 1:TEXT:*:#:msg # a", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :hi", want: []string{"/msg #trout a"}},
		}},
		{name: "halt only", src: "on 1:TEXT:secret:#:halt", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :secret", halted: true},
			{raw: bob + "PRIVMSG #trout :public"},
		}},
	})
}

func TestSpecMembershipEvents(t *testing.T) {
	runSpecEvents(t, []specEvent{
		// "on 1:JOIN:#:/msg $chan Welcome $nick" -- "any user joins any channel"
		{name: "join", src: "on 1:JOIN:#:/msg $chan Welcome $nick", steps: []specStep{
			{raw: ":dave!u@h JOIN #trout", want: []string{"/msg #trout Welcome dave"}},
			{raw: ":erin!u@h JOIN #other", want: []string{"/msg #other Welcome erin"}},
		}},
		// "on 1:JOIN:#mirc,#irchelp:/msg $nick hiya!"
		{name: "join locations", src: "on 1:JOIN:#mirc,#irchelp:/msg $nick hiya!", steps: []specStep{
			{raw: ":dave!u@h JOIN #mirc", want: []string{"/msg dave hiya!"}},
			{raw: ":dave!u@h JOIN #irchelp", want: []string{"/msg dave hiya!"}},
			{raw: ":dave!u@h JOIN #trout"},
		}},
		// "on 1:PART:#mIRC,#newbies:/describe $chan waves bye-bye to $nick *sniff*"
		{name: "part", src: "on 1:PART:#mIRC,#newbies:/describe $chan waves bye-bye to $nick *sniff*", steps: []specStep{
			{raw: ":bob!u@h PART #mIRC", want: []string{"/describe #mIRC waves bye-bye to bob *sniff*"}},
			{raw: ":bob!u@h PART #newbies :bye", want: []string{"/describe #newbies waves bye-bye to bob *sniff*"}},
			{raw: ":bob!u@h PART #trout"},
		}},
		// "on me:*:JOIN:#:/msg # Hello to one and all!" -- "will only trigger when you join a channel"
		{name: "me prefix", src: "on me:*:JOIN:#:/msg # Hello to one and all!", steps: []specStep{
			{raw: ":dave!u@h JOIN #trout"},
			{raw: ":trout!u@h JOIN #new", want: []string{"/msg #new Hello to one and all!"}},
		}},
		// "The ! prefix: prevent an event from being triggered if it was initiated by you"
		{name: "! prefix", src: "on !1:JOIN:#:/msg # hi $nick", steps: []specStep{
			{raw: ":trout!u@h JOIN #new"},
			{raw: ":dave!u@h JOIN #trout", want: []string{"/msg #trout hi dave"}},
		}},
		// "The @ prefix: limit events to being executed only when you have Ops on a channel"
		{name: "@ prefix", src: "on @1:JOIN:#:/mode $chan +o $nick",
			setup: func(h *fakeHost) { h.chans["#other"] = []Nick{{Name: "trout"}} },
			steps: []specStep{
				{raw: ":dave!u@h JOIN #trout", want: []string{"/mode #trout +o dave"}},
				{raw: ":dave!u@h JOIN #other"},
			}},
		// The me prefix on PART: only our own.
		{name: "me prefix part", src: "on me:*:PART:#:/echo Left $chan", steps: []specStep{
			{raw: ":bob!u@h PART #trout"},
			{raw: ":trout!u@h PART #trout", want: []string{"Left #trout"}},
		}},
		// "The * prefix: allow any user to trigger an event regardless of their access level"
		{name: "* prefix", src: "on *:TEXT:help:#:/msg $nick you have accessed a * level event", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :help", want: []string{"/msg bob you have accessed a * level event"}},
		}},
		// "on 1:KICK:#mIRC:/msg $chan $nick kicked $knick" -- "$knick refers to
		// the nickname of the user who was kicked."
		{name: "kick", src: "on 1:KICK:#:/msg $chan $nick kicked $knick", steps: []specStep{
			{raw: ":alice!a@h KICK #trout bob :behave", want: []string{"/msg #trout alice kicked bob"}},
		}},
		// "on 5:KICK:#:/invite $knick $chan | /msg $nick Hey, $knick is my friend!" at level 1.
		{name: "kick two commands", src: "on 1:KICK:#:/invite $knick $chan | /msg $nick Hey, $knick is my friend!", steps: []specStep{
			{raw: ":alice!a@h KICK #trout bob :x", want: []string{"/invite bob #trout", "/msg alice Hey, bob is my friend!"}},
		}},
		{name: "kick location", src: "on 1:KICK:#mirc:/msg $chan $knick", steps: []specStep{
			{raw: ":alice!a@h KICK #trout bob :x"},
		}},
		// "on 1:QUIT:/notice $me $nick just quit IRC with the message $1-"
		{name: "quit", src: "on 1:QUIT:/notice $me $nick just quit IRC with the message $1-", steps: []specStep{
			{raw: ":bob!u@h QUIT :Gone fishing", want: []string{"/notice trout bob just quit IRC with the message Gone fishing"}},
		}},
		// "on 1:QUIT:/ame waves bye-bye to $nick *sniff*"
		{name: "quit ame", src: "on 1:QUIT:/ame waves bye-bye to $nick *sniff*", steps: []specStep{
			{raw: ":bob!u@h QUIT :bye", want: []string{"/ame waves bye-bye to bob *sniff*"}},
		}},
		// "on 1:NICK:/echo $newnick was previously known as $nick"
		{name: "nick echo", src: "on 1:NICK:/echo $newnick was previously known as $nick", steps: []specStep{
			{raw: ":bob!u@h NICK :robert", want: []string{"robert was previously known as bob"}},
		}},
		// "on 1:NICK:/describe $newnick thinks $nick was a nicer nickname!"
		{name: "nick describe", src: "on 1:NICK:/describe $newnick thinks $nick was a nicer nickname!", steps: []specStep{
			{raw: ":bob!u@h NICK :robert", want: []string{"/describe robert thinks bob was a nicer nickname!"}},
		}},
		// "on 1:TOPIC:#mIRC4Dummies:/describe $chan admires $nick $+ 's new topic!"
		// -- "The $1- parameters hold the actual text of the new topic."
		{name: "topic", src: "on 1:TOPIC:#mIRC4Dummies:/describe $chan admires $nick $+ 's new topic!", steps: []specStep{
			{raw: ":bob!u@h TOPIC #mIRC4Dummies :Fresh topic", want: []string{"/describe #mIRC4Dummies admires bob's new topic!"}},
			{raw: ":bob!u@h TOPIC #trout :Fresh topic"},
		}},
		{name: "topic text", src: "on 1:TOPIC:#:/msg $chan $1-", steps: []specStep{
			{raw: ":bob!u@h TOPIC #trout :Fresh topic here", want: []string{"/msg #trout Fresh topic here"}},
		}},
		// "on 1:INVITE:#mIRC:/join $chan"
		{name: "invite", src: "on 1:INVITE:#mIRC:/join $chan", steps: []specStep{
			{raw: ":bob!u@h INVITE trout #mIRC", want: []string{"/join #mIRC"}},
			{raw: ":bob!u@h INVITE trout #other"},
		}},
		{name: "invite any", src: "on 1:INVITE:#:/join $chan | /msg $nick thanks", steps: []specStep{
			{raw: ":bob!u@h INVITE trout #other", want: []string{"/join #other", "/msg bob thanks"}},
		}},
		// "on 1:CONNECT:/join #new2irc"
		{name: "connect", src: "on 1:CONNECT:/join #new2irc", steps: []specStep{
			{ev: &Event{Kind: "CONNECT", Network: "TestNet"}, want: []string{"/join #new2irc"}},
		}},
		{name: "connect time", src: "on 1:CONNECT:/echo Connected at $time", steps: []specStep{
			{ev: &Event{Kind: "CONNECT", Network: "TestNet"}, want: []string{"Connected at 15:04:05"}},
		}},
	})
}

func TestSpecCtcpEvents(t *testing.T) {
	runSpecEvents(t, []specEvent{
		// "ctcp 1:help:*:/msg $nick help yourself!" -- "either in a channel or private message"
		{name: "basic", src: "ctcp 1:HELP:*:/msg $nick help yourself!", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("HELP"), want: []string{"/msg bob help yourself!"}},
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("HELP"), want: []string{"/msg bob help yourself!"}},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("OTHER")},
		}},
		// "ctcp 1:ping:?:/notice $nick Ouch! | /halt" -- "/halt ... prevents
		// the standard ping reply"
		{name: "ping halts", src: "ctcp 1:PING:?:/notice $nick Ouch! | /halt", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("PING 12345"), want: []string{"/notice bob Ouch!"}, halted: true},
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("PING 12345")},
		}},
		// "If you do not use the /halt, the standard reply to PING will be sent."
		{name: "ping without halt", src: "ctcp 1:PING:?:/notice $nick Ouch!", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("PING 12345"), want: []string{"/notice bob Ouch!"}},
		}},
		// "ctcp 1:time:?:/notice $nick The time here is around $time | /halt"
		{name: "time", src: "ctcp 1:TIME:?:/notice $nick The time here is around $time | /halt", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("TIME"), want: []string{"/notice bob The time here is around 15:04:05"}, halted: true},
		}},
		// "ctcp 5:opme:?:/mode $2 +o $nick" -- /ctcp yournick opme #mIRC, at level 1.
		{name: "opme", src: "ctcp 1:opme:?:/mode $2 +o $nick", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("opme #mIRC"), want: []string{"/mode #mIRC +o bob"}},
		}},
		// "ctcp 1:*help*:#:/notice $nick I can see that you need some help"
		{name: "wildcard channel only", src: "ctcp 1:*help*:#:/notice $nick I can see that you need some help", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("NEEDHELP now"), want: []string{"/notice bob I can see that you need some help"}},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("NEEDHELP now")},
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("PING")},
		}},
		// "By using the * and ? wildcard characters, you can match any incoming text."
		{name: "question mark wildcard", src: "ctcp 1:h?lp:*:/msg $nick ok", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("HELP"), want: []string{"/msg bob ok"}},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("HLP")},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("HEELP")},
		}},
		// "ctcp 1:%password:?:/notice $nick Your access has been authorized" --
		// "if you set %password to the value "moo" and someone sends you a
		// "moo" ctcp, it will match"
		{name: "variable", vars: map[string]string{"password": "moo"}, src: "ctcp 1:%password:?:/notice $nick Your access has been authorized", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("moo"), want: []string{"/notice bob Your access has been authorized"}},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("cow")},
		}},
		// "the *#? specify whether to react to any message, to channel messages,
		// or to private messages"
		{name: "star location", src: "ctcp 1:FOO:*:/msg $nick got", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("FOO"), want: []string{"/msg bob got"}},
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("FOO"), want: []string{"/msg bob got"}},
		}},
		{name: "private location", src: "ctcp 1:FOO:?:/msg $nick got", steps: []specStep{
			{raw: bob + "PRIVMSG #trout :" + ctcpRaw("FOO")},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("FOO"), want: []string{"/msg bob got"}},
		}},
		// "ctcp !2:HELP:*:... You would be unable to access the above event" -- at level 1, for ourselves.
		{name: "! prefix", src: "ctcp !1:FOO:*:/msg $nick got", steps: []specStep{
			{raw: ":trout!u@h PRIVMSG bob :" + ctcpRaw("FOO")},
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("FOO"), want: []string{"/msg bob got"}},
		}},
		// An ACTION is not a ctcp event.
		{name: "action is not a ctcp", src: "ctcp 1:*:*:/msg $nick ctcp", steps: []specStep{
			{raw: bob + "PRIVMSG trout :" + ctcpRaw("ACTION waves")},
		}},
	})
}

// "You can use the /enable and /disable commands to enable or disable
// groups. A group that is disabled will be ignored when processing events."
func TestSpecGroups(t *testing.T) {
	src := `#one off
on 1:JOIN:#c1:/msg # one
#one end
#two on
on 1:JOIN:#c2:/msg # two
#two end
#helpa off
on 1:JOIN:#c3:/msg # helpa
#helpa end
#helpb off
on 1:JOIN:#c4:/msg # helpb
#helpb end
alias en enable $1-
alias dis disable $1-
`
	e := specEngine(nil)
	if !specLoad(t, e, "g.mrc", src) {
		return
	}
	h := evalHost()
	join := func(c string) []string {
		a, _ := e.Dispatch(Event{Kind: "JOIN", Network: "TestNet", Nick: "dave", Chan: c}, h)
		return specRender(a)
	}
	alias := func(name, args string) []string {
		a, ok := e.Alias("TestNet", "#trout", name, args, h)
		if !ok {
			t.Errorf("no alias %s", name)
		}
		return specRender(a)
	}
	check := func(what string, got, want []string) {
		t.Helper()
		if !(len(got) == 0 && len(want) == 0) && !slices.Equal(got, want) {
			t.Errorf("%s: got %q, want %q", what, got, want)
		}
	}
	// "#group1 on" / "#group1 off" start groups on or off.
	check("#one starts off", join("#c1"), nil)
	check("#two starts on", join("#c2"), []string{"/msg #c2 two"})
	// "/enable #one #two #three"
	alias("en", "#one")
	check("#one enabled", join("#c1"), []string{"/msg #c1 one"})
	// "/disable #one #two #three"
	alias("dis", "#one #two")
	check("#one disabled", join("#c1"), nil)
	check("#two disabled", join("#c2"), nil)
	// "/enable #help* ... enable all matching groups"
	check("#helpa before", join("#c3"), nil)
	alias("en", "#help*")
	check("#helpa wildcard", join("#c3"), []string{"/msg #c3 helpa"})
	check("#helpb wildcard", join("#c4"), []string{"/msg #c4 helpb"})
	check("#one untouched by #help*", join("#c1"), nil)
	// "/disable #help*"
	alias("dis", "#help*")
	check("#helpa wildcard off", join("#c3"), nil)
	check("#helpb wildcard off", join("#c4"), nil)
	// Groups apply "in all scripts".
	specLoad(t, e, "h.mrc", "#two2 off\non 1:JOIN:#c5:/msg # two2\n#two2 end")
	check("other script's group off", join("#c5"), nil)
	alias("en", "#two2")
	check("other script's group on", join("#c5"), []string{"/msg #c5 two2"})
}

// Remote Identifiers: "$group(#test) returns on or off for group #test".
func TestSpecGroupIdentifier(t *testing.T) {
	e := specEngine(nil)
	if !specLoad(t, e, "g.mrc", "#one off\non 1:JOIN:#c1:/msg # one\n#one end\n#two on\non 1:JOIN:#c2:/msg # two\n#two end\nalias grp echo -a $group($1)\nalias en enable $1-\nalias dis disable $1-") {
		return
	}
	h := evalHost()
	run := func(name, args string) []string {
		a, _ := e.Alias("TestNet", "#trout", name, args, h)
		return specRender(a)
	}
	for _, c := range []struct {
		step, name, args string
		want             []string
	}{
		{"#one starts off", "grp", "#one", []string{"off"}},
		{"#two starts on", "grp", "#two", []string{"on"}},
		{"enable", "en", "#one", nil},
		{"#one now on", "grp", "#one", []string{"on"}},
		{"disable", "dis", "#one #two", nil},
		{"#one off again", "grp", "#one", []string{"off"}},
		{"#two off", "grp", "#two", []string{"off"}},
	} {
		got := run(c.name, c.args)
		if !(len(got) == 0 && len(c.want) == 0) && !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.step, got, c.want)
		}
	}
}
