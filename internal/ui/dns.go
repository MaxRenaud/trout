package ui

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// /dns [-ch46] <nick | address>, like mIRC's (checked against it): a name
// with a dot in it, or an IP address, is looked up (an address for its
// name); anything else is a nick, whose host comes from the address list
// or else from USERHOST. -h takes it as a host name whatever it looks
// like, -4 and -6 want only those addresses, -c drops the lookups not
// done yet, and /dns alone lists them. What it finds goes to the
// network's status window, whichever window it was typed in, and to
// scripts' on DNS, whose /halt keeps the result's line out. /.dns says
// nothing: only on DNS hears about it.

// dnsWait is how long a lookup can take before it counts as failed, so a
// resolver that never answers doesn't leave it pending for good.
const dnsWait = 30 * time.Second

// dnsLookup is a /dns not done yet.
type dnsLookup struct {
	id     int
	n      *state.Network
	nick   string // whose address this is, for a nick
	addr   string // what's looked up
	asking bool   // USERHOST went out for the nick's address
	family string // "ip", or "ip4" or "ip6" for -4 and -6
	quiet  bool   // /.dns
}

// dnsMsg is a lookup's result: an address's name, or a name's addresses.
type dnsMsg struct {
	id   int
	ips  []string
	name string
}

func (a *App) dnsCommand(w *state.Window, rest string) tea.Cmd {
	n := w.Net
	sw, arg := "", strings.TrimSpace(rest)
	if strings.HasPrefix(arg, "-") {
		sw, arg, _ = strings.Cut(arg, " ")
		arg = strings.TrimSpace(arg)
	}
	arg, _, _ = strings.Cut(arg, " ")
	if strings.Contains(sw, "c") {
		// Quietly, like mIRC's, and not a nick still waiting for USERHOST,
		// which is looked up when the answer comes (checked).
		a.dnsLookups = slices.DeleteFunc(a.dnsLookups, func(l *dnsLookup) bool { return !l.asking })
		if arg == "" {
			return nil
		}
	}
	if arg == "" {
		a.dnsList(n.Status())
		return nil
	}
	a.dnsSeq++
	l := &dnsLookup{id: a.dnsSeq, n: n, family: "ip", quiet: a.quiet}
	switch four, six := strings.Contains(sw, "4"), strings.Contains(sw, "6"); {
	case four && !six:
		l.family = "ip4"
	case six && !four:
		l.family = "ip6"
	}
	if strings.Contains(sw, "h") || strings.ContainsAny(arg, ".:") {
		l.addr = arg
		a.dnsLookups = append(a.dnsLookups, l)
		return a.dnsResolve(l)
	}
	l.nick = arg
	if addr, ok := a.ial[n][n.Fold(arg)]; ok {
		// Someone in a channel with us: no USERHOST, like mIRC's (checked).
		_, l.addr, _ = strings.Cut(addr, "@")
		a.dnsLookups = append(a.dnsLookups, l)
		return a.dnsResolve(l)
	}
	if n == a.home || n.Down() || a.connFor(n) == nil || !a.sendNet(n, "USERHOST", arg) {
		a.addLine(w, state.RoleInfo, "* /dns: not connected to server")
		return nil
	}
	l.asking = true
	a.dnsLookups = append(a.dnsLookups, l)
	return nil
}

// dnsList is /dns alone: the addresses being looked up, one a line under
// a heading, like mIRC's; not a nick still waiting for USERHOST (checked).
func (a *App) dnsList(status *state.Window) {
	var addrs []string
	for _, l := range a.dnsLookups {
		if !l.asking {
			addrs = append(addrs, l.addr)
		}
	}
	if len(addrs) == 0 {
		a.addLine(status, state.RoleInfo, "* /dns: no addresses being resolved")
		return
	}
	a.addLine(status, state.RoleInfo, "* DNS resolving:")
	for _, addr := range addrs {
		a.addLine(status, state.RoleInfo, addr)
	}
}

