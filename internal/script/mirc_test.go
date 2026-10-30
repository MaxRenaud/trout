//go:build mirc

package script

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMIRC runs evalCases and condCases in real mIRC, on the Windows test
// VM (dev/windows/mirc-run), and checks that mIRC agrees with what the
// engine's tests expect. Cases that depend on the client (our nick, the
// nicklist, the time, $rand, the current channel) are left out: the
// script runs with no connection.
//
//	go test -tags mirc ./internal/script -run TestMIRC -v
func TestMIRC(t *testing.T) {
	vars := maps.Clone(evalVars)
	maps.Copy(vars, condVars)
	e := New(Options{})
	e.Restore(vars)

	// Each line is checked as the engine evaluates it, so this also covers
	// the probes, which have no expected value of their own.
	type check struct{ id, line, want string }
	var checks []check
	add := func(id, line string) {
		if !hostFree(line) {
			return
		}
		r := e.newRun(evalHost())
		v, err := r.eval(&frame{network: "TestNet", params: evalParams}, line)
		if err != nil {
			v = "!error"
		}
		checks = append(checks, check{id, line, v})
	}
	for i, c := range evalCases {
		add(fmt.Sprintf("e%d", i), c.in)
	}
	for i, c := range condCases {
		add(fmt.Sprintf("c%d", i), "$iif("+c.in+",yes,no)")
	}
	for i, line := range mircProbes {
		add(fmt.Sprintf("p%d", i), line)
	}

	var b strings.Builder
	b.WriteString("on *:START:{\n  unsetall\n  write -c out.txt\n")
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		fmt.Fprintf(&b, "  set %%%s %s\n", name, literal(vars[name]))
	}
	for _, c := range checks {
		fmt.Fprintf(&b, "  %s %s\n", c.id, strings.Join(evalParams, " "))
	}
	b.WriteString("  exit -n\n}\n")
	// Each case is its own alias, so an error in one doesn't stop the rest.
	for _, c := range checks {
		fmt.Fprintf(&b, "alias -l %s {\n  write out.txt %s $chr(9) %s\n  return\n  :error\n  write out.txt %s $chr(9) !error $error\n  reseterror\n}\n",
			c.id, c.id, c.line, c.id)
	}

	script := filepath.Join(t.TempDir(), "cases.mrc")
	if err := os.WriteFile(script, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("../../dev/windows/mirc-run", script).Output()
	if err != nil {
		t.Fatalf("mirc-run: %v", err)
	}
	got := map[string]string{}
	for line := range strings.Lines(string(out)) {
		id, v, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "\t")
		if ok {
			v = strings.TrimPrefix(v, " ")
			if strings.HasPrefix(v, "!error") {
				// Errors match errors: their messages differ.
				t.Logf("%s: mIRC: %s", id, v)
				v = "!error"
			}
			got[strings.TrimSpace(id)] = v
		}
	}
	for _, c := range checks {
		v, ok := got[c.id]
		switch {
		case !ok:
			t.Errorf("%s: mIRC wrote nothing for %s", c.id, c.line)
		case v != c.want:
			t.Errorf("%s = %q in mIRC, %q in trout", c.line, v, c.want)
		}
	}
	t.Logf("%d cases checked against mIRC", len(checks))
}

