package ui

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestConnectFlow walks the first run: no config, so the Connect dialog;
// a typo, then a bouncer that's saved with its password; a wrong password
// reopening the dialog; a self-signed certificate getting pinned; and a
// settings change being saved.
func TestConnectFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	a := New(Options{Config: cfg})
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	if cmd := a.Init(); cmd != nil {
		t.Fatal("Init with no bouncer tried to connect")
	}
	f, ok := a.dlg.(*formBox)
	if !ok {
		t.Fatalf("first run opened %T, want the Connect dialog", a.dlg)
	}
	typeText := func(s string) { a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }
	key := func(k tea.KeyType) { a.Update(tea.KeyMsg{Type: k}) }

	typeText("irc://bnc.example.net")
	key(tea.KeyEnter)
	if a.dlg != f || f.Note == "" {
		t.Fatal("an irc:// address didn't keep the dialog open with a note")
	}
	f.Fields[0].Edit = state.EditBox{}
	typeText("bnc.example.net")
	key(tea.KeyTab)
	typeText("max")
	key(tea.KeyTab)
	typeText("hunter2")
	key(tea.KeyEnter)
	if a.dlg != nil {
		t.Fatalf("Connect didn't close the dialog; note %q", f.Note)
	}
	if a.bnc.Name != "bnc.example.net" || a.bnc.User != "max" || a.password != "hunter2" || a.stopped {
		t.Fatalf("connecting as %+v / %q, stopped %v", a.bnc, a.password, a.stopped)
	}
	reload := func() *config.Config {
		t.Helper()
		c, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := reload(); c.Active != "bnc.example.net" || len(c.Bouncers) != 1 {
		t.Fatalf("saved config %+v, want the new bouncer, active", c)
	} else if pw, _ := c.Password(c.Bouncers[0]); pw != "hunter2" {
		t.Fatalf("saved password %q", pw)
	}

	// A wrong password: no retrying, the dialog comes back on the password.
	home := a.home
	a.Update(ircDownMsg{home, fmt.Errorf("registering with x: %w: bad password", irc.ErrLogin)})
	if f, ok := a.dlg.(*formBox); !ok || f.focus != 2 || !a.stopped {
		t.Fatalf("a failed login opened %T (stopped %v), want the Connect dialog on the password", a.dlg, a.stopped)
	}
	if _, cmd := a.Update(reconnectMsg{home}); cmd != nil {
		t.Fatal("reconnected after a failed login")
	}
	key(tea.KeyEsc)

	// A self-signed certificate: Enter doesn't trust it, T does.
	ce := &irc.CertError{Addr: "bnc.example.net:6697", Fingerprint: "SHA256:k3y"}
	a.Update(ircDownMsg{home, ce})
	if _, ok := a.dlg.(*msgBox); !ok {
		t.Fatalf("an untrusted certificate opened %T, want the trust box", a.dlg)
	}
	key(tea.KeyEnter)
	if a.bnc.Pin != "" {
		t.Fatal("Enter trusted the key")
	}
	a.Update(ircDownMsg{home, ce})
	typeText("t")
	if a.bnc.Pin != "SHA256:k3y" || reload().Bouncers[0].Pin != "SHA256:k3y" || a.stopped {
		t.Fatalf("trusting pinned %q, saved %q, stopped %v", a.bnc.Pin, reload().Bouncers[0].Pin, a.stopped)
	}

	// Switching the theme saves it.
	a.Update(tea.KeyMsg{Type: tea.KeyF2})
	if got := reload().Settings.Theme; got != "dark" {
		t.Fatalf("saved theme %q after F2, want dark", got)
	}

	// Adding a second bouncer in Tools > Bouncers switches to it and starts
	// over; lines from the old connections don't land anywhere.
	a.openSavedBouncers()
	typeText("a")
	f = a.dlg.(*formBox)
	typeText("irc+insecure://other.example:6667")
	key(tea.KeyTab)
	typeText("max")
	key(tea.KeyTab)
	typeText("pw2")
	key(tea.KeyEnter)
	if a.home == home || len(a.nets) != 1 || reload().Active != "other.example" || a.bnc.Pin != "" {
		t.Fatalf("after switching: home replaced %v, %d nets, active %q, pin %q",
			a.home != home, len(a.nets), reload().Active, a.bnc.Pin)
	}
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 1 name=Old")
	a.Update(ircLineMsg{home, m})
	if len(a.nets) != 1 {
		t.Fatal("a line from the old bouncer added a network")
	}

	// Use goes back to the first one with its saved password and pin;
	// Remove forgets the second.
	a.openSavedBouncers()
	d := a.dlg.(*savedBouncersBox)
	d.sel = 0
	typeText("u")
	if a.bnc.Name != "bnc.example.net" || a.password != "hunter2" || a.bnc.Pin != "SHA256:k3y" {
		t.Fatalf("Use switched to %+v / %q", a.bnc, a.password)
	}
	a.openSavedBouncers()
	a.dlg.(*savedBouncersBox).sel = 1
	typeText("r")
	typeText("y")
	if c := reload(); len(c.Bouncers) != 1 || c.Bouncers[0].Name != "bnc.example.net" {
		t.Fatalf("after Remove, config has %+v", c.Bouncers)
	} else if pw, _ := c.StoredPassword("other.example"); pw != "" {
		t.Fatal("Remove left the password behind")
	}
}

