package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// TestDNS walks /dns as mIRC has it (checked against mIRC 7.86): names
// and addresses, nicks from the address list or USERHOST, -c, /dns alone,
// /.dns, and everything in the status window wherever it was typed.
func TestDNS(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	w := n.AddChannel(&state.Window{Kind: state.KChannel, Name: "#go", Joined: true})
	a.activate(w)
	st := n.Status()
	lines := func() []string {
		var out []string
		for _, l := range st.Lines {
			out = append(out, l.Text)
		}
		return out
	}
	expect := func(want ...string) {
		t.Helper()
		got := lines()
		if len(got) < len(want) || !slices.Equal(got[len(got)-len(want):], want) {
			t.Fatalf("status ends %q, want %q", got[max(0, len(got)-len(want)):], want)
		}
	}
	dns := func(line string) {
		t.Helper()
		a.typedCommand(w, line)
	}
	last := func() *dnsLookup { return a.dnsLookups[len(a.dnsLookups)-1] }
	answer := func(l *dnsLookup, name string, ips ...string) {
		a.Update(dnsMsg{id: l.id, ips: ips, name: name})
	}

	dns("dns irc.example.org")
	expect("* DNS resolving irc.example.org")
	l := last()
	dns("dns")
	expect("* DNS resolving:", "irc.example.org")
	answer(l, "", "192.0.2.1", "2001:db8::1")
	expect("* DNS resolved irc.example.org to 192.0.2.1, 2001:db8::1")
	dns("dns")
	expect("* /dns: no addresses being resolved")

	// An address is looked up for its name, IPv6 too; -h takes anything
	// as a name.
	dns("dns 192.0.2.1")
	answer(last(), "host.example")
	expect("* DNS resolving 192.0.2.1", "* DNS resolved 192.0.2.1 to host.example")
	dns("dns ::1")
	answer(last(), "")
	expect("* DNS resolving ::1", "* DNS unable to resolve address ::1")
	dns("dns -h nohost")
	answer(last(), "")
	expect("* DNS resolving nohost", "* DNS unable to resolve address nohost")
	for sw, family := range map[string]string{"-4": "ip4", "-6": "ip6", "-46": "ip", "-h4": "ip4"} {
		dns("dns " + sw + " x.example")
		if got := last().family; got != family {
			t.Errorf("/dns %s: %s, want %s", sw, got, family)
		}
	}
	dns("dns -c")
	if len(a.dnsLookups) != 0 {
		t.Fatalf("-c left %d lookups", len(a.dnsLookups))
	}

	// A nick in a channel with us: its host, without USERHOST.
	feed(":carol!c@carol.example JOIN #go")
	dns("dns carol")
	expect("* DNS resolving carol.example")
	if l := last(); l.nick != "carol" || l.asking {
		t.Fatalf("carol's lookup: %+v", l)
	}
	answer(last(), "", "198.51.100.7")
	expect("* DNS resolved carol.example to 198.51.100.7")

	// Anyone else: USERHOST. Not listed until its answer comes, and -c
	// keeps it.
	dns("dns bob")
	expectSent(t, sent, "USERHOST bob")
	dns("dns -c")
	dns("dns")
	expect("* /dns: no addresses being resolved")
	feed(":srv 302 max :erin=+e@erin.example") // your own /userhost, shown
	if !strings.Contains(lastLine(st), "erin") {
		t.Fatalf("someone else's USERHOST: %q", lastLine(st))
	}
	feed(":srv 302 max :bob=+b@bob.example")
	expect("* DNS resolving bob.example")
	answer(last(), "", "203.0.113.9")
	expect("* DNS resolved bob.example to 203.0.113.9")
	dns("dns nobody")
	expectSent(t, sent, "USERHOST nobody")
	feed(":srv 302 max :")
	expect("* /dns: no such user nobody")
	dns("dns dave")
	feed(":srv 421 max USERHOST :Disconnected from upstream network")
	expect("* /dns: not connected to server")

	// A lookup dropped with -c says nothing when it's done.
	dns("dns gone.example")
	gone := last()
	dns("dns -c kept.example")
	answer(gone, "", "192.0.2.2")
	expect("* DNS resolving gone.example", "* DNS resolving kept.example")

	// /.dns says nothing.
	before := len(st.Lines)
	dns(".dns quiet.example")
	answer(last(), "", "192.0.2.3")
	if len(st.Lines) != before {
		t.Fatalf("/.dns said %q", lines()[before:])
	}

	for _, l := range w.Lines {
		if strings.Contains(l.Text, "DNS") {
			t.Fatalf("a line in the channel: %q", l.Text)
		}
	}

	// Not connected: a nick can't be looked up, and that's said where it
	// was typed.
	delete(a.conns, n)
	dns("dns bob")
	if got := lastLine(w); got != "* /dns: not connected to server" {
		t.Fatalf("not connected: %q", got)
	}
}

