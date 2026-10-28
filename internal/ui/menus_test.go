package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

func TestParseDCCSend(t *testing.T) {
	for in, want := range map[string]string{
		"file.zip 2130706433 6800 3145728": "file.zip 127.0.0.1 6800 3145728",
		`"my file.zip" 3232235777 5000 10`: "my file.zip 192.168.1.1 5000 10",
		"file.zip ::1 6800 42 token":       "file.zip ::1 6800 42",
		"file.zip 2130706433 0 42 1234":    "file.zip 127.0.0.1 0 42",
		"file.zip nonsense 6800 1":         "error",
		`"unclosed 2130706433 6800 1`:      "error",
		"file.zip 2130706433 99999 1":      "error",
	} {
		file, ip, port, size, err := parseDCCSend(in)
		got := "error"
		if err == nil {
			got = strings.Join([]string{file, ip.String(), port, itoa(size)}, " ")
		}
		if got != want {
			t.Errorf("parseDCCSend(%q) = %q, want %q", in, got, want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd": "passwd",
		`..\..\boot.ini`:   "boot.ini",
		".bashrc":          "bashrc",
		"ok file.zip":      "ok file.zip",
		"evil\x1b[2Jname":  "evil[2Jname",
		"..":               "download",
		"":                 "download",
	} {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDownload gets a file from a local sender twice: whole, then resumed
// from the middle, acknowledging what it got as DCC requires.
func TestDownload(t *testing.T) {
	content := bytes.Repeat([]byte("trout!"), 50000)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	acked := make(chan int, 2)
	from := make(chan int, 2)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			start := <-from
			go func() {
				ackBytes, _ := io.ReadAll(c) // ends when trout closes
				acked <- len(ackBytes)
			}()
			c.Write(content[start:])
			time.Sleep(100 * time.Millisecond)
			c.(*net.TCPConn).CloseWrite()
		}
	}()
	path := filepath.Join(t.TempDir(), "f")
	from <- 0
	if got, err := download(t.Context(), (&net.Dialer{}).DialContext, 30*time.Second, ln.Addr().String(), path, 0, int64(len(content)), func(int64) {}); err != nil || got != int64(len(content)) {
		t.Fatalf("download = %d, %v", got, err)
	}
	if n := <-acked; n == 0 || n%4 != 0 {
		t.Errorf("acks were %d bytes, want 4 per read", n)
	}
	os.Truncate(path, 1000)
	from <- 1000
	if _, err := download(context.Background(), (&net.Dialer{}).DialContext, 30*time.Second, ln.Addr().String(), path, 1000, int64(len(content)), func(int64) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, content) {
		t.Fatalf("resumed file is %d bytes and differs", len(got))
	}
}

func TestIgnoreAndCTCP(t *testing.T) {
	a, n, feed := realApp(t)
	ch := n.Find(state.KChannel, "#go")
	a.ircCommand(ch, "ignore spammer")
	before := len(ch.Lines)
	feed(":spammer!s@h PRIVMSG #go :buy now", ":Spammer!x@y PRIVMSG max :psst")
	if len(ch.Lines) != before || n.Find(state.KQuery, "spammer") != nil {
		t.Fatal("an ignored nick's messages got through")
	}
	a.ircCommand(ch, "unignore spammer")
	feed(":spammer!s@h PRIVMSG #go :back")
	if lastLine(ch) != "<spammer> back" {
		t.Fatal("unignore didn't let them through")
	}

	statusBefore := len(n.Status().Lines)
	feed(":alice!a@h PRIVMSG max :\x01VERSION\x01")
	// It's shown in the status window (and answered, which fails here:
	// nothing's connected), not as a private message.
	if n.Find(state.KQuery, "alice") != nil || n.Status().Lines[statusBefore].Text != "[alice VERSION]" {
		t.Fatalf("a CTCP VERSION showed as %q", n.Status().Lines[statusBefore].Text)
	}
	a.activate(ch)
	feed(":alice!a@h NOTICE max :\x01VERSION irssi 1.4\x01")
	if lastLine(ch) != "[alice VERSION reply]: irssi 1.4" {
		t.Fatalf("a VERSION reply showed as %q", lastLine(ch))
	}

	feed(":bob!b@h INVITE max #secret")
	if !strings.Contains(lastLine(ch), "bob invites you to #secret") {
		t.Fatalf("an invite showed as %q", lastLine(ch))
	}
	feed(":bob!b@h PRIVMSG #go :see https://go.dev/doc, and http://example.com).")
	if len(a.urls) != 2 || a.urls[0].url != "http://example.com" || a.urls[1].url != "https://go.dev/doc" {
		t.Fatalf("URLs List has %+v", a.urls)
	}
}

func TestNotify(t *testing.T) {
	a, n, feed := realApp(t)
	a.setNotify(n, "alice", true)
	var out bytes.Buffer
	a.bellOut = &out
	feed(":srv 730 max :alice!a@h")
	if !n.Online["alice"] || !strings.Contains(lastLine(n.Status()), "alice (a@h) is on Libera at ") {
		t.Fatalf("notify online: %v, %q", n.Online, lastLine(n.Status()))
	}
	feed(":srv 731 max :alice")
	if n.Online["alice"] || !strings.Contains(lastLine(n.Status()), "alice has left Libera") {
		t.Fatalf("notify offline: %v, %q", n.Online, lastLine(n.Status()))
	}
}

// TestChannelModes sends only what changed, with mode arguments in order.
func TestChannelModes(t *testing.T) {
	a, sent := fakeSoju(t)
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	// Send on the control connection, so fakeSoju sees it.
	a.conns[n] = a.control
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true, Modes: "ntk", Topic: "old",
		Nicks: []state.Nick{{Prefix: "@", Name: "max"}}})
	a.activate(w)
	a.openChannelModes(w)
	f := a.dlg.(*formBox)
	f.Fields[0].Edit = state.EditBox{Buf: []rune("new topic")}
	f.Fields[2].On = false                              // -n
	f.Fields[3].On = true                               // +i
	f.Fields[7].Edit = state.EditBox{Buf: []rune("-")}  // -k
	f.Fields[8].Edit = state.EditBox{Buf: []rune("10")} // +l 10
	a.dlg.key(a, "enter")
	want := []string{"TOPIC #go :new topic", "MODE #go +il-nk 10 *"}
	for _, w := range want {
		select {
		case got := <-sent:
			if got != w {
				t.Fatalf("sent %q, want %q", got, w)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("never sent %q", w)
		}
	}
}

// TestDCCOffer walks an offer: the privacy notice once, then a get dialog
// for an unsolicited offer, but none for a file you asked for.
func TestDCCOffer(t *testing.T) {
	a, n, feed := realApp(t)
	a.downloadDir = t.TempDir()
	offer := ":bot!b@h PRIVMSG max :\x01DCC SEND pack.bin 3232235777 6800 1024\x01" // 192.168.1.1
	feed(offer)
	d, ok := a.dlg.(*msgBox)
	if !ok || d.Title != "DCC and Your Privacy" {
		t.Fatalf("the first offer opened %T, want the privacy notice", a.dlg)
	}
	d.Checked = true
	a.dlg.key(a, "enter") // Continue
	if a.dccWarn {
		t.Fatal("\"don't show again\" didn't stick")
	}
	g, ok := a.dlg.(*msgBox)
	if !ok || g.Title != "DCC Get" || g.Sel != 1 || !strings.Contains(strings.Join(g.Lines, "\n"), "Careful: it points at 192.168.1.1") {
		t.Fatalf("a LAN offer should ask, with a warning and Decline focused; got %T %+v", a.dlg, a.dlg)
	}
	a.dlg.key(a, "d")

	// A request from another of your clients, relayed by the bouncer,
	// doesn't count as asking: that one asks.
	feed(":max!m@h PRIVMSG bot :xdcc send #1")
	feed(":bot!b@h PRIVMSG max :\x01DCC SEND pack.bin 134744072 6800 1024\x01") // 8.8.8.8
	if _, ok := a.dlg.(*msgBox); !ok {
		t.Fatal("an offer someone else asked for didn't ask")
	}
	a.dlg.key(a, "d")

	// Asked for here: /msg bot xdcc send #1. A public address and a
	// recent message means no dialog.
	a.messaged[n.ID+" bot"] = time.Now()                                        // as sendNet records it
	feed(":bot!b@h PRIVMSG max :\x01DCC SEND pack.bin 134744072 6800 1024\x01") // 8.8.8.8
	if a.dlg != nil {
		t.Fatalf("an offer you asked for opened %T", a.dlg)
	}
	if len(a.transfers) != 1 || a.transfers[0].path != filepath.Join(a.downloadDir, "pack.bin") {
		t.Fatalf("transfers: %+v", a.transfers)
	}
	// It has a Get window, like mIRC; closing that cancels the download
	// (which would otherwise try to reach 8.8.8.8).
	tr0 := a.transfers[0]
	if tr0.win == nil || n.Find(state.KTransfer, "Get pack.bin") != tr0.win || a.active == tr0.win {
		t.Fatal("no Get window, or it took the focus")
	}
	for tr0.cancel == nil {
		time.Sleep(time.Millisecond) // dccGo runs in the background
	}
	a.closeWindow(tr0.win)
	if n.Find(state.KTransfer, "Get pack.bin") != nil {
		t.Fatal("closing the Get window didn't close it")
	}

	// The sender re-offers after a failed try, much later: resume, no dialog.
	tr := a.transfers[0]
	tr.done, tr.err = true, errors.New("connection reset by peer")
	os.WriteFile(tr.path, make([]byte, 100), 0o644)
	clear(a.messaged)
	feed(":bot!b@h PRIVMSG max :\x01DCC SEND pack.bin 134744072 6800 1024\x01")
	if a.dlg != nil || len(a.transfers) != 2 || !a.transfers[1].resuming || a.transfers[1].from != 100 {
		t.Fatalf("a re-offer after a failure: dialog %T, transfers %+v", a.dlg, a.transfers)
	}

	a.dccGet = 2
	feed(offer)
	if a.dlg != nil || !strings.Contains(lastLine(n.Status()), "Ignored pack.bin") {
		t.Fatal("Ignore all still asked")
	}
}

func TestFormChecks(t *testing.T) {
	a := New(Options{Demo: true})
	var got []string
	f := &formBox{Title: "t", Buttons: []string{"OK", "Cancel"},
		Fields: []*formField{newCheck("one", false), newField("Text:", "", false)},
		OnOK:   func(a *App, v []string) (tea.Cmd, error) { got = v; return nil, nil }}
	a.openDialog(f)
	a.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")})
	a.Update(tea.KeyMsg{Type: tea.KeyTab})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if strings.Join(got, ",") != "1,x" {
		t.Fatalf("form values %q, want the box ticked and x typed", got)
	}
}

// TestChannelsList downloads the list once, shows the cached one next
// time, survives the server's "try again later" and a server that never
// answers, and leaves a filtered /list to the status window.
func TestChannelsList(t *testing.T) {
	a, sent := fakeSoju(t)
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Rizon;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control // so fakeSoju sees what's sent
	a.activate(n.Status())
	feed := func(lines ...string) {
		for _, l := range lines {
			m, _ := ircmsg.ParseLine(l)
			a.Update(ircLineMsg{n, m})
		}
	}
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-sent:
			if got != want {
				t.Fatalf("sent %q, want %q", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("never sent %q", want)
		}
	}

	if a.openChannelsList(false) == nil {
		t.Fatal("no timeout for the first download")
	}
	expect("LIST")
	feed(":srv 321 max Channel :Users Name", ":srv 322 max #small 3 :tiny",
		":srv 322 max #big 300 :busy", ":srv 323 max :End of /LIST")
	l := a.dlg.(*listBox)
	if len(l.Keys) != 2 || l.Keys[0] != "#big" || !strings.Contains(l.Title, "got just now") {
		t.Fatalf("after downloading: %q, %v", l.Title, l.Keys)
	}

	a.dlg = nil
	if a.openChannelsList(false) != nil {
		t.Fatal("reopening asked the server again instead of showing the cached list")
	}
	l = a.dlg.(*listBox)
	l.press(a, "Get List")
	expect("LIST")
	feed(":srv 263 max LIST :This command could not be completed because it has been used recently, and is rate-limited")
	if len(l.Keys) != 2 || !strings.Contains(l.Title, "rate-limited") {
		t.Fatalf("after RPL_TRYAGAIN: %q, %v", l.Title, l.Keys)
	}

	l.press(a, "Get List")
	expect("LIST")
	a.Update(listTimeoutMsg{n, a.listSeq})
	if !strings.Contains(l.Title, "no answer") || a.listWaiting[n] != 0 {
		t.Fatalf("after no answer: %q", l.Title)
	}

	a.dlg = nil
	a.ircCommand(n.Status(), "list >100")
	expect("LIST >100")
	feed(":srv 322 max #big 301 :busy", ":srv 323 max :End of /LIST")
	if a.dlg != nil || !slices.Contains(lineTexts(n.Status()), "#big 301 busy") {
		t.Fatalf("a filtered /list: dialog %T, status %q", a.dlg, lineTexts(n.Status()))
	}
	if c := a.chanLists[n]; len(c.rows) != 2 {
		t.Fatal("a filtered /list replaced the cached list")
	}
}

func lineTexts(w *state.Window) []string {
	var out []string
	for _, l := range w.Lines {
		out = append(out, l.Text)
	}
	return out
}

// TestChannelCentralLists fetches the ban list, keeps it current as bans
// come and go, removes one, and is read-only for non-operators.
func TestChannelCentralLists(t *testing.T) {
	a, sent := fakeSoju(t)
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;state=connected")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick, n.ChanModes = "max", "beI,k,l,imnpst"
	a.conns[n] = a.control
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true,
		Nicks: []state.Nick{{Prefix: "@", Name: "max"}}})
	feed := func(lines ...string) {
		for _, l := range lines {
			m, _ := ircmsg.ParseLine(l)
			a.Update(ircLineMsg{n, m})
		}
	}
	a.openChannelModes(w)
	a.dlg.(*formBox).press(a, 1) // Bans...
	expectSent(t, sent, "MODE #go +b")
	feed(":srv 367 max #go *!*@spam.example op!o@h 1700000000", ":srv 368 max #go :End of channel ban list")
	l := a.dlg.(*listBox)
	if len(l.Keys) != 1 || l.Keys[0] != "*!*@spam.example" || !strings.HasPrefix(l.Title, "Bans") {
		t.Fatalf("ban list: %q %v", l.Title, l.Keys)
	}
	feed(":op!o@h MODE #go +b-b troll!*@* *!*@spam.example")
	if len(l.Keys) != 1 || l.Keys[0] != "troll!*@*" {
		t.Fatalf("after +b troll -b spam: %v", l.Keys)
	}
	l.press(a, "Remove")
	expectSent(t, sent, "MODE #go -b troll!*@*")
	l.press(a, "Next List")
	expectSent(t, sent, "MODE #go +e")

	feed(":op!o@h TOPIC #go :new times")
	if w.Topics[len(w.Topics)-1].Text != "new times" {
		t.Fatal("the topic change wasn't kept")
	}

	w.Nicks = []state.Nick{{Name: "max"}} // no longer an operator
	a.openChannelModes(w)
	f := a.dlg.(*formBox)
	if !strings.Contains(f.Lines[0], "read-only") {
		t.Fatal("Channel Central isn't read-only for non-operators")
	}
	f.Fields[0].Edit.Buf = []rune("changed")
	f.press(a, 0)
	select {
	case got := <-sent:
		t.Fatalf("a non-operator's Channel Central sent %q", got)
	case <-time.After(200 * time.Millisecond):
	}
}