// TestSojuNetworks drives network management through BouncerServ: File >
// Connect and Disconnect, the Servers dialog's Add with SASL, /server,
// and soju's state reports, with a scripted soju on the other end.
func TestSojuNetworks(t *testing.T) {
	a, sent := fakeSoju(t)
	feed := func(n *state.Network, lines ...string) {
		for _, l := range lines {
			m, err := ircmsg.ParseLine(l)
			if err != nil {
				t.Fatal(err)
			}
			a.Update(ircLineMsg{n, m})
		}
	}
	feed(a.home, "BOUNCER NETWORK 7 name=Libera;host=irc.libera.chat;port=6697;tls=1;nickname=max;state=connected")
	if len(a.nets) != 2 {
		t.Fatal("soju's network didn't show up")
	}
	lib := a.nets[1]
	lib.Add(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})

	a.disconnectNet(lib)
	if got := <-sent; got != "PRIVMSG BouncerServ :network update Libera -enabled false" {
		t.Fatalf("Disconnect sent %q", got)
	}
	feed(a.home,
		":max!max@soju PRIVMSG BouncerServ :network update Libera -enabled false", // echo-message
		`:BouncerServ!BouncerServ@BouncerServ PRIVMSG max :updated network "Libera"`,
		"BOUNCER NETWORK 7 state=disconnected;error=Connection\\sreset")
	if lib.Find(state.KQuery, "BouncerServ") != nil || a.home.Find(state.KQuery, "BouncerServ") != nil {
		t.Fatal("trout's own BouncerServ command opened a query window")
	}
	if ch := lib.Find(state.KChannel, "#go"); ch.Joined || !lib.Down() || !strings.Contains(a.winTitle(lib.Status()), "[disconnected]") {
		t.Fatalf("after soju disconnected: #go joined %v, down %v, title %q", ch.Joined, lib.Down(), a.winTitle(lib.Status()))
	}
	if last := lib.Status().Lines[len(lib.Status().Lines)-1].Text; last != "* Bouncer: disconnected from Libera (Connection reset)" {
		t.Fatalf("status says %q", last)
	}
	// Disconnected, or switched off? BouncerServ's network status says.
	if got := <-sent; got != "PRIVMSG BouncerServ :network status" {
		t.Fatalf("after a network went down, sent %q", got)
	}
	feed(a.home, ":BouncerServ!BouncerServ@BouncerServ PRIVMSG max :Libera (ircs://irc.libera.chat:6697) [disabled]")
	if !lib.Disabled || !strings.Contains(a.winTitle(lib.Status()), "[disabled]") {
		t.Fatalf("Libera disabled %v, title %q", lib.Disabled, a.winTitle(lib.Status()))
	}

	// Add a network with a SASL login: created disabled, login set, then
	// enabled, and its window focused once soju has it.
	a.openServers()
	a.dlg.key(a, "insert")
	f := a.dlg.(*formBox)
	for i, v := range []string{"OFTC", "irc.oftc.net", "", "", "", "", "max", `pa"ss word`} {
		f.Fields[i].Edit.Buf = []rune(v)
	}
	a.dlg.key(a, "enter")
	if a.dlg != nil {
		t.Fatalf("Add didn't close: %q", f.Note)
	}
	for _, want := range []string{
		"PRIVMSG BouncerServ :network create -addr ircs://irc.oftc.net:6697 -name OFTC -enabled false",
		`PRIVMSG BouncerServ :sasl set-plain -network OFTC max "pa\"ss word"`,
		"PRIVMSG BouncerServ :network update OFTC -enabled true",
	} {
		if got := <-sent; got != want {
			t.Fatalf("sent %q, want %q", got, want)
		}
	}
	feed(a.home, "BOUNCER NETWORK 8 name=OFTC;host=irc.oftc.net;port=6697;tls=1;state=connecting")
	if a.active.Net.Name != "OFTC" {
		t.Fatalf("the new network's window isn't focused; active is %s", a.active.Net.Name)
	}

	// An error reply shows up as a message box.
	feed(a.home, `:BouncerServ!BouncerServ@BouncerServ PRIVMSG max :error: unknown network "OFTC"`)
	if _, ok := a.dlg.(*msgBox); !ok {
		t.Fatal("an error from BouncerServ didn't open a message box")
	}
	a.dlg = nil

	// /away also puts the message on the network itself, since soju only
	// ever sends "AWAY *" upstream.
	feed(a.home, "BOUNCER NETWORK 7 state=connected")
	toServ := func() string { // skipping the AWAY to soju itself
		for got := range sent {
			if strings.HasPrefix(got, "PRIVMSG BouncerServ") {
				return got
			}
		}
		return ""
	}
	a.ircCommand(a.home.Status(), "away gone fishing")
	feed(lib, ":soju 306 max :You have been marked as being away")
	if got := toServ(); got != `PRIVMSG BouncerServ :network quote Libera "AWAY :gone fishing"` {
		t.Fatalf("/away sent %q", got)
	}
	a.ircCommand(a.home.Status(), "away")
	feed(lib, ":soju 305 max :You are no longer marked as being away")
	if got := toServ(); got != "PRIVMSG BouncerServ :network quote Libera AWAY" {
		t.Fatalf("/away to come back sent %q", got)
	}
	feed(a.home, "BOUNCER NETWORK 7 state=disconnected")
	toServ() // the network status check

	// /server to a host soju has focuses it; a new one gets added.
	a.ircCommand(a.home.Status(), "server irc.libera.chat")
	if a.active != lib.Status() {
		t.Fatal("/server irc.libera.chat didn't focus Libera")
	}
	if got := <-sent; got != "PRIVMSG BouncerServ :network update Libera -enabled true" {
		t.Fatalf("/server on a disconnected network sent %q", got)
	}
	a.ircCommand(a.home.Status(), "server -m irc.rizon.net 6667")
	if got := <-sent; got != "PRIVMSG BouncerServ :network create -addr ircs://irc.rizon.net:6667 -name irc.rizon.net" {
		t.Fatalf("/server for a new host sent %q", got)
	}
}

