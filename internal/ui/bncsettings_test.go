package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"
)

// TestBouncerSettings walks Bouncer > Settings against a scripted soju:
// what it asks BouncerServ, the pages it makes of the answers, detaching
// a channel and its options, deleting a user with soju's token, a
// network's connect commands, and a user who isn't an admin.
func TestBouncerSettings(t *testing.T) {
	a, sent := fakeSoju(t)
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	feed := func(lines ...string) {
		t.Helper()
		for _, l := range lines {
			m, err := ircmsg.ParseLine(l)
			if err != nil {
				t.Fatal(err)
			}
			a.Update(ircLineMsg{a.home, m})
		}
	}
	serv := func(text string) string { return ":BouncerServ!BouncerServ@BouncerServ PRIVMSG max :" + text }
	expect := func(want ...string) {
		t.Helper()
		for _, w := range want {
			if got := <-sent; got != "PRIVMSG BouncerServ :"+w {
				t.Fatalf("sent %q, want BouncerServ %q", got, w)
			}
		}
	}
	// The settings asking again after a change (six lines, or the five
	// after one already expected), whose answers this test doesn't give,
	// so they're forgotten.
	drain := func(n int) {
		t.Helper()
		for range n {
			if got := <-sent; !strings.HasPrefix(got, "PRIVMSG BouncerServ :") {
				t.Fatalf("sent %q", got)
			}
		}
		a.serving = nil
	}
	feed("BOUNCER NETWORK 7 name=TroutNet;host=troutnet;port=6697;tls=1;state=connected;nickname=max")

	a.openBouncerSettings(bncNetworks)
	expect("network status", "help help", "channel status", "help help", "user status", "help help")
	feed(serv("TroutNet (ircs://troutnet:6697) [connected as max]: 1 channels"), serv(servListEnd),
		serv("#trout/TroutNet [joined]"), serv("#old/TroutNet [parted, detached]"), serv(servListEnd),
		serv("max (admin): 1 networks"), serv("ann (disabled): 2 networks"), serv(servListEnd))
	expect("server status", "help help")
	feed(serv("2/2 users, 1 downstreams, 1 upstreams, 3 networks, 2 channels"), serv(servListEnd))

	o, ok := a.dlg.(*optionsBox)
	if !ok || o.title() != bncSettingsTitle {
		t.Fatalf("Settings opened %T", a.dlg)
	}
	page := func(i int) string { return strings.Join(o.pages[i].Notes, "\n") }
	if !strings.Contains(page(bncNetworks), "TroutNet: connected as max, 1 channel") {
		t.Errorf("Networks:\n%s", page(bncNetworks))
	}
	if !strings.Contains(page(bncChannels), "keeps 2 channels; 1 detached") {
		t.Errorf("Channels:\n%s", page(bncChannels))
	}
	if !strings.Contains(page(bncUsers), "ann (disabled): 2 networks") || len(o.pages[bncUsers].Items) == 0 {
		t.Errorf("Users:\n%s", page(bncUsers))
	}
	if !strings.Contains(page(bncServer), "Now:\n  2/2 users\n  1 downstream") {
		t.Errorf("Server:\n%s", page(bncServer))
	}
	if strings.Contains(strings.Join([]string{page(0), page(1), page(2), page(3), page(4)}, ""), "Asking") {
		t.Error("still says it's asking after every answer")
	}

	// Channels: detach one, then set what it relays and when it detaches.
	a.openBouncerChannels(a.bset)
	l := a.dlg.(*listBox)
	if len(l.Rows) != 2 || !strings.Contains(l.Rows[1], "parted, detached") {
		t.Fatalf("channels: %q", l.Rows)
	}
	l.press(a, "Detach")
	expect("channel update #trout/TroutNet -detached true", "network status")
	drain(5)
	a.openChannelOptions("#trout/TroutNet")
	f := a.dlg.(*formBox)
	f.focus = 0
	for range 3 { // unchanged, the default, nothing, highlights
		f.typed(a, []rune(" "))
	}
	f.Fields[3].Edit.Buf = []rune("2x")
	f.press(a, 0)
	if !strings.Contains(f.Note, "30m or 24h") {
		t.Fatalf("a bad time: note %q", f.Note)
	}
	f.Fields[3].Edit.Buf = []rune("24h")
	f.press(a, 0)
	expect("channel update #trout/TroutNet -relay-detached highlight -detach-after 24h")
	drain(6)

	// Deleting a user: soju answers with a token to send back.
	a.deleteUser("ann")
	a.dlg.(*msgBox).OnClose(a, 0)
	expect("user delete ann", "help help")
	feed(serv(`To confirm user deletion, send "user delete ann 1a2b3c"`), serv(servListEnd))
	expect("user delete ann 1a2b3c")
	drain(6)

	// A network's connect commands, which soju takes but never shows.
	n := a.nets[1]
	f = a.networkForm(n, nil)
	f.Fields[8].Edit.Buf = []rune("/mode max +x | PRIVMSG NickServ :identify secret")
	if _, err := a.saveNetwork(n, formVals(f)); err != nil {
		t.Fatal(err)
	}
	expect(`network update TroutNet -connect-command "mode max +x" -connect-command "PRIVMSG NickServ :identify secret"`)
	f.Fields[8].Edit.Buf = []rune("-")
	a.saveNetwork(n, formVals(f))
	expect(`network update TroutNet -connect-command ""`)
}

// TestBouncerSettingsNotAdmin: soju refuses user status to a user who
// isn't an admin; Users says so, and nothing else is asked.
func TestBouncerSettingsNotAdmin(t *testing.T) {
	a, sent := fakeSoju(t)
	a.Update(tea.WindowSizeMsg{Width: 110, Height: 34})
	a.openBouncerSettings(bncUsers)
	for range 6 {
		<-sent
	}
	for _, l := range []string{"no networks", servListEnd, servListEnd,
		"error: this command requires admin privileges", servListEnd} {
		m, _ := ircmsg.ParseLine(":BouncerServ!BouncerServ@BouncerServ PRIVMSG max :" + l)
		a.Update(ircLineMsg{a.home, m})
	}
	o := a.dlg.(*optionsBox)
	if got := strings.Join(o.pages[bncUsers].Notes, " "); !strings.Contains(got, "Only the bouncer's admins") || len(o.pages[bncUsers].Items) != 0 {
		t.Fatalf("Users for a non-admin: %q", got)
	}
	if strings.Contains(strings.Join(o.pages[bncServer].Notes, " "), "Asking") {
		t.Error("Server still asking")
	}
	select {
	case got := <-sent:
		t.Errorf("sent %q to a non-admin", got)
	default:
	}
}

func formVals(f *formBox) []string {
	v := make([]string, len(f.Fields))
	for i, fl := range f.Fields {
		v[i] = string(fl.Edit.Buf)
	}
	return v
}
