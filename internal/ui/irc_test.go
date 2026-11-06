package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestHandleIRC feeds a channel's worth of server traffic through
// handleIRC and checks the windows, nicklist and lines that come out.
func TestHandleIRC(t *testing.T) {
	a := New(Options{Bouncer: config.Bouncer{Address: "irc+insecure://127.0.0.1:6667", User: "trout"}})
	n := state.NewNetwork("TestNet")
	n.Nick = "trout"
	a.nets = append(a.nets, n)
	feed := func(lines ...string) {
		for _, l := range lines {
			m, err := ircmsg.ParseLine(l)
			if err != nil {
				t.Fatalf("parsing %q: %v", l, err)
			}
			a.handleIRC(n, m)
		}
	}

	feed(
		":ergo 005 trout PREFIX=(ov)@+ CHANMODES=beI,k,l,imnpst :are supported by this server",
		":trout!t@h JOIN #test",
		":ergo 353 trout = #test :@alice +bob trout",
		":ergo 366 trout #test :End of /NAMES list",
		":ergo 332 trout #test :hello world",
		":ergo 324 trout #test +nt",
		":carol!c@h JOIN #test",
		":alice!a@h MODE #test +o carol",
		":alice!a@h MODE #test +k secret",
		":bob!b@h NICK bobby",
		":bobby!b@h PRIVMSG #test :hi trout",
		":alice!a@h PRIVMSG #test :\x01ACTION waves\x01",
		":alice!a@h PRIVMSG trout :psst",
		":dave!d@h PRIVMSG trout :\x01VERSION\x01",
		":carol!c@h QUIT :bye",
		":soju BATCH +h1 chathistory #test",
		"@batch=h1;time=2026-01-01T00:00:00.000Z :zed!z@h PRIVMSG #test :from history",
		":soju BATCH -h1",
	)

	w := n.Find(state.KChannel, "#test")
	if w == nil || !w.Joined {
		t.Fatal("no joined #test window")
	}
	var nicks []string
	for _, nk := range w.Nicks {
		nicks = append(nicks, nk.Prefix+nk.Name)
	}
	if got := strings.Join(nicks, " "); got != "@alice +bobby trout" {
		t.Errorf("nicklist = %q, want %q", got, "@alice +bobby trout")
	}
	if w.Topic != "hello world" || w.Modes != "ntk" {
		t.Errorf("topic %q, modes %q; want hello world, ntk", w.Topic, w.Modes)
	}
	if first := w.Lines[0]; first.Text != "<zed> from history" {
		t.Errorf("first line = %q, want the history line in front", first.Text)
	}
	byText := map[string]state.Role{}
	for _, l := range w.Lines {
		byText[l.Text] = l.Role
	}
	for text, role := range map[string]state.Role{
		"<bobby> hi trout":            state.RoleHighlight,
		"* alice waves":               state.RoleAction,
		"* bob is now known as bobby": state.RoleEvent,
		"* Quits: carol (c@h) (bye)":  state.RoleQuit,
	} {
		if got, ok := byText[text]; !ok || got != role {
			t.Errorf("line %q: role %v, present %v; want role %v", text, got, ok, role)
		}
	}
	if q := n.Find(state.KQuery, "alice"); q == nil || q.Lines[len(q.Lines)-1].Text != "<alice> psst" {
		t.Error("a private message didn't open a query window with it")
	}
	if n.Find(state.KQuery, "dave") != nil {
		t.Error("a CTCP VERSION opened a query window; CTCPs other than ACTION are ignored")
	}

	feed(":trout!t@h PART #test")
	if n.Find(state.KChannel, "#test") != nil {
		t.Error("parting didn't close the channel window")
	}
}

