package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

func TestStampLayout(t *testing.T) {
	at := time.Date(2026, 10, 27, 15, 4, 5, 0, time.UTC)
	for format, want := range map[string]string{
		"[HH:nn]":          "[15:04]",
		"[hh:nn tt]":       "[03:04 pm]",
		"[dd/mm HH:nn:ss]": "[27/10 15:04:05]",
		"ddd HH:nn":        "Tue 15:04",
	} {
		if got := at.Format(stampLayout(format)); got != want {
			t.Errorf("%q = %q, want %q", format, got, want)
		}
	}
}

// optionsApp is a real-mode App on a scripted bouncer with one connected
// network, for options that send things.
func optionsApp(t *testing.T) (*App, *state.Network, chan string, func(lines ...string)) {
	a, sent := fakeSoju(t)
	m, _ := ircmsg.ParseLine("BOUNCER NETWORK 7 name=Libera;host=irc.libera.chat;port=6697;tls=1;state=connected;nickname=max;realname=Max")
	a.Update(ircLineMsg{a.home, m})
	n := a.nets[1]
	n.Nick = "max"
	a.conns[n] = a.control
	feed := func(lines ...string) {
		for _, l := range lines {
			m, _ := ircmsg.ParseLine(l)
			a.Update(ircLineMsg{n, m})
		}
	}
	return a, n, sent, feed
}

func expectSent(t *testing.T, sent chan string, want string) {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case got := <-sent:
			if got == want {
				return
			}
		case <-timeout:
			t.Fatalf("never sent %q", want)
		}
	}
}

func TestIRCOptions(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})
	a.activate(w)

	a.rejoinOnKick = true
	feed(":op!o@h KICK #go max :out")
	expectSent(t, sent, "JOIN #go")

	a.joinOnInvite = true
	feed(":bob!b@h INVITE max #secret")
	expectSent(t, sent, "JOIN #secret")

	a.whoisOnQuery = true
	feed(":carol!c@h PRIVMSG max :hi")
	expectSent(t, sent, "WHOIS carol")

	a.noticesActive = false
	feed(":NickServ!s@services. NOTICE max :identify, please")
	if lastLine(n.Status()) != "-NickServ- identify, please" {
		t.Fatalf("with notices off the active window, status says %q", lastLine(n.Status()))
	}

	a.confirmPaste = false
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\ntwo"), Paste: true})
	if a.dlg != nil {
		t.Fatal("pasting asked with the confirmation off")
	}
	expectSent(t, sent, "PRIVMSG #go one")
	expectSent(t, sent, "PRIVMSG #go two")
}

func TestConnectOptions(t *testing.T) {
	a, n, sent, _ := optionsApp(t)
	a.activate(n.Status())
	a.openOptions(optConnect)
	o := a.dlg.(*optionsBox)
	items := o.pages[optConnect].Items
	if !strings.Contains(items[0].Label, "max") || !strings.Contains(items[1].Label, "Max") {
		t.Fatalf("Connect shows %q, %q", items[0].Label, items[1].Label)
	}
	items[1].toggle(a)
	f := a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune("Max Power")
	a.dlg.key(a, "enter")
	expectSent(t, sent, `PRIVMSG BouncerServ :user update -realname "Max Power"`)
	if _, ok := a.dlg.(*optionsBox); !ok {
		t.Fatal("didn't go back to Options")
	}

	// A network's own username and real name, in Select Server > Edit.
	f = a.networkForm(n, nil)
	a.openDialog(f)
	f.Fields[3].Edit.Buf = []rune("maxi")
	f.Fields[4].Edit.Buf = []rune("Max on Libera")
	a.dlg.key(a, "enter")
	expectSent(t, sent, `PRIVMSG BouncerServ :network update Libera -username maxi -realname "Max on Libera"`)
}

func TestDownloadFolder(t *testing.T) {
	a := New(Options{Demo: true})
	dir := filepath.Join(t.TempDir(), "incoming", "files")
	a.openDownloadDir()
	f := a.dlg.(*formBox)
	f.Fields[0].Edit.Buf = []rune(dir)
	a.dlg.key(a, "enter")
	if a.downloadDir != dir {
		t.Fatalf("download folder %q, want %q", a.downloadDir, dir)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatal("the folder wasn't made")
	}
	if got, _ := a.downloads(); got != dir {
		t.Fatalf("downloads go to %q", got)
	}
}

// TestConnectToggle: like mIRC's, the toolbar button says Disconnect while
// connected, and does that.
func TestConnectToggle(t *testing.T) {
	a, n, sent, _ := optionsApp(t)
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	a.activate(n.Status())
	if !strings.Contains(a.View(), "Disconnect") {
		t.Fatal("the toolbar says Connect while connected")
	}
	a.toolAction("Connect")
	expectSent(t, sent, "PRIVMSG BouncerServ :network update Libera -enabled false")
}
