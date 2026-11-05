package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestScriptClientIdents: on ACTIVE when you change windows ($active and
// $lactive), on APPACTIVE when the terminal gains or loses focus, $color
// from the scheme and the Colors dialog, and $findfile in the scripts
// folder.
func TestScriptClientIdents(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{"c.mrc": `on *:ACTIVE:*:echo -s active $active last $lactive
on *:APPACTIVE:echo -s app $appactive
alias colors echo -s colors $color(normal) $color(action) $color(highlight).dd $color(1)
alias files echo -s files $findfile(logs,*.txt,0) $findfile(logs,*.txt,1)
alias up echo -s up $uptime(mirc,3) $iif($uptime(system) > $uptime(mirc),yes,no) $iif($calc($ticks - $uptime(system)) < 1000,yes,no) $uptime(server)`,
		"logs/a.txt": "x", "logs/old/b.txt": "y"}
	st := a.nets[0].Status()
	a.activate(st)
	a.Update(nil)
	a.edit(st).Buf = []rune("/load -rs c.mrc")
	a.submit(st)
	last := func() string { return lastLine(st) }

	ch := a.nets[0].Wins[1]
	a.activate(ch)
	a.Update(nil)
	if got := last(); got != "active "+ch.Name+" last Status Window" {
		t.Errorf("on ACTIVE: %q", got)
	}
	a.activate(st)
	a.Update(nil)
	if got := last(); got != "active Status Window last "+ch.Name {
		t.Errorf("back to status: %q", got)
	}
	a.Update(tea.BlurMsg{})
	if got := last(); got != "app $false" {
		t.Errorf("on APPACTIVE: %q", got)
	}
	a.Update(tea.FocusMsg{})
	if got := last(); got != "app $true" {
		t.Errorf("focus: %q", got)
	}

	a.themeIdx = 0 // Classic: mIRC's colors, but red highlights
	a.colors = nil
	a.edit(st).Buf = []rune("/colors")
	a.submit(st)
	if got := last(); got != "colors 1 6 04 0" {
		t.Errorf("Classic's colors: %q", got)
	}
	a.colors = parseColors("action=12")
	a.edit(st).Buf = []rune("/colors")
	a.submit(st)
	if got := last(); !strings.HasPrefix(got, "colors 1 12 ") {
		t.Errorf("with action=12: %q", got)
	}
	a.edit(st).Buf = []rune("/files")
	a.submit(st)
	if got := last(); got != "files 2 logs/a.txt" {
		t.Errorf("$findfile: %q", got)
	}
	// $uptime: trout's just started; the computer's been up longer, and
	// $ticks is its uptime; the demo's network has no connection.
	a.edit(st).Buf = []rune("/up")
	a.submit(st)
	if got := last(); got != "up 0 yes yes 0" {
		t.Errorf("$uptime: %q", got)
	}
}

// TestDefenderNote: the first /load on Windows says what Defender does to
// some scripts, once; the setting remembers it, in config.toml. Elsewhere
// it's not said.
func TestDefenderNote(t *testing.T) {
	defer func(g string) { goos = g }(goos)
	for _, sys := range []string{"linux", "windows"} {
		goos = sys
		cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		a := New(Options{Config: cfg})
		a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
		dir := a.scriptsDir()
		if err := errors.Join(os.MkdirAll(dir, 0o755), os.WriteFile(filepath.Join(dir, "a.mrc"), []byte("alias a echo -a a"), 0o644),
			os.WriteFile(filepath.Join(dir, "b.mrc"), []byte("alias b echo -a b"), 0o644)); err != nil {
			t.Fatal(err)
		}
		st := a.home.Status()
		a.activate(st)
		load := func(f string) string {
			a.edit(st).Buf = []rune("/load -rs " + f)
			a.submit(st)
			return lastLine(st)
		}
		first, second := load("a.mrc"), load("b.mrc")
		noted := strings.Contains(first, "Microsoft Defender")
		if noted != (sys == "windows") || strings.Contains(second, "Defender") || a.settings().DefenderNoted != noted {
			t.Errorf("%s: first %q, second %q, setting %v", sys, first, second, a.settings().DefenderNoted)
		}
	}
}

// TestDefenderRisk: on Windows, loading a script with both on START and
// /bwrite warns that Defender may delete it; either alone doesn't.
func TestDefenderRisk(t *testing.T) {
	defer func(g string) { goos = g }(goos)
	goos = "windows"
	for src, want := range map[string]bool{
		"on *:START:{\n  bwrite x.bin 0 -1 &b\n}":         true,
		"ON 1:START:echo hi\nalias s { .bwrite f 0 1 x }": true,
		"on *:START:echo hi":                              false,
		"alias s bwrite f 0 -1 &b":                        false,
		"on *:START:echo bwriter":                         false,
	} {
		if got := defenderRisk(src); got != want {
			t.Errorf("%q: %v, want %v", src, got, want)
		}
	}
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.scriptMem = map[string]string{"risky.mrc": "on *:START:echo hi\nalias s bwrite f 0 -1 &b"}
	st := a.nets[0].Status()
	a.activate(st)
	a.edit(st).Buf = []rune("/load -rs risky.mrc")
	a.submit(st)
	if !slices.ContainsFunc(st.Lines, func(l state.Line) bool { return strings.Contains(l.Text, "Defender may delete risky.mrc") }) {
		t.Error("no warning loading risky.mrc")
	}
	goos = "darwin"
	if defenderRisk("on *:START:echo hi\nalias s bwrite f 0 -1 &b") {
		t.Error("warned off Windows")
	}
}

// TestScriptDCCEvents: a download finishing or failing is on FILERCVD or
// GETFAIL, Send File's upload on FILESENT or SENDFAIL, a DCC chat message
// on CHAT; and $sfile asks for a file name before a typed line runs.
func TestScriptDCCEvents(t *testing.T) {
	a := New(Options{Demo: true})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	n := a.nets[0]
	a.scriptMem = map[string]string{"d.mrc": `on *:FILERCVD:*.txt:echo -s got $nopath($filename) from $nick $get(-1).size $get(0)
on *:GETFAIL:*:echo -s getfail $nopath($filename) $get(-1).status
on *:FILESENT:*:echo -s sent $nopath($filename) to $nick $send(-1).size
on *:SENDFAIL:*:echo -s sendfail $nopath($filename) to $nick
on *:CHAT:*:echo -s chat $nick said $1- $chat(0)
alias pick echo -s picked $sfile(logs,Pick one) $sfstate`}
	st := n.Status()
	a.activate(st)
	a.edit(st).Buf = []rune("/load -rs d.mrc")
	a.submit(st)
	last := func() string { return lastLine(st) }

	tr := &transfer{net: n, nick: "bob", file: "notes.txt", path: "/dl/notes.txt", size: 100, started: time.Now()}
	a.transfers = append(a.transfers, tr)
	a.Update(dccDoneMsg{t: tr, got: 100})
	if got := last(); got != "got notes.txt from bob 100 1" {
		t.Errorf("FILERCVD: %q", got)
	}
	tr2 := &transfer{net: n, nick: "ann", file: "big.iso", path: "/dl/big.iso", size: 900, started: time.Now()}
	a.transfers = append(a.transfers, tr2)
	a.Update(dccDoneMsg{t: tr2, got: 10, err: errors.New("the sender stopped")})
	if got := last(); got != "getfail big.iso failed" {
		t.Errorf("GETFAIL: %q", got)
	}
	a.Update(uploadDoneMsg{w: st, target: "carol", link: "https://x/y", path: "/home/talk.pdf", size: 512, start: time.Now()})
	if got := last(); got != "sent talk.pdf to carol 512" {
		t.Errorf("FILESENT: %q", got)
	}
	a.Update(uploadDoneMsg{w: st, target: "dave", err: errors.New("no"), path: "/home/x.bin"})
	if got := last(); got != "sendfail x.bin to dave" {
		t.Errorf("SENDFAIL: %q", got)
	}
	c := &dccChat{net: n, nick: "eve", win: &state.Window{Kind: state.KChat, Name: "=eve", Net: n}}
	n.Wins = append(n.Wins, c.win)
	a.chats = append(a.chats, c)
	a.Update(chatLineMsg{c, "hello there"})
	if got := last(); got != "chat eve said hello there 1" {
		t.Errorf("CHAT: %q", got)
	}

	// $sfile asks first, in a file box with the folder filled in.
	a.edit(st).Buf = []rune("/pick")
	a.submit(st)
	f, ok := a.dlg.(*formBox)
	if !ok || f.Title != "Select File" || string(f.Fields[0].Edit.Buf) != "logs" {
		t.Fatalf("$sfile: %#v", a.dlg)
	}
	f.Fields[0].Edit.Buf = []rune("logs/a.txt")
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := last(); got != "picked logs/a.txt" {
		t.Errorf("after picking: %q", got)
	}
}

// TestScriptNetIdents: $network is the 005 NETWORK= once the server says
// it, $ip and $serverip the two ends of trout's connection, and
// $troutversion trout's version.
func TestScriptNetIdents(t *testing.T) {
	a, _ := fakeSoju(t)
	a.opts.Version = "v0.5.0"
	a.scripts = script.New(script.Options{Version: a.opts.Version})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=irc.example.net;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	ask := func(expr string) string {
		t.Helper()
		acts, _ := a.scripts.Command(n.Name, "", "echo -s "+expr, true, a.scriptHost())
		if len(acts) == 0 || acts[0].Echo == nil {
			t.Fatalf("%s: %+v", expr, acts)
		}
		return acts[0].Echo.Text
	}
	if got := ask("$network"); got != "irc.example.net" {
		t.Errorf("before 005: %q", got)
	}
	m, _ = ircmsg.ParseLine(":srv 005 max NETWORK=Libera.Chat CHANTYPES=# :are supported")
	a.Update(ircLineMsg{n, m})
	if got := ask("$network $ip $serverip $troutversion"); got != "Libera.Chat 127.0.0.1 127.0.0.1 0.5.0" {
		t.Errorf("after 005: %q", got)
	}
}

// TestScriptFullPaths: $mircdir is the scripts folder, a full path into
// it works like a name in it, and like mIRC's, so does one outside it.
func TestScriptFullPaths(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	a := New(Options{Config: cfg})
	dir := a.scriptsDir()
	if err := errors.Join(os.MkdirAll(dir, 0o755), os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("first line\r\n"), 0o644)); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("nope\r\n"), 0o644)
	acts, _ := a.scripts.Command(a.home.Name, "", "echo -s dir= $+ $mircdir full= $+ $read($mircdir $+ notes.txt,1) / $read(notes.txt,1) / outside= $+ $read("+outside+",1)", true, a.scriptHost())
	want := "dir=" + dir + string(filepath.Separator) + " full=first line / first line / outside=nope"
	if len(acts) == 0 || acts[0].Echo == nil || acts[0].Echo.Text != want {
		t.Errorf("got %q, want %q", acts[0].Echo.Text, want)
	}
}