// TestNotSent: with echo-message, your messages show when the bouncer
// echoes them; if the connection dies first (a bouncer that vanished),
// the ones it never echoed say so instead of quietly disappearing.
func TestNotSent(t *testing.T) {
	a, n, _, feed := optionsApp(t)
	q := a.openQuery(n, "bob")
	a.say(q, "first")
	a.say(q, "second")
	a.action(q, "waves")
	feed(":max!m@h PRIVMSG bob :first") // the echo of the first
	a.Update(ircDownMsg{n, irc.ErrNoReply})
	lines := lineTexts(q)
	want := []string{"<max> first", "* Not sent, the connection died first: second", "* Not sent, the connection died first: /me waves"}
	if !slices.Equal(lines, want) {
		t.Fatalf("query:\n%q\nwant\n%q", lines, want)
	}
	if !strings.Contains(lastLine(n.Status()), "no answer, even to a ping") {
		t.Fatalf("status: %q", lastLine(n.Status()))
	}
	if len(a.unechoed[n]) != 0 {
		t.Fatal("still waiting on the dead connection's messages")
	}
}

// TestMsgNoQuery: /msg and /describe to someone without a query window
// show "-> *nick*" in the window they were typed in and don't open one,
// like mIRC, even when the bouncer echoes them (/msg X on Undernet). With
// a query open, the echo still lands there.
func TestMsgNoQuery(t *testing.T) {
	a, n, _, feed := optionsApp(t)
	ch := n.Add(&state.Window{Kind: state.KChannel, Name: "#test"})
	a.ircCommand(ch, "msg X login max pw")
	a.ircCommand(ch, "describe X waves")
	feed(":max!m@h PRIVMSG X :login max pw", ":max!m@h PRIVMSG X :\x01ACTION waves\x01")
	if n.Find(state.KQuery, "X") != nil {
		t.Fatal("/msg opened a query window")
	}
	want := []string{"-> *X* login max pw", "-> *X* max waves"}
	if got := lineTexts(ch); !slices.Equal(got, want) {
		t.Fatalf("channel:\n%q\nwant\n%q", got, want)
	}

	q := a.openQuery(n, "bob")
	a.ircCommand(ch, "msg bob hi")
	feed(":max!m@h PRIVMSG bob :hi")
	if got := lastLine(ch); got != "-> *bob* hi" {
		t.Errorf("channel: %q", got)
	}
	if got := lineTexts(q); !slices.Equal(got, []string{"<max> hi"}) {
		t.Errorf("query: %q", got)
	}
	if len(a.unechoed[n]) != 0 {
		t.Fatal("still waiting on echoes")
	}
}

// TestNickQueries: a query follows a nick change, matched by the network's
// CASEMAPPING, but never onto a query already open with the new nick.
func TestNickQueries(t *testing.T) {
	a, n, _, feed := optionsApp(t)
	bob := a.openQuery(n, "bob[1]")
	carol := a.openQuery(n, "carol")
	feed(":bob{1}!b@h NICK bobby") // rfc1459: {} are []'s lowercase
	if bob.Name != "bobby" {
		t.Fatalf("bob[1]'s query is %q after bob{1} became bobby", bob.Name)
	}
	feed(":bobby!b@h NICK carol")
	queries := 0
	for _, w := range n.Wins {
		if w.Kind == state.KQuery && n.Same(w.Name, "carol") {
			queries++
		}
	}
	// Like mIRC (checked): bobby's query keeps its name, and neither says
	// anything about it.
	if queries != 1 || bob.Name != "bobby" || len(carol.Lines)+len(bob.Lines) != 0 {
		t.Fatalf("%d carol queries, bob's is %q; carol's says %q, bobby's %q", queries, bob.Name, lineTexts(carol), lineTexts(bob))
	}
	feed(":CAROL!c@h QUIT :bye")
	if lastLine(carol) != "* Quits: CAROL (c@h) (bye)" {
		t.Fatalf("carol's query: %q", lastLine(carol))
	}
}