// TestDNSScript: on DNS gets what mIRC's does (checked), its /halt keeps
// the line out, and a script's .dns is quiet.
func TestDNSScript(t *testing.T) {
	a, n, sent, feed := optionsApp(t)
	a.scripts = script.New(script.Options{})
	st := n.Status()
	a.scripts.Load("d.mrc", `on *:DNS:{
  echo -s ev $nick $+ / $+ $address i= $+ $iaddress n= $+ $naddress r= $+ $raddress $dns(0) $dns(1) $dns(1).ip $dns(1).addr $dns(1).nick $dns(2).ip
  if ($nick == hush) halt
}
alias q .dns $1`)
	answer := func(name string, ips ...string) {
		a.Update(dnsMsg{id: a.dnsLookups[len(a.dnsLookups)-1].id, ips: ips, name: name})
	}
	run := func(line string) { a.typedCommand(st, line) }
	for _, c := range []struct {
		typed, name string
		ips         []string
		event, line string
	}{
		{"dns a.example", "", []string{"192.0.2.1", "192.0.2.2"},
			"ev/a.example i=192.0.2.1 n=a.example r=192.0.2.1 2 a.example 192.0.2.1 a.example 192.0.2.2",
			"* DNS resolved a.example to 192.0.2.1, 192.0.2.2"},
		{"dns 192.0.2.1", "a.example", nil,
			"ev/192.0.2.1 i=192.0.2.1 n=a.example r=a.example 1 192.0.2.1 192.0.2.1 a.example",
			"* DNS resolved 192.0.2.1 to a.example"},
		{"dns 10.255.255.1", "", nil, "ev/10.255.255.1 i=10.255.255.1 n= r= 0",
			"* DNS unable to resolve address 10.255.255.1"},
		{"dns b.invalid", "", nil, "ev/b.invalid i= n=b.invalid r= 0",
			"* DNS unable to resolve address b.invalid"},
	} {
		run(c.typed)
		answer(c.name, c.ips...)
		got := st.Lines[len(st.Lines)-2:]
		if got[0].Text != c.event || got[1].Text != c.line {
			t.Errorf("/%s: %q, %q; want %q, %q", c.typed, got[0].Text, got[1].Text, c.event, c.line)
		}
	}

	// A nick: $nick, and $address its host; one who isn't on has the
	// nick as $address.
	feed(":carol!c@carol.example JOIN #go")
	run("dns carol")
	answer("", "198.51.100.7")
	if got := st.Lines[len(st.Lines)-2].Text; got != "ev carol/carol.example i=198.51.100.7 n=carol.example r=198.51.100.7 1 carol.example 198.51.100.7 carol.example carol" {
		t.Errorf("a nick: %q", got)
	}
	run("dns nobody")
	expectSent(t, sent, "USERHOST nobody")
	feed(":srv 302 max :")
	if got := st.Lines[len(st.Lines)-2:]; got[0].Text != "ev nobody/nobody i= n=nobody r= 0" || got[1].Text != "* /dns: no such user nobody" {
		t.Errorf("not on: %q, %q", got[0].Text, got[1].Text)
	}

	// /halt keeps the line out, but not the "resolving" one before it.
	run("dns hush")
	feed(":srv 302 max :hush=+h@hush.example")
	answer("", "192.0.2.9")
	if got := lastLine(st); !strings.HasPrefix(got, "ev hush/") || lastText(st, 2) != "* DNS resolving hush.example" {
		t.Errorf("halted: %q after %q", got, lastText(st, 2))
	}
	run("dns hush")
	feed(":srv 302 max :")
	if got := lastLine(st); !strings.HasPrefix(got, "ev hush/") {
		t.Errorf("halted, not on: %q", got)
	}

	// A script's .dns: only on DNS hears of it.
	run("q c.example")
	if got := lastLine(st); strings.Contains(got, "resolving") {
		t.Fatalf(".dns in a script said %q", got)
	}
	answer("", "192.0.2.4")
	if got := lastLine(st); !strings.HasPrefix(got, "ev/c.example") {
		t.Fatalf(".dns in a script: %q", got)
	}

	// Outside on DNS, like mIRC's: $dns(0) is 0, the rest empty.
	acts, _ := a.scripts.Command(n.Name, "", "echo -s < $+ $dns(0) $dns(1) $iaddress $naddress $raddress $+ >", true, a.scriptHost())
	if got := acts[0].Echo.Text; got != "<0>" {
		t.Errorf("outside on DNS: %q", got)
	}
}

// TestDNSLookup does a real lookup, of localhost, which needs no network.
func TestDNSLookup(t *testing.T) {
	a, n, _, _ := optionsApp(t)
	cmd := a.dnsCommand(n.Status(), "-h localhost")
	msg, ok := cmd().(dnsMsg)
	if !ok || !slices.ContainsFunc(msg.ips, func(ip string) bool { return ip == "127.0.0.1" || ip == "::1" }) {
		t.Fatalf("localhost: %+v", msg)
	}
	a.Update(msg)
	if got := lastLine(n.Status()); !strings.HasPrefix(got, "* DNS resolved localhost to ") {
		t.Fatalf("got %q", got)
	}
}

// lastText is the text of w's nth line from the end (1 is the last).
func lastText(w *state.Window, nth int) string { return w.Lines[len(w.Lines)-nth].Text }