// mircProbes are lines whose result the help doesn't settle: TestMIRC
// compares the engine with mIRC on them.
var mircProbes = []string{
	// Brackets and $+.
	"[ %x ]", "a $+ $null $+ b", "$+ a", "a $+", "a $+ $+ b",
	// Text glued around identifiers and variables.
	"x$len(ab)", "a-$len(ab)", "($len(ab))", "$len(ab)!", "$len(ab)'s", "$null!", "%n!", "%n's",
	"%n.", "$cr$lf", "a,$len(ab)",
	// Numbers.
	"$calc(1 / 3)", "$calc(2 / 3)", "$calc(0.1 + 0.2)", "$calc(2 ^ 53)", "$calc(10 ^ 20)",
	"$calc(-2 ^ 2)", "$calc(2 * -3)", "$calc(5 % -3)", "$calc(-7 % 3)", "$calc(1 + )", "$calc(abc)",
	"$calc(3 4)", "$calc(1,000)", "$calc(1e3)", "$calc(0x10)", "$calc((1 + 2)", "$calc(7 // 2)",
	"$round(2.5,0)", "$round(3.5,0)", "$round(-2.5,0)", "$round(1.005,2)", "$int(-3.7)", "$abs(-0)",
	"$calc(1.10)", "$calc(100000000)", "$calc(0.0000001)", "$calc(1 / 7 * 7)",
	// Strings.
	"$left(abc,0)", "$right(abc,0)", "$left(abc,9)", "$mid(abcdef,0,2)", "$mid(abcdef,-2,1)",
	"$mid(abcdef,2,-1)", "$mid(abcdef,3,0)", "$str(ab,0)", "$len()", "$upper(straße)",
	"$replace(abc,a,b,b,c)", "$replace(aaa,a,aa)", "$replace(abc,,x)", "$remove(aabb,ab)",
	"$pos(aaa,aa,0)", "$count(aaa,aa)", "$count(abcabc,a,b)", "$chr(0)", "$asc()", "$asc(ab)",
	"$replace(a b,$chr(32),_)", "$strip(%colored,b)",
	// Tokens.
	"$gettok(a.b.c,0,46)", "$gettok(a.b.c,-2,46)", "$gettok(a.b.c,-2-,46)", "$gettok(a.b.c,2-1,46)",
	"$gettok(a.b.c,4-,46)", "$gettok(.a..b.,2,46)", "$gettok(a b,1,32)", "$numtok(,46)",
	"$deltok(a.b.c,-1,46)", "$deltok(a.b.c,5,46)", "$instok(a.b.c,x,0,46)", "$instok(a.b.c,x,-1,46)",
	"$puttok(a.b.c,x,-1,46)", "$puttok(a.b.c,x,9,46)", "$addtok(,a,46)", "$remtok(a.b.a,a,46)",
	"$reptok(a.b.a,a,x,46)", "$sorttok(b.A.a.B,46)", "$sorttokcs(b.A.a.B,46)", "$sorttok(10.9.100,46)",
	"$sorttok(10.9.100,46,n)", "$sorttok(@b.+a.c,46,c)", "$sorttok(a10.a9.a100,46,a)",
	"$wildtok(one two three,&e,0,32)", "$matchtok(one two three,O,1,32)", "$findtok(a.A.a,a,0,46)",
	"$istok(a.b,,46)", "$gettok(a.b.c,2,0)",
	// Parameters.
	"$1-2", "$3-", "$5-", "$0", "$4-9", "$2-1",
	// $eval and $iif.
	"$eval(%x %x,2)", "$eval($!len(ab),2)", "$iif(0,a,b)", "$iif($false,a)", "$iif(%nope,a,b)",
	"$iif(1 == 1,a b,c)", "$iif(,a,b)", "$(%n)",
	// $duration.
	"$duration(59)", "$duration(60)", "$duration(86399)", "$duration(31536000)", "$duration(-5)",
	"$duration(1.5)", "$duration(3600,3)", "$duration(90000,3)", "$duration(1wk)", "$duration(1hr 30mins)",
	"$duration(01:30:00)", "$duration(5 secs)",
	// Conditions.
	"$iif(abc < ABD,y,n)", "$iif(10 < 9a,y,n)", "$iif(1e3 == 1000,y,n)", "$iif(0x10 == 16,y,n)",
	"$iif(-5 < 3,y,n)", "$iif(a == ,y,n)", "$iif( == ,y,n)", "$iif(-1 isnum,y,n)", "$iif(1.5 isnum,y,n)",
	"$iif(.5 isnum,y,n)", "$iif(1. isnum,y,n)", "$iif(+1 isnum,y,n)", "$iif(5 isnum 5-,y,n)",
	"$iif(5 isnum -6,y,n)", "$iif(ab isletter abc,y,n)", "$iif(b isletter abc,y,n)", "$iif(1 isletter,y,n)",
	"$iif($null isalpha,y,n)", "$iif(ABC islower,y,n)", "$iif(aB1 isalnum,y,n)", "$iif(é isalpha,y,n)",
	"$iif(2 & 1,y,n)", "$iif(0 // 5,y,n)", "$iif(a iswm A,y,n)", "$iif(* iswm $null,y,n)",
	"$iif(%n == 5.0,y,n)", "$iif(05 === 5,y,n)", "$iif(a != A,y,n)", "$iif(!0,y,n)", "$iif(!$null,y,n)",
	"$iif($true,y,n)", "$iif(0.0,y,n)", "$iif(00,y,n)", "$iif(-0,y,n)", "$iif( ,y,n)",
	"$iif(*& bob iswm hi bob,y,n)", "$iif(*a*b iswm xaxxbx,y,n)", "$iif(& iswm $null,y,n)", "$iif(?é iswm aé,y,n)",
	// Where an identifier's name ends, and identifiers that don't exist.
	"$version!", "$version's", "$false!", "$false's", "$false,", "$false.", "$false.x", "$true.foo",
	"$nosuchthing", "$nosuchthing(1)", "a $nosuchthing b", "$len(ab)x", "$len(ab).x", "$upper(a).foo",
	"$1!", "$1's", "$1,", "$1.", "$2-x", "$cr.", "$len(ab)$len(abc)", "$chr(65)$chr(66)",
	"$false$true", "x$false", "$false-$true",
}