// TestJoinList: /join a,#b[1] joins #a and #b[1], and each comes to the
// front when its JOIN does, matched by CASEMAPPING.
func TestJoinList(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	a.ircCommand(n.Status(), "join a,#b[1]")
	expectSent(t, sent, "JOIN #a,#b[1]")
	feed(":max!m@h JOIN #A")
	if a.active.Name != "#A" {
		t.Fatalf("active is %q after joining #A", a.active.Name)
	}
	feed(":max!m@h JOIN #B{1}")
	if a.active.Name != "#B{1}" || len(a.joining) != 0 {
		t.Fatalf("active is %q after joining #B{1}; still joining %v", a.active.Name, a.joining)
	}
}

// TestISupportChanges: CHANTYPES decides what's a channel, and a -TOKEN
// takes a feature back.
func TestISupportChanges(t *testing.T) {
	a, n, _ := realApp(t)
	feed := func(l string) { m, _ := ircmsg.ParseLine(l); a.handleIRC(n, m) }
	feed(":srv 005 max CHANTYPES=# MONITOR=100 :are supported by this server")
	if n.IsChannel("&local") || !n.IsChannel("#go") || !n.Monitor {
		t.Fatalf("CHANTYPES=#: &local a channel %v, #go %v; monitor %v", n.IsChannel("&local"), n.IsChannel("#go"), n.Monitor)
	}
	feed(":srv 005 max -CHANTYPES -MONITOR :are supported by this server")
	if !n.IsChannel("&local") || n.Monitor || a.isupport[n]["MONITOR"] != "" {
		t.Fatalf("after -CHANTYPES -MONITOR: &local a channel %v, monitor %v", n.IsChannel("&local"), n.Monitor)
	}
}

// TestNoNetworksYet: a bouncer with no networks (a new one, This Computer
// the first time) says how to add one and opens Select Server, once.
func TestNoNetworksYet(t *testing.T) {
	a, _ := fakeSoju(t)
	feed := func(lines ...string) {
		for _, l := range lines {
			m, _ := ircmsg.ParseLine(l)
			a.Update(ircLineMsg{a.home, m})
		}
	}
	feed("BATCH +n soju.im/bouncer-networks", "BATCH -n")
	if _, ok := a.dlg.(*serversBox); !ok || !strings.Contains(lastLine(a.home.Status()), "/server irc.libera.chat") {
		t.Fatalf("no networks: %T, %q", a.dlg, lastLine(a.home.Status()))
	}
	a.dlg = nil
	feed("BATCH +m soju.im/bouncer-networks", "BATCH -m") // a reconnect
	if a.dlg != nil {
		t.Fatal("asked again")
	}
}

// TestShortLines: a line with fewer parameters than its command has, from
// a broken bouncer or a network, is shown or dropped, not a panic that
// quits trout. Every command handleIRC knows and every numeric, with 0 to 3
// parameters, from the bouncer's own connection and from a network.
func TestShortLines(t *testing.T) {
	a, n, _ := realApp(t)
	cmds := []string{"AWAY", "BATCH", "BOUNCER", "CAP", "CHGHOST", "CLIENTCERT", "ERROR", "FAIL",
		"INVITE", "JOIN", "KICK", "MARKREAD", "MODE", "NICK", "NOTICE", "PART", "PING", "PRIVMSG",
		"QUIT", "TAGMSG", "TOPIC", "WALLOPS", "ACCOUNT", "SETNAME"}
	for i := 1; i < 1000; i++ {
		cmds = append(cmds, fmt.Sprintf("%03d", i))
	}
	params := []string{"", " max", " max #go", " max #go :x"}
	for _, net := range []*state.Network{n, a.home} {
		for _, c := range cmds {
			for _, src := range []string{":srv ", ":bob!b@h ", ""} {
				for _, p := range params {
					l := src + c + p
					m, err := ircmsg.ParseLine(l)
					if err != nil {
						continue
					}
					func() {
						defer func() {
							if r := recover(); r != nil {
								t.Errorf("%q on %s: %v", l, net.Name, r)
							}
						}()
						a.handleIRC(net, m)
					}()
				}
			}
		}
	}
}