// fakeSoju is an App connected to a pretend soju: whatever trout sends on
// any connection to it (the control one, and networks') comes out of sent.
func fakeSoju(t *testing.T) (*App, chan string) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	sent := make(chan string, 64)
	serve := func(nc net.Conn) {
		defer nc.Close()
		r := bufio.NewReader(nc)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			switch {
			case line == "CAP LS 302":
				nc.Write([]byte(":soju CAP * LS :echo-message batch server-time draft/chathistory draft/read-marker\r\n"))
			case strings.HasPrefix(line, "CAP REQ "):
				nc.Write([]byte(":soju CAP max ACK " + strings.TrimPrefix(line, "CAP REQ ") + "\r\n"))
			case line == "CAP END":
				nc.Write([]byte(":soju 001 max :Welcome\r\n"))
			case strings.HasPrefix(line, "NICK"), strings.HasPrefix(line, "USER"), strings.HasPrefix(line, "BOUNCER BIND"):
			default:
				sent <- line
			}
		}
	}
	go func() {
		for {
			nc, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(nc)
		}
	}()
	a := New(Options{Bouncer: config.Bouncer{Address: "irc+insecure://" + ln.Addr().String(), User: "max"}})
	up := a.connectBouncer()()
	if _, ok := up.(ircUpMsg); !ok {
		t.Fatalf("connecting to the fake soju: %v", up)
	}
	a.Update(up)
	for _, want := range []string{"BOUNCER LISTNETWORKS", "PRIVMSG BouncerServ :network status"} {
		if got := <-sent; got != want {
			t.Fatalf("on connecting, sent %q, want %q", got, want)
		}
	}
	a.home.Nick = "max"
	m, _ := ircmsg.ParseLine(":BouncerServ!BouncerServ@BouncerServ PRIVMSG max :no networks yet")
	a.Update(ircLineMsg{a.home, m})
	return a, sent
}

