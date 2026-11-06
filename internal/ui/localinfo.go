package ui

import (
	"context"
	"net"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// localInfo is mIRC's Options > Connect > Local Info with its Server
// lookup method: on connecting, it asks the server for your own address
// (USERHOST) and looks the host up, for $host and $ip. Through a bouncer
// that's your address as the network sees it, the bouncer's, and not the
// end of trout's connection to the bouncer. on CONNECT waits for it, like
// mIRC's (checked).
type localInfo struct {
	host, ip string
	asked    bool // USERHOST went out and hasn't been answered
	done     bool // the lookup is over, found or not
	connect  bool // the MOTD ended meanwhile: on CONNECT is waiting
}

// localInfoWait is how long on CONNECT waits for the lookup: soju may
// not answer USERHOST while the network is down.
const localInfoWait = 10 * time.Second

type localIPMsg struct {
	n  *state.Network
	li *localInfo
	ip string
}

type localInfoTimeoutMsg struct {
	n  *state.Network
	li *localInfo
}

// askLocalInfo starts the lookup when the server welcomes us (001), like
// mIRC's, which sends USERHOST right after it (checked).
func (a *App) askLocalInfo(n *state.Network) tea.Cmd {
	if n == a.home || a.opts.Demo || a.connFor(n) == nil {
		return nil
	}
	li := &localInfo{asked: true}
	if a.localInfo == nil {
		a.localInfo = map[*state.Network]*localInfo{}
	}
	a.localInfo[n] = li
	a.sendNet(n, "USERHOST", n.Nick)
	return tea.Tick(localInfoWait, func(time.Time) tea.Msg { return localInfoTimeoutMsg{n, li} })
}

// localInfoReply takes RPL_USERHOST (302) about ourselves while the lookup
// is waiting for it, and doesn't show it. A host that's an address is
// $ip with $host empty; any other is $host, and $ip what it resolves to,
// or empty if it doesn't (a cloak), like mIRC's (checked).
func (a *App) localInfoReply(n *state.Network, m ircmsg.Message) (tea.Cmd, bool) {
	li := a.localInfo[n]
	if m.Command != "302" || li == nil || !li.asked {
		return nil, false
	}
	host := ""
	for r := range strings.FieldsSeq(lastParam(m.Params)) {
		nick, addr, _ := strings.Cut(r, "=")
		if n.Same(strings.TrimSuffix(nick, "*"), n.Nick) {
			_, host, _ = strings.Cut(addr, "@")
		}
	}
	if host == "" && lastParam(m.Params) != "" {
		return nil, false // someone else's: your own /userhost
	}
	li.asked = false
	if net.ParseIP(host) != nil {
		li.ip = host
		return a.localInfoDone(n, li), true
	}
	if host == "" {
		return a.localInfoDone(n, li), true
	}
	li.host = host
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), localInfoWait)
		defer cancel()
		ips, _ := net.DefaultResolver.LookupIP(ctx, "ip", host)
		ip := ""
		for _, v := range ips { // IPv4 first, like mIRC's
			if v.To4() != nil {
				ip = v.String()
				break
			}
		}
		if ip == "" && len(ips) > 0 {
			ip = ips[0].String()
		}
		return localIPMsg{n, li, ip}
	}, true
}

// localInfoUpdate is the host's address arriving, or the wait running out.
func (a *App) localInfoUpdate(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case localIPMsg:
		if a.localInfo[msg.n] == msg.li {
			msg.li.ip = msg.ip
			return a.localInfoDone(msg.n, msg.li)
		}
	case localInfoTimeoutMsg:
		if a.localInfo[msg.n] == msg.li {
			msg.li.asked = false
			return a.localInfoDone(msg.n, msg.li)
		}
	}
	return nil
}

func (a *App) localInfoDone(n *state.Network, li *localInfo) tea.Cmd {
	if li.done {
		return nil
	}
	li.done = true
	if li.connect {
		return a.scriptEvent(script.Event{Kind: "CONNECT", Network: n.Name})
	}
	return nil
}

// motdEnded is on CONNECT, at the end of the MOTD like mIRC's, once the
// local info lookup is over.
func (a *App) motdEnded(n *state.Network) tea.Cmd {
	if li := a.localInfo[n]; li != nil && !li.done {
		li.connect = true
		return nil
	}
	return a.scriptEvent(script.Event{Kind: "CONNECT", Network: n.Name})
}
