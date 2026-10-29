package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

// CTCP: asking clients things (/ctcp, the nick popup) and answering them,
// like mIRC. DCC offers are CTCPs too; dcc.go takes those.

// ctcpVersion is what trout answers CTCP VERSION with.
func (a *App) ctcpVersion() string {
	return "trout " + cmp.Or(a.opts.Version, "dev") + ", an mIRC-inspired IRC client for the terminal"
}

// ctcpBurst answers in ctcpWindow keep a CTCP flood from making trout
// flood the network back, while a few requests in a row still get theirs.
const (
	ctcpBurst  = 4
	ctcpWindow = 10 * time.Second
)

// ctcpSplit takes a message apart if it's a CTCP: "\x01VERSION\x01" is
// ("VERSION", ""). ACTION isn't one here; it's shown as /me.
func ctcpSplit(text string) (cmd, args string, ok bool) {
	inner, found := strings.CutPrefix(text, "\x01")
	if !found {
		return "", "", false
	}
	inner = strings.TrimSuffix(inner, "\x01")
	cmd, args, _ = strings.Cut(inner, " ")
	cmd = strings.ToUpper(cmd)
	return cmd, args, cmd != "ACTION" && cmd != ""
}

// sendCTCP is /ctcp nick request [args]. A PING carries the time, so the
// reply says how long the round trip took.
func (a *App) sendCTCP(w *state.Window, nick, req string) {
	cmd, args, _ := strings.Cut(req, " ")
	cmd = strings.ToUpper(cmd)
	if cmd == "PING" && args == "" {
		args = strconv.FormatInt(time.Now().UnixMilli(), 10)
	}
	text := cmd
	if args != "" {
		text += " " + args
	}
	if a.sendNet(w.Net, "PRIVMSG", nick, "\x01"+text+"\x01") {
		a.addLine(w, state.RoleInfo, "-> ["+nick+"] "+cmd)
	}
}

// ctcp handles a CTCP request (PRIVMSG) or reply (NOTICE) from someone
// else. It reports whether m was one, so it isn't shown as a message.
func (a *App) ctcp(n *state.Network, m ircmsg.Message, t time.Time) bool {
	if len(m.Params) < 2 {
		return false
	}
	cmd, args, ok := ctcpSplit(m.Params[1])
	if !ok {
		return false
	}
	nick := m.Nick()
	if n.Same(nick, n.Nick) {
		// Our own, echoed, unless it was to ourselves, which mIRC answers.
		// That one comes twice (the echo and the delivery): skip the
		// second by its msgid.
		if !n.Same(m.Params[0], n.Nick) {
			return true
		}
		if ok, id := m.GetTag("msgid"); ok {
			if id == a.selfCTCP {
				return true
			}
			a.selfCTCP = id
		}
	}
	if m.Command == "NOTICE" {
		text := args
		if cmd == "PING" {
			if sent, err := strconv.ParseInt(args, 10, 64); err == nil {
				text = fmt.Sprintf("%.2fsecs", time.Since(time.UnixMilli(sent)).Seconds())
			}
		}
		a.addLineAt(a.whoisWin(n), state.RoleNotice, "["+nick+" "+cmd+" reply]: "+text, t)
		return true
	}
	if cmd == "DCC" {
		a.dccOffer(n, m, args, t)
		return true
	}
	where := n.Status()
	if a.ctcpActive { // Options > Messages
		where = a.whoisWin(n)
	}
	a.addLineAt(where, state.RoleNotice, "["+nick+" "+cmd+"]", t)
	reply := ""
	switch cmd {
	case "VERSION":
		reply = a.ctcpVersion()
	case "PING":
		reply = args
	case "TIME":
		reply = time.Now().Format(time.RFC1123Z)
	case "CLIENTINFO":
		reply = "ACTION CLIENTINFO DCC PING TIME VERSION"
	default:
		return true
	}
	// Not to a flood, and not to requests from history the bouncer
	// replays: the asker gave up long ago.
	a.ctcpSent = slices.DeleteFunc(a.ctcpSent, func(at time.Time) bool { return time.Since(at) > ctcpWindow })
	if len(a.ctcpSent) >= ctcpBurst || time.Since(t) > time.Minute {
		return true
	}
	a.ctcpSent = append(a.ctcpSent, time.Now())
	text := cmd
	if reply != "" {
		text += " " + reply
	}
	a.sendNet(n, "NOTICE", nick, "\x01"+text+"\x01")
	return true
}