// TestCatchUp opens a private chat that got messages while trout was
// closed, places the line marker at soju's read marker, fills the gap after
// a dropped connection, and syncs reading with soju only while focused.
func TestCatchUp(t *testing.T) {
	a, sent := fakeSoju(t)
	expect := func(prefix string) string {
		t.Helper()
		timeout := time.After(5 * time.Second)
		for {
			select {
			case got := <-sent:
				if strings.HasPrefix(got, prefix) {
					return got
				}
			case <-timeout:
				t.Fatalf("trout never sent %q", prefix)
			}
		}
	}
	feed := func(n *state.Network, lines ...string) {
		t.Helper()
		for _, l := range lines {
			m, err := ircmsg.ParseLine(l)
			if err != nil {
				t.Fatal(err)
			}
			a.Update(ircLineMsg{n, m})
		}
	}
	connect := func(cmd tea.Cmd) {
		t.Helper()
		up, ok := cmd().(ircUpMsg)
		if !ok {
			t.Fatal("the network didn't connect")
		}
		a.Update(up)
	}

	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;host=irc.libera.chat;nickname=max;state=connected")
	b, _ := irc.ParseBouncerNetwork(m)
	connect(a.bouncerNetwork(b))
	n := a.nets[1]
	expect("CHATHISTORY TARGETS timestamp=")

	feed(n, "BATCH +t draft/chathistory-targets",
		"@batch=t CHATHISTORY TARGETS alice 2026-10-27T12:05:00.000Z",
		"@batch=t CHATHISTORY TARGETS bob 2026-10-27T11:00:00.000Z",
		"@batch=t CHATHISTORY TARGETS Global 2026-10-27T12:01:00.000Z",
		"@batch=t CHATHISTORY TARGETS #go 2026-10-27T12:00:00.000Z",
		"BATCH -t")
	expect("MARKREAD alice")
	expect("MARKREAD bob")
	expect("MARKREAD Global")
	feed(n, ":max!m@h MARKREAD alice timestamp=2026-10-27T12:00:00.000Z",
		":max!m@h MARKREAD bob timestamp=2026-10-27T11:30:00.000Z",
		":max!m@h MARKREAD Global *")
	expect("CHATHISTORY LATEST alice * 50")
	expect("CHATHISTORY LATEST Global * 50")
	if n.Find(state.KQuery, "bob") != nil {
		t.Fatal("bob, all read, got a window")
	}
	feed(n, "BATCH +h chathistory alice",
		"@batch=h;time=2026-10-27T12:05:00.000Z :alice!a@h PRIVMSG max :are you there?",
		"@batch=h;time=2026-10-27T11:59:00.000Z :alice!a@h PRIVMSG max :hi",
		"@batch=h;time=2026-10-27T12:04:00.000Z :alice!a@h NOTICE max :a notice",
		"BATCH -h")
	// Notices and CTCPs only, like Rizon's Global: no window; the notices
	// go to the status window, like mIRC would have shown them.
	feed(n, "BATCH +g chathistory Global",
		"@batch=g;time=2026-10-27T12:01:00.000Z :Global!g@services. NOTICE max :[Logon News] Welcome",
		"@batch=g;time=2026-10-27T12:01:01.000Z :Global!g@services. PRIVMSG max :\x01VERSION\x01",
		"BATCH -g")
	if n.Find(state.KQuery, "Global") != nil {
		t.Fatal("notices and CTCPs opened a private window")
	}
	var statusTexts []string
	for _, l := range n.Status().Lines {
		statusTexts = append(statusTexts, l.Text)
	}
	if !slices.Contains(statusTexts, "-Global- [Logon News] Welcome") || !slices.Contains(statusTexts, "-alice- a notice") {
		t.Fatalf("missed notices aren't in the status window: %q", statusTexts)
	}
	alice := n.Find(state.KQuery, "alice")
	if alice == nil {
		t.Fatal("alice's unread private messages didn't open a window")
	}
	if len(alice.Lines) != 2 || alice.Lines[0].Text != "<alice> hi" || alice.Marker != 1 || alice.Act != state.ActMsg {
		t.Fatalf("alice: %d lines (first %q), marker %d, activity %v; want hi first, marker after it, unread",
			len(alice.Lines), alice.Lines[0].Text, alice.Marker, alice.Act)
	}

	// Another client reads it: no longer unread here either.
	feed(n, ":max!m@h MARKREAD alice timestamp=2026-10-27T12:05:00.000Z")
	if alice.Act != state.ActNone || alice.Marker != 2 {
		t.Fatalf("after reading elsewhere: activity %v, marker %d", alice.Act, alice.Marker)
	}

	// A dropped connection: on reconnecting, ask for what came after.
	a.Update(ircDownMsg{n, errors.New("EOF")})
	connect(a.dial(n, n.ID, networkCaps))
	expect("CHATHISTORY AFTER alice timestamp=2026-10-27T12:05:00.000Z 500")

	// Reading: looking at alice while focused tells soju, but not while
	// the terminal is in the background.
	feed(n, "@time=2026-10-27T12:10:00.000Z :alice!a@h PRIVMSG max :ping")
	a.Update(tea.BlurMsg{})
	a.activate(alice)
	a.Update(readTickMsg{})
	if !alice.ReadAt.Equal(time.Date(2026, 10, 27, 12, 5, 0, 0, time.UTC)) {
		t.Fatalf("read up to %v while the terminal was in the background", alice.ReadAt)
	}
	a.Update(tea.FocusMsg{})
	if got := expect("MARKREAD alice"); got != "MARKREAD alice timestamp=2026-10-27T12:10:00.000Z" {
		t.Fatalf("sent %q", got)
	}
}
