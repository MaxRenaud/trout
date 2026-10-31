package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// Per-network lists saved in config.toml: favorite channels, people to
// ignore, and people to watch for (the notify list).

type listKind int

const (
	listFavorites listKind = iota
	listIgnore
	listNotify
	listKinds
)

// listMap is where a kind of list lives: config.toml, or memory without
// one (tests).
func (a *App) listMap(k listKind) map[string][]string {
	if a.cfg == nil {
		if a.lists[k] == nil {
			a.lists[k] = map[string][]string{}
		}
		return a.lists[k]
	}
	m := []*map[string][]string{&a.cfg.Favorites, &a.cfg.Ignore, &a.cfg.Notify}[k]
	if *m == nil {
		*m = map[string][]string{}
	}
	return *m
}

func (a *App) netList(k listKind, n *state.Network) []string { return a.listMap(k)[n.Name] }

// setInList adds or removes item from n's list, keeping it sorted, and
// saves the config. It reports whether that changed anything.
func (a *App) setInList(k listKind, n *state.Network, item string, on bool) bool {
	m := a.listMap(k)
	old := m[n.Name]
	list := slices.DeleteFunc(slices.Clone(old), func(s string) bool { return strings.EqualFold(s, item) })
	if on {
		list = append(list, item)
		slices.SortFunc(list, func(x, y string) int { return strings.Compare(strings.ToLower(x), strings.ToLower(y)) })
	}
	if slices.Equal(list, old) {
		return false
	}
	if len(list) == 0 {
		delete(m, n.Name)
	} else {
		m[n.Name] = list
	}
	a.saveConfig()
	return true
}

// ignoreMask turns "nick" into "nick!*@*"; a full mask stays as it is.
func ignoreMask(s string) string {
	if strings.ContainsAny(s, "!@") {
		return s
	}
	return s + "!*@*"
}

// ignored reports whether m comes from someone ignored for its kind of
// message (see ignoreEntry). Like mIRC, it hides messages, notices,
// CTCPs, DCC offers and invites, not joins and parts, and an open query's
// private messages still come through.
func (a *App) ignored(n *state.Network, m ircmsg.Message) bool {
	e, ok := a.ignoreMatch(n, m)
	if !ok {
		return false
	}
	kind := ignoreKind(n, m)
	if kind == 'p' && n.Find(state.KQuery, m.Nick()) != nil {
		return false
	}
	return strings.IndexByte(e.flags, kind) >= 0
}

// ignoreCommand is /ignore [-pcntikd] [-uSECONDS] [nick|mask] and
// /unignore nick|mask, like mIRC; /ignore alone lists the network's
// ignores.
func (a *App) ignoreCommand(w *state.Window, on bool, rest string) {
	n := w.Net
	e := ignoreEntry{flags: "pcntid"}
	var arg string
	for f := range strings.FieldsSeq(rest) {
		switch {
		case strings.HasPrefix(f, "-u"):
			if secs, err := strconv.Atoi(f[2:]); err == nil {
				e.until = time.Now().Add(time.Duration(secs) * time.Second)
			}
		case f == "-r": // like mIRC, /ignore -r removes
			on = false
		case strings.HasPrefix(f, "-"):
			e.flags = strings.Trim(f[1:], "u0123456789")
		case arg != "" && isDigits(f):
			// mIRC's mask type after the nick (/ignore nick 1): trout
			// ignores by nick!*@*, so it's left out.
		default:
			arg = f
		}
	}
	if arg == "" {
		list := a.netList(listIgnore, n)
		if len(list) == 0 {
			a.addLine(w, state.RoleInfo, "* Nobody is ignored on "+n.Name)
			return
		}
		a.addLine(w, state.RoleInfo, "* Ignored on "+n.Name+": "+strings.Join(list, ", "))
		return
	}
	e.mask = ignoreMask(arg)
	a.setIgnoreEntry(w, e, on)
}

func (a *App) setIgnore(w *state.Window, who string, on bool) {
	a.setIgnoreEntry(w, ignoreEntry{mask: ignoreMask(who), flags: "pcntid"}, on)
}

// setIgnoreEntry adds an entry (replacing one for the same mask), or
// removes the one for its mask.
func (a *App) setIgnoreEntry(w *state.Window, e ignoreEntry, on bool) {
	found := false
	for _, s := range a.netList(listIgnore, w.Net) {
		if strings.EqualFold(parseIgnore(s).mask, e.mask) {
			a.setInList(listIgnore, w.Net, s, false)
			found = true
		}
	}
	switch {
	case on:
		a.setInList(listIgnore, w.Net, e.String(), true)
		a.addLine(w, state.RoleInfo, "* Now ignoring "+e.String()+" on "+w.Net.Name)
	case found:
		a.addLine(w, state.RoleInfo, "* No longer ignoring "+e.mask+" on "+w.Net.Name)
	default:
		a.addLine(w, state.RoleInfo, "* "+e.mask+" isn't ignored")
	}
}

// setNotify adds or removes nick from n's notify list.
func (a *App) setNotify(n *state.Network, nick string, on bool) {
	a.setNotifyEntry(n, notifyEntry{nick: nick}, on)
}