// hostFree reports whether a case can run in mIRC with no connection: it
// doesn't use our nick, a nicklist, the clock, $rand or #.
func hostFree(line string) bool {
	for _, s := range []string{"$me", "$network", "$nick", "$time", "$date", "$adate", "$fulldate",
		"$ctime", "$asctime", "$rand", "$active", "isop", "isvoice", "isreg", "ison", "ischan"} {
		if strings.Contains(line, s) {
			return false
		}
	}
	for w := range strings.FieldsSeq(line) {
		if w == "#" || strings.HasPrefix(w, "#$") {
			return false
		}
	}
	return true
}

// literal is an mIRC expression for s, character by character, so /set
// stores it exactly: $+($chr(37),$chr(121)) is %y.
func literal(s string) string {
	var parts []string
	for _, r := range s {
		parts = append(parts, fmt.Sprintf("$chr(%d)", r))
	}
	return "$+(" + strings.Join(parts, ",") + ")"
}

// mircScripts are alias bodies that write what they do with w (an alias
// that /writes a line); TestMIRCScripts runs each in mIRC and in trout and
// compares what they wrote.
var mircScripts = []string{
	"%x = 5 + 1 | w %x",
	"%x = 5 + 1 + 1 | w %x",
	"%x = 2 ^ 3 | w %x",
	"%x = 7 % 3 | w %x",
	"%x = 10 / 4 | w %x",
	"%x = a + 1 | w %x",
	"%x = 1+1 | w %x",
	"%x = $null | w [ %x ] .",
	"set %x 1 + 2 | w %x",
	"set -n %x 1 + 2 | w %x",
	"set %x | w a %x b",
	"set %y 1 + 2 | set %x %y | w %x",
	"var %a = 1, %b = 2, %c | w %a %b %c .",
	"var %a 1 | w %a",
	"var %a = $gettok(a b c,2,32), %b = x | w %a %b",
	"var %a = 1 | var %a = 2 | w %a",
	"inc %n | inc %n 5 | dec %n 2 | w %n",
	"inc %n 1.5 | w %n",
	"set %s abc | inc %s | w %s",
	"var %i = 1 | while (%i <= 3) { w %i | inc %i }",
	"var %i = 0 | while (%i < 5) { inc %i | if (%i == 2) continue | if (%i == 4) break | w %i }",
	// Labels go on lines of their own: mIRC's goto goes to a label's line,
	// so ":top" after a | loops forever.
	"var %i = 1\n:top\nw %i | inc %i\nif (%i <= 2) goto top",
	"goto b\n:a\nw a\nreturn\n:b\nw b\ngoto a", // not halt: that would stop on START too
	"if (1) w yes | else w no",
	"if (0) { w yes } | elseif (1) { w elseif } | else { w no }",
	"if (0) w a | elseif (0) w b | else w c",
	"if 1 == 1 w bare",
	"if (%nope == $null) w empty",
	"if ($v1 == x) w a | w v1= $+ $v1 v2= $+ $v2",
	"if (abc isin xabcx) w $v1 $v2",
	"tokenize 46 a.b.c | w $0 $2",
	"tokenize 32 $null | w $0 .",
	"w $calc(2 + 2) [ $+ x ]",
	"set %k.a 1 | set %k.b 2 | unset %k.* | w %k.a %k.b .",
	"set %a1 x | w %a [ $+ 1 ]",
	"set %v hi | w $eval(% $+ v,2)",
	"noop $calc(1 + 1) | w $result .",
	"r 3 | w $result",
	"w $r(4)",
	"w $iif($isid,id,cmd) | s",
	"w a | return | w b",
	".w dot",
	// Hash tables (item order is mIRC's hash order, so these don't depend on it).
	"hmake t 10 | hadd t a 1 | hadd t B two words | w $hget(t,a) $hget(t,b) $hget(t,0).item $hget(t) $hget(t).size $hget(0)",
	"hmake t | hadd t a 1 | w $hfind(t,A,1)",
	"hmake t | hadd t a 1 | w $hfind(t,*,0,w)",
	"hmake t | hadd t a 1 | w $hfind(t,1,1).data",
	"hmake t | hadd t a 1 | w $hfind(t,x,1) .",
	"hmake t | hadd t ab 1 | w $hfind(t,a*,1,w) $hfind(t,b,1,W) $hfind(t,/^a/,1,r)",
	"hmake t 1 | hmake u 12 | hmake v 13 | hmake w 100 | hmake x | w $hget(t).size $hget(u).size $hget(v).size $hget(w).size $hget(x).size",
	"hinc -m t x | hinc t x 5 | hdec t x | w $hget(t,x)",
	"hadd nosuch a 1 | w after",
	"hadd -m t a 1 | hdel t a | w $hget(t,a) $hget(t,0).item",
	"hadd -m t aa 1 | hadd t ab 2 | hadd t b 3 | hdel -w t a* | w $hget(t,0).item $hget(t,b)",
	"hmake t | hmake t | w after",
	"hfree nosuch | w after",
	"hadd -m t a 1 | hadd t b 2 | hsave t t.hsh | hfree t | hmake t | hload t t.hsh | w $hget(t,a) $hget(t,b)",
	"hadd -m t k v | hsave -n t n.txt | w $read(n.txt,1)",
	"w $hget(nosuch,a) $hget(nosuch) .",
	// $read and /write.
	"write -c f.txt a | write f.txt b $!me | w $read(f.txt,0) $read(f.txt,1) $read(f.txt,n,2) $lines(f.txt)",
	"write -c f.txt 2 | write f.txt x | write f.txt y | w $read(f.txt,1) $read(f.txt,2) $lines(f.txt)",
	"write -c f.txt one 1 | write f.txt two 2 | w $read(f.txt,s,two) $read(f.txt,w,o*1) $read(f.txt,r,/^t/)",
	"write -c e.txt x | w $read(missing.txt,1) $exists(missing.txt) $exists(e.txt)",
	// Regular expressions.
	"w $regex(abc123,/(\\d+)/) $regml(1) $regml(0)",
	"w $regex(a1b22c333,/(\\d+)/g) $regml(0) $regml(2)",
	"w $regex(ABC,/abc/i) $regex(ABC,/abc/) $regex(abc,abc) $regex(abc,m/b/)",
	"w $regex(n1,ab,/(a)(b)/) $regml(n1,2) $regml(n1,0)",
	"w $regex(abc,/(x)?abc/) $regml(0)",
	"w $regex(abc,/b/) $regml(0)",
	"w $regex(aaa,/a/g)",
	"w $regsubex(abc,/b/,X) $regsubex(a1b2,/(\\d)/g,<\\1>) $regsubex(abc,/(.)/g,\\n)",
	"w $regsubex(ab,/(.)/g,$upper(\\1))",
	"noop $regsub(hello,/l/g,L,%r) | w %r $regsub(hello,/l/,L,%s) %s",
	"w $regex(\x02bold,/^bold/) $regex(\x02bold,/^bold/S)",
	"w $regex(a b,/a b/x) $regex(ab,/a b/x) $regex(a b,/[ ]/x) $regex(#12,/[#]\\d+/x)",
	"hmake abc | hmake 2 | hadd 2 k v | hadd abc k other | w $hget(2,k) $hget(abc,k) $hget(0)",
}

