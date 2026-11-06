package script

import (
	"strings"
	"testing"
)

// TestAliasCommand walks /alias as mIRC does it (each case checked there):
// adding to the first aliases file, a / in front, replacing in any case,
// removing, a named file, one that isn't loaded, a remote's multi-line
// alias, -l, and no name.
func TestAliasCommand(t *testing.T) {
	e := New(Options{})
	if p := e.LoadAs("aliases.ini", "[aliases]\r\nn0=/j /join #$1\r\nn1=big {\r\nn2=  echo -a one\r\nn3=}\r\n", "aliases"); len(p) > 0 {
		t.Fatal(p)
	}
	e.LoadAs("extra.ini", "[aliases]\r\nn0=ex1 echo -a ex1\r\n", "aliases")
	e.Load("remote.mrc", "alias mlt {\n  echo -a one\n}")
	h := evalHost()
	var file string
	run := func(line string) string {
		t.Helper()
		acts, _ := e.Command("TestNet", "", line, true, h) // as in a script
		file = ""
		for _, a := range acts {
			if a.Echo != nil {
				return "error: " + a.Echo.Text
			}
			if a.File != nil {
				file = a.File.Name + ": " + strings.ReplaceAll(a.File.Data, "\r\n", "|")
			}
		}
		return file
	}
	is := func(name string) string { return evalIn(t, e, "$isalias("+name+") $isalias("+name+").alias") }

	if got := run("alias hello echo -a hi $!me"); got != "aliases.ini: [aliases]|n0=/j /join #$1|n1=big {|n2=  echo -a one|n3=}|n4=hello echo -a hi $me|" {
		t.Errorf("add: %s", got)
	}
	if got := is("hello"); got != "$true echo -a hi $me" {
		t.Errorf("hello: %s", got)
	}
	run("alias /hello2 /echo -a two")
	run("alias HELLO echo -a replaced")
	if got := is("hello") + " / " + is("hello2"); got != "$true echo -a replaced / $true /echo -a two" {
		t.Errorf("replace and /: %s", got)
	}
	run("alias hello")
	if got := run("alias nosuch"); is("hello") != "$false" || got != "" {
		t.Errorf("remove: %s, %q", is("hello"), got)
	}
	if got := run("alias extra.ini ex2 echo -a ex2"); got != "extra.ini: [aliases]|n0=ex1 echo -a ex1|n1=ex2 echo -a ex2|" {
		t.Errorf("named file: %s", got)
	}
	if got := run("alias notloaded.ini nl echo -a nl"); got != "" || is("nl") != "$false" {
		t.Errorf("a file that isn't loaded: %q, %s", got, is("nl"))
	}
	run("alias big echo -a single")
	if got := is("big"); got != "$true {" {
		t.Errorf("a multi-line alias changed: %s", got)
	}
	run("alias mlt echo -a single")
	if got := evalIn(t, e, "$isalias(mlt).fname"); !strings.HasSuffix(got, "aliases.ini") {
		t.Errorf("mlt is from %s, want the aliases file", got)
	}
	run("alias -l loc echo -a loc")
	if got := is("loc"); got != "$true echo -a loc" {
		t.Errorf("-l: %s", got)
	}
	if got := run("alias"); !strings.Contains(got, "/alias: insufficient parameters") {
		t.Errorf("no name: %s", got)
	}
}

// TestAliasNewFile: with no aliases file loaded, /alias makes aliases.ini
// and has the client load it.
func TestAliasNewFile(t *testing.T) {
	e := New(Options{})
	acts, _ := e.Command("TestNet", "", "alias hi echo -a hi", false, evalHost())
	var got []string
	for _, a := range acts {
		switch {
		case a.File != nil:
			got = append(got, a.File.Name+"="+strings.ReplaceAll(a.File.Data, "\r\n", "|"))
		case a.Command != "":
			got = append(got, a.Command)
		}
	}
	if strings.Join(got, " ; ") != "aliases.ini=[aliases]|n0=hi echo -a hi| ; /.load -a aliases.ini" {
		t.Errorf("actions %q", got)
	}
	if evalIn(t, e, "$isalias(hi)") != "$true" {
		t.Error("hi isn't an alias")
	}
}

// TestIsaliasDef: $isalias().alias in a remote script is the definition's
// first line from the name on, like mIRC's (checked).
func TestIsaliasDef(t *testing.T) {
	e := New(Options{})
	e.Load("ml.mrc", "alias mlt {\n  echo -a one\n}\nalias one echo -a single\nalias braced { echo -a b }")
	for name, want := range map[string]string{"mlt": "mlt {", "one": "one echo -a single", "braced": "braced { echo -a b }"} {
		if got := evalIn(t, e, "$isalias("+name+").alias"); got != want {
			t.Errorf("$isalias(%s).alias = %q, want %q", name, got, want)
		}
	}
}

// TestBraceAfterCommand: { } after a command's first word aren't a block
// but dropped, the words between them the command's, | still splitting,
// like mIRC's (checked with this script).
func TestBraceAfterCommand(t *testing.T) {
	e := New(Options{})
	if p := e.Load("bb.mrc", "alias t1 {\n  set %z 0\n  noop hi { set %z 1 }\n  alias u { set %b 2 }\n  alias v { set %y 3 | set %x 4 }\n}"); len(p) > 0 {
		t.Fatal(p)
	}
	e.Alias("TestNet", "", "t1", "", evalHost())
	got := evalIn(t, e, "z $+([,%z,]) b $+([,%b,]) y $+([,%y,]) x $+([,%x,]) u $+([,$isalias(u).alias,]) v $+([,$isalias(v).alias,])")
	if want := "z [0] b [] y [] x [4] u [set 2] v [set 3]"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