// setNotifyEntry adds an entry (replacing one for the same nick) or
// removes the nick, and tells the network to watch them (MONITOR) or
// asks with ISON.
func (a *App) setNotifyEntry(n *state.Network, e notifyEntry, on bool) {
	had := false
	for _, s := range a.netList(listNotify, n) {
		if n.Same(parseNotify(s).nick, e.nick) {
			a.setInList(listNotify, n, s, false)
			had = true
		}
	}
	if on {
		a.setInList(listNotify, n, e.String(), true)
	} else {
		delete(n.Online, n.Fold(e.nick))
	}
	switch {
	case a.conns[n] == nil || on == had:
	case n.Monitor && on:
		a.sendNet(n, "MONITOR", "+", e.nick)
	case n.Monitor:
		a.sendNet(n, "MONITOR", "-", e.nick)
	case on:
		a.sendNet(n, "ISON", e.nick) // the poll would get to it within a minute
	}
}

// monitor starts watching n's notify list after connecting: MONITOR where
// the network has it, else an ISON now and then every isonEvery.
func (a *App) monitor(n *state.Network) {
	list := a.notifyNicks(n)
	switch {
	case len(list) == 0:
	case n.Monitor:
		a.sendNet(n, "MONITOR", "+", strings.Join(list, ","))
	default:
		a.sendNet(n, "ISON", strings.Join(list, " "))
	}
}

// monitorReply handles RPL_MONONLINE (730) and RPL_MONOFFLINE (731).
func (a *App) monitorReply(n *state.Network, m ircmsg.Message) {
	if len(m.Params) < 2 {
		return
	}
	for target := range strings.SplitSeq(m.Params[len(m.Params)-1], ",") {
		if nick, _, _ := strings.Cut(target, "!"); nick != "" {
			a.notifySeen(n, nick, m.Command == "730", target)
		}
	}
}

// notifySeen notes that a notify-list nick is online or not. When that
// changes, like mIRC's notify, it's said in the network's status window
// (or the active one, by Options > Messages) with the entry's note and
// the address and time if known, and beeps; a "+" entry gets a WHOIS.
func (a *App) notifySeen(n *state.Network, nick string, online bool, addr string) {
	if n.Online == nil {
		n.Online = map[string]bool{}
	}
	key := n.Fold(nick)
	if n.Online[key] == online {
		return
	}
	n.Online[key] = online
	var e notifyEntry
	for _, s := range a.netList(listNotify, n) {
		if x := parseNotify(s); n.Same(x.nick, nick) {
			e = x
		}
	}
	text := "* " + nick
	if _, host, ok := strings.Cut(addr, "!"); ok {
		text += " (" + host + ")"
	}
	if online {
		text += " is on " + n.Name
	} else {
		text += " has left " + n.Name
	}
	if e.note != "" {
		text += ": " + e.note
	}
	text += " at " + time.Now().Format("15:04")
	where := n.Status()
	if a.notifyActive {
		where = a.whoisWin(n)
	}
	_, address, _ := strings.Cut(addr, "!")
	ev := script.Event{Kind: map[bool]string{true: "NOTIFY", false: "UNOTIFY"}[online], Network: n.Name,
		Nick: nick, Address: address}
	cmd, halted := a.scriptBefore(ev) // a ^ handler can say it its own way
	a.queueScript(cmd)
	if !halted {
		a.addLine(where, state.RoleHighlight, text)
		a.beepFor(where, state.Line{Role: state.RoleHighlight})
	}
	ev.Halted = halted
	a.queueScript(a.scriptAfter(ev))
	if online && e.whois {
		a.sendNet(n, "WHOIS", nick)
	}
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s + " "
	}
	return s + strings.Repeat(" ", w-len(s))
}

// openNickInfo is the nick popup's Info: mIRC opened the Address Book
// entry; here that's whether you watch for them or ignore them.
func (a *App) openNickInfo(n *state.Network, nick string) {
	notify := slices.ContainsFunc(a.notifyNicks(n), func(s string) bool { return n.Same(s, nick) })
	ignored := slices.ContainsFunc(a.netList(listIgnore, n), func(s string) bool {
		return strings.EqualFold(parseIgnore(s).mask, ignoreMask(nick))
	})
	yesNo := map[bool]string{true: "yes", false: "no"}
	d := message(nick+" on "+n.Name, "",
		"Notify when online:  "+yesNo[notify],
		"Ignored:             "+yesNo[ignored])
	d.Title = "Info - " + nick
	d.Buttons = []string{"Notify", "Ignore", "Close"}
	if notify {
		d.Buttons[0] = "Stop Notify"
	}
	if ignored {
		d.Buttons[1] = "Unignore"
	}
	d.Sel, d.Cancel = 2, 2
	d.OnClose = func(a *App, button int) tea.Cmd {
		switch button {
		case 0:
			a.setNotify(n, nick, !notify)
		case 1:
			a.setIgnore(n.Status(), nick, !ignored)
		default:
			return nil
		}
		a.openNickInfo(n, nick)
		return nil
	}
	a.openDialog(d)
}
