package ui

import (
	"strings"
	"testing"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

// TestHandleIRC feeds a channel's worth of server traffic through
// handleIRC and checks the windows, nicklist and lines that come out.
func TestHandleIRC(t *testing.T) {
	a := New(Options{Addr: "127.0.0.1:6667", User: "trout"})
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