// Known differences, not checked: mIRC's PCRE has lookarounds and
// backreferences, which trout's RE2 doesn't ($regex(ab,/(?<=a)b/) is an
// error in trout); and mIRC's hash table order, which scripts aren't to
// depend on, is its hash's, where trout's is the order items were added.

// mircScriptHelpers are the aliases mircScripts call.
const mircScriptHelpers = `alias -l w write out.txt %case $chr(9) $1-
alias -l r return $calc($1 * 2)
alias -l s w $iif($isid,id,cmd)
`

func TestMIRCScripts(t *testing.T) {
	var src strings.Builder
	src.WriteString("on *:START:{\n  unsetall\n  write -c out.txt\n")
	for i := range mircScripts {
		fmt.Fprintf(&src, "  unsetall\n  hfree -w *\n  set %%case s%d\n  s%d\n", i, i)
	}
	src.WriteString("  exit -n\n}\n" + mircScriptHelpers)
	for i, body := range mircScripts {
		fmt.Fprintf(&src, "alias -l s%d {\n  %s\n  return\n  :error\n  w !error\n  reseterror\n}\n", i, strings.ReplaceAll(body, "\n", "\n  "))
	}
	script := filepath.Join(t.TempDir(), "scripts.mrc")
	if err := os.WriteFile(script, []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("../../dev/windows/mirc-run", script).Output()
	if err != nil {
		t.Fatalf("mirc-run: %v", err)
	}
	inMIRC := map[string][]string{}
	for line := range strings.Lines(string(out)) {
		id, v, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "\t")
		if ok {
			id = strings.TrimSpace(id)
			inMIRC[id] = append(inMIRC[id], strings.TrimPrefix(v, " "))
		}
	}

	// The same aliases in trout, one Engine each, with /write turned into
	// the lines it would write.
	for i, body := range mircScripts {
		id := fmt.Sprintf("s%d", i)
		e := New(Options{})
		src := "alias t {\n  set %case " + id + "\n  " + id + "\n}\n" + mircScriptHelpers +
			fmt.Sprintf("alias -l %s {\n  %s\n  return\n  :error\n  w !error\n  reseterror\n}\n", id, strings.ReplaceAll(body, "\n", "\n  "))
		for _, p := range e.Load("s.mrc", src) {
			if p.Fatal {
				t.Fatalf("%s: %v", body, p)
			}
		}
		actions, _ := e.Alias("TestNet", "", "t", "", evalHost())
		var got []string
		for _, a := range actions {
			switch {
			case a.File != nil && a.File.Name == "out.txt":
				_, v, _ := strings.Cut(strings.TrimRight(a.File.Data, "\r\n"), "\t")
				got = append(got, strings.TrimPrefix(v, " "))
			case a.Echo != nil:
				got = append(got, "!error")
			}
		}
		if want := inMIRC[id]; !slices.Equal(got, want) {
			t.Errorf("%s\n  mIRC:  %q\n  trout: %q", body, want, got)
		}
	}
}

