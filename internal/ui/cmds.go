package ui

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// More of mIRC's commands, from its help file, that a person (not a
// script) types. ircCommand tries these before sending a command to the
// server as it is.

// switchCommands take mIRC's -switches, which trout doesn't use but must
// not send along (like /query -n bob, /msg -m).
var switchCommands = []string{"join", "query", "msg", "privmsg", "notice", "describe", "raw", "quote", "clear", "close", "part"}

// stripSwitches takes a leading -switch off rest for those commands.
func stripSwitches(name, rest string) (switches, out string) {
	if strings.HasPrefix(rest, "-") && len(rest) > 1 && slices.Contains(switchCommands, strings.ToLower(name)) {
		switches, out, _ = strings.Cut(rest, " ")
		return switches, strings.TrimSpace(out)
	}
	return "", rest
}

// mircCommand handles one of those commands, reporting whether it was one.
func (a *App) mircCommand(w *state.Window, name, rest string) (tea.Cmd, bool) {
	arg, tail, _ := strings.Cut(rest, " ")
	n := w.Net
	need := func() { a.addLine(w, state.RoleInfo, "* /"+name+": insufficient parameters") }
	switch strings.ToLower(name) {
	case "uwho": // the nick's info, like mIRC's user info box
		nick := arg
		if nick == "" && w.Kind == state.KQuery {
			nick = w.Name
		}
		if nick == "" {
			need()
			return nil, true
		}
		a.openNickInfo(n, nick)
		return nil, true
	case "closemsg": // closes a query
		if q := n.Find(state.KQuery, arg); q != nil {
			a.closeWindow(q)
		}
		return nil, true
	case "beep": // the terminal bell, which is trout's only sound
		a.bellDue = true
		return nil, true
	case "flash":
		// mIRC flashes its window when it isn't in front: the bell is the
		// terminal's way to get your attention.
		if !a.focused {
			a.bellDue = true
		}
		return nil, true
	case "ping": // /ping nick is a CTCP PING, like mIRC's
		if arg == "" {
			need()
			return nil, true
		}
		a.sendCTCP(w, arg, "PING")
		return nil, true
	case "timestamp":
		// /timestamp -f format sets the format (mIRC's, like trout's own);
		// /timestamp [on|off] switches them, for every window. -g is the
		// log's format: the bouncer keeps the logs.
		sw, arg := "", strings.TrimSpace(rest)
		if strings.HasPrefix(arg, "-") {
			sw, arg, _ = strings.Cut(arg, " ")
			arg = strings.TrimSpace(arg)
		}
		switch {
		case strings.Contains(sw, "f"):
			if arg != "" {
				a.stampFormat = arg
			}
		case strings.Contains(sw, "g"):
		case strings.EqualFold(arg, "on"):
			a.stamps = true
		case strings.EqualFold(arg, "off"):
			a.stamps = false
		case arg == "":
			a.stamps = !a.stamps
		}
		return nil, true
	case "privmsg": // like /msg
		return a.ircCommand(w, "msg "+rest), true
	case "say": // to the active window, even text starting with /
		if rest == "" {
			need()
		} else {
			a.say(w, rest)
		}
	case "slap": // not mIRC's own (its slap is the nick popup's), but the alias everyone wrote
		switch {
		case arg == "":
			need()
		case w.Kind != state.KChannel && w.Kind != state.KQuery && w.Kind != state.KChat:
			a.addLine(w, state.RoleInfo, "* /slap: slap someone from a channel or a query")
		default:
			a.action(w, slapText(arg))
		}
	case "describe": // /describe <nick|#chan> action
		if tail == "" {
			need()
			break
		}
		if t := a.windowFor(n, arg); t != nil {
			a.action(t, tail)
			break
		}
		for _, part := range a.sendText(n, "PRIVMSG", arg, tail, asAction) {
			a.sentElsewhere(w, arg, part, state.RoleAction, "-> *"+arg+"* "+n.Nick+" "+part)
		}
	case "omsg", "onotice": // to a channel's ops: @#chan
		ch, text := a.channelArg(w, rest)
		if ch == "" || text == "" {
			need()
			break
		}
		cmd := map[string]string{"omsg": "PRIVMSG", "onotice": "NOTICE"}[strings.ToLower(name)]
		for _, part := range a.sendText(n, cmd, "@"+ch, text, plain) {
			a.addLine(w, state.RoleNotice, "-> @"+ch+" "+part)
		}
	case "qmsg", "qme": // to every query window on this network
		if rest == "" {
			need()
			break
		}
		for _, q := range n.Wins {
			if q.Kind != state.KQuery {
				continue
			}
			if strings.EqualFold(name, "qme") {
				a.action(q, rest)
			} else {
				a.privmsg(q, q.Name, rest)
			}
		}
	case "hop": // part and rejoin, keeping the window
		ch, msg := a.channelArg(w, rest)
		if ch == "" {
			need()
			break
		}
		a.hopping[n.Fold(ch)] = true
		if msg != "" {
			a.sendNet(n, "PART", ch, msg)
		} else {
			a.sendNet(n, "PART", ch)
		}
		a.sendNet(n, "JOIN", ch)
	case "partall":
		for _, ch := range n.Wins {
			if ch.Kind == state.KChannel && ch.Joined {
				if rest != "" {
					a.sendNet(n, "PART", ch.Name, rest)
				} else {
					a.sendNet(n, "PART", ch.Name)
				}
			}
		}
	case "ctcpreply": // /ctcpreply nick ctcp [message]
		f := strings.SplitN(rest, " ", 3)
		if len(f) < 2 {
			need()
			break
		}
		text := strings.ToUpper(f[1])
		if len(f) == 3 {
			text += " " + f[2]
		}
		if a.sendNet(n, "NOTICE", f[0], "\x01"+text+"\x01") {
			a.addLine(w, state.RoleInfo, "-> ["+f[0]+"] "+text)
		}
	case "disconnect":
		a.disconnectNet(n)
	case "dcc":
		sub, more, _ := strings.Cut(rest, " ")
		switch strings.ToLower(sub) {
		case "send": // through the bouncer's file host, like Send File
			to, file, _ := strings.Cut(strings.TrimSpace(more), " ")
			if file == "" {
				a.openSendFile(to)
				break
			}
			path := strings.TrimSpace(file)
			if home, err := os.UserHomeDir(); err == nil {
				if r, ok := strings.CutPrefix(path, "~/"); ok {
					path = filepath.Join(home, r)
				}
			}
			if n.FileHost == "" {
				a.addLine(w, state.RoleInfo, "* This bouncer doesn't take file uploads")
				break
			}
			a.addLine(w, state.RoleInfo, "* Uploading "+filepath.Base(path)+"...")
			return a.upload(w, to, path), true
		case "chat":
			a.addLine(w, state.RoleInfo, "* Starting a DCC chat needs an open port on this computer, which trout doesn't do; you can accept chats")
		default:
			a.addLine(w, state.RoleInfo, "* /dcc send <nick> <file> uploads a file and sends the link")
		}
	case "channel": // Channel Central
		ch := w
		if arg != "" {
			ch = n.Find(state.KChannel, arg)
		}
		if ch == nil || ch.Kind != state.KChannel {
			a.addLine(w, state.RoleInfo, "* /channel: not in that channel")
			break
		}
		a.openChannelModes(ch)
	case "findtext":
		if rest != "" {
			a.findText = rest
		}
		a.openFind()
	case "savebuf":
		a.saveWindow(w)
	case "url":
		a.openURLsList()
	case "abook":
		a.openAddressBook()
	case "markasread":
		a.markWindowRead(w)
	default:
		return nil, false
	}
	return nil, true
}

// windowFor is n's open window for a nick or channel.
func (a *App) windowFor(n *state.Network, target string) *state.Window {
	if n.IsChannel(target) {
		return n.Find(state.KChannel, target)
	}
	return n.Find(state.KQuery, target)
}

// channelArg splits "[#chan] rest": a leading channel, else the window's.
func (a *App) channelArg(w *state.Window, rest string) (ch, text string) {
	first, more, _ := strings.Cut(rest, " ")
	if w.Net.IsChannel(first) {
		return first, strings.TrimSpace(more)
	}
	if w.Kind == state.KChannel {
		return w.Name, rest
	}
	return "", rest
}

// markWindowRead is /markasread: read up to now, here and for the bouncer.
func (a *App) markWindowRead(w *state.Window) {
	w.Act = state.ActNone
	focused := a.focused
	a.focused = true
	a.sendRead(w)
	a.focused = focused
}

// lastInvited is the channel you were last invited to, for /join -i.
func (a *App) lastInvited(n *state.Network) string { return cmp.Or(a.invitedTo[n], "") }

// slapText is mIRC's slap, for the nick popup and /slap.
func slapText(nick string) string { return "slaps " + nick + " around a bit with a large trout" }