// dnsResolve says what's being looked up and looks it up.
func (a *App) dnsResolve(l *dnsLookup) tea.Cmd {
	if !l.quiet {
		a.addLine(l.n.Status(), state.RoleInfo, "* DNS resolving "+l.addr)
	}
	id, addr, family := l.id, l.addr, l.family
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), dnsWait)
		defer cancel()
		msg := dnsMsg{id: id}
		if ip, err := netip.ParseAddr(addr); err == nil {
			// mIRC shows one name, as the system's reverse lookup gives it.
			if names, _ := net.DefaultResolver.LookupAddr(ctx, ip.String()); len(names) > 0 {
				msg.name = strings.TrimSuffix(names[0], ".")
			}
			return msg
		}
		ips, _ := net.DefaultResolver.LookupIP(ctx, family, addr)
		for _, ip := range ips {
			msg.ips = append(msg.ips, ip.String())
		}
		return msg
	}
}

// dnsDone is a lookup's result arriving. One dropped with -c meanwhile
// says nothing, like mIRC's (checked).
func (a *App) dnsDone(msg dnsMsg) tea.Cmd {
	i := slices.IndexFunc(a.dnsLookups, func(l *dnsLookup) bool { return l.id == msg.id })
	if i < 0 {
		return nil
	}
	l := a.dnsLookups[i]
	a.dnsLookups = slices.Delete(a.dnsLookups, i, i+1)
	text := "* DNS unable to resolve address " + l.addr
	switch {
	case msg.name != "":
		text = "* DNS resolved " + l.addr + " to " + msg.name
	case len(msg.ips) > 0:
		text = "* DNS resolved " + l.addr + " to " + strings.Join(msg.ips, ", ")
	}
	return a.dnsReport(l, &script.DNS{Addr: l.addr, IPs: msg.ips, Name: msg.name}, text)
}

// dnsUserhost takes RPL_USERHOST (302) for a nick's lookup waiting on it,
// and reports whether m was the answer. An empty one means they're not
// on; one about someone else is from your own /userhost. soju answers
// 421 while it isn't connected to the network.
func (a *App) dnsUserhost(n *state.Network, m ircmsg.Message) (tea.Cmd, bool) {
	i := slices.IndexFunc(a.dnsLookups, func(l *dnsLookup) bool { return l.n == n && l.asking })
	if i < 0 {
		return nil, false
	}
	l := a.dnsLookups[i]
	switch {
	case m.Command == "421" && len(m.Params) > 1 && strings.EqualFold(m.Params[1], "USERHOST"):
		a.dnsLookups = slices.Delete(a.dnsLookups, i, i+1)
		a.addLine(n.Status(), state.RoleInfo, "* /dns: not connected to server")
		return nil, true
	case m.Command != "302":
		return nil, false
	}
	replies := strings.Fields(lastParam(m.Params))
	for _, r := range replies {
		nick, addr, _ := strings.Cut(r, "=")
		if _, host, ok := strings.Cut(addr, "@"); ok && host != "" && n.Same(strings.TrimSuffix(nick, "*"), l.nick) {
			l.addr, l.asking = host, false
			return a.dnsResolve(l), true
		}
	}
	if len(replies) > 0 {
		return nil, false
	}
	a.dnsLookups = slices.Delete(a.dnsLookups, i, i+1)
	// mIRC's on DNS then has the nick as $address too (checked).
	return a.dnsReport(l, &script.DNS{Addr: l.nick}, "* /dns: no such user "+l.nick), true
}

// dnsReport ends a lookup: on DNS, then its line in the status window,
// unless a handler halted (checked) or it was /.dns.
func (a *App) dnsReport(l *dnsLookup, d *script.DNS, text string) tea.Cmd {
	var cmd tea.Cmd
	halted := false
	if a.scriptsOn() {
		ev := script.Event{Kind: "DNS", Network: l.n.Name, Nick: l.nick, Address: d.Addr, DNS: d}
		var actions []script.Action
		actions, halted = a.scripts.Dispatch(ev, a.scriptHost())
		cmd = a.runScript(actions)
	}
	if !halted && !l.quiet {
		a.addLine(l.n.Status(), state.RoleInfo, text)
	}
	return cmd
}