// mircTimers starts timers in on START and writes what they see when they
// run: their command's second evaluation, $ctimer, $timer().reps, an
// automatic name, and one timer stopping another.
const mircTimers = `on *:START:{
  write -c out.txt
  set %x 1
  .timerA 1 0 write out.txt A %x $!ctimer $!timer(0)
  set %x 2
  .timerB 5 1 write out.txt B $!timer(B).reps %x
  .timerC -m 1 2500 tc
  .timer 1 3 write out.txt auto $!ctimer
  .timerZ 1 4 exit -n
  write out.txt start $timer(0) $ltimer $timer(1) $timer(B).delay $timer(C).delay
}
alias tc {
  write out.txt C $ctimer $timer(B).reps
  .timerB off
  write out.txt C2 $timer(0)
}
`

func TestMIRCTimers(t *testing.T) {
	script := filepath.Join(t.TempDir(), "timers.mrc")
	if err := os.WriteFile(script, []byte(mircTimers), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("../../dev/windows/mirc-run", script).Output()
	if err != nil {
		t.Fatalf("mirc-run: %v", err)
	}
	var want []string
	for line := range strings.Lines(string(out)) {
		want = append(want, strings.TrimRight(line, "\r\n"))
	}

	e := New(Options{})
	if p := e.Load("timers.mrc", mircTimers); len(p) > 0 {
		t.Fatal(p)
	}
	h := evalHost()
	var got []string
	collect := func(actions []Action) (exit bool) {
		for _, a := range actions {
			switch {
			case a.File != nil && a.File.Name == "out.txt" && a.File.Append:
				got = append(got, strings.TrimRight(a.File.Data, "\r\n"))
			case a.Command == "/exit -n":
				exit = true
			case a.Echo != nil:
				got = append(got, "!error "+a.Echo.Text)
			}
		}
		return exit
	}
	exit := collect(e.Init("timers.mrc", true, h))
	for range 100 {
		next, ok := e.NextTimer()
		if exit || !ok {
			break
		}
		if next.After(h.now) {
			h.now = next
		}
		exit = collect(e.Tick(h))
	}
	if !slices.Equal(got, want) {
		t.Errorf("\n  mIRC:  %q\n  trout: %q", want, got)
	}
}
