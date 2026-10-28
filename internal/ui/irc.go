package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// Real mode: one control connection to soju lists its networks
// (soju.im/bouncer-networks); each network gets its own connection bound
// to it with BOUNCER BIND. Connection goroutines hand every line to Update
// as an ircLineMsg, so all state changes happen on the UI goroutine.

type (
	ircUpMsg struct {
		net  *state.Network
		conn *irc.Conn
	}
	ircLineMsg struct {
		net *state.Network
		m   ircmsg.Message
	}
	ircDownMsg struct {
		net *state.Network
		err error
	}
	reconnectMsg struct{ net *state.Network }
)

// histBatch collects a chathistory batch (or a TARGETS list, or search
// results) until it ends.
type histBatch struct {
	targets bool // draft/chathistory-targets
	search  bool // soju.im/search
	target  string
	msgs    []ircmsg.Message
}

var (
	controlCaps = []string{"soju.im/bouncer-networks", "soju.im/bouncer-networks-notify",
		"server-time", "message-tags", "batch", "echo-message", "soju.im/client-cert"}
	networkCaps = []string{"soju.im/bouncer-networks", "server-time", "message-tags", "batch",
		"echo-message", "multi-prefix", "draft/chathistory", "draft/read-marker", "away-notify", "chghost", "soju.im/search"}
)

const reconnectDelay = 5 * time.Second

func (a *App) dial(n *state.Network, bind string, caps []string) tea.Cmd {
	addr, useTLS, err := irc.ParseAddress(a.bnc.Address)
	cfg := irc.Config{Addr: addr, TLS: useTLS, Pin: a.bnc.Pin, Nick: a.bnc.User, User: a.bnc.User,
		Password: a.password, Caps: caps, Bind: bind, Debug: a.opts.Debug, Dial: a.dialer(false)}
	if useTLS { // this computer's certificate, for logging in without the password
		cfg.ClientCert = a.loadClientCert()
		cfg.External = a.bnc.Login == "certificate" && !a.certFailed
	}
	return func() tea.Msg {
		if err != nil {
			return ircDownMsg{n, err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		c, err := irc.Dial(ctx, cfg)
		if err != nil {
			return ircDownMsg{n, err}
		}
		return ircUpMsg{n, c}
	}
}

func (a *App) connectBouncer() tea.Cmd {
	a.stopped = false
	a.addLine(a.home.Status(), state.RoleInfo, "* Connecting to the bouncer at "+a.bnc.Address+" as "+a.bnc.User+"...")
	return a.dial(a.home, "", controlCaps)
}

func (a *App) connFor(n *state.Network) *irc.Conn {
	if n == a.home {
		return a.control
	}
	return a.conns[n]
}

func (a *App) updateIRC(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case ircUpMsg:
		n, c := msg.net, msg.conn
		var cmd tea.Cmd
		if !slices.Contains(a.nets, n) { // removed while connecting
			c.Close()
			return nil
		}
		a.upAt[n] = time.Now() // for the online timer
		if n == a.home {
			a.control = c
			a.addLine(n.Status(), state.RoleInfo, "* Connected to the bouncer")
			c.Send("BOUNCER", "LISTNETWORKS")
			a.refreshDisabled()
			a.pinClientCert(c)
			if len(a.moving) > 0 { // a bouncer trout just set up
				a.moveNetworks()
			}
		} else {
			a.conns[n] = c
			a.addLine(n.Status(), state.RoleInfo, "* Connected to "+n.Name+" through the bouncer")
			a.catchUp(n, c) // the notify list starts at the end of the MOTD, after ISUPPORT
			if !a.isonOn {  // the notify list's poll, for networks without MONITOR
				a.isonOn = true
				cmd = tea.Tick(isonEvery, func(time.Time) tea.Msg { return isonTickMsg{} })
			}
		}
		if a.away != "" {
			a.sendAway(c)
		}
		go func() {
			err := c.Run(func(m ircmsg.Message) { a.send(ircLineMsg{n, m}) })
			a.send(ircDownMsg{n, err})
		}()
		return cmd
	case ircLineMsg:
		if !slices.Contains(a.nets, msg.net) { // from before switching bouncers
			return nil
		}
		return a.handleIRC(msg.net, msg.m)
	case ircDownMsg:
		n := msg.net
		delete(a.upAt, n)
		if n == a.home {
			a.control = nil
		} else {
			delete(a.conns, n)
		}
		if !slices.Contains(a.nets, n) {
			return nil
		}
		for _, w := range n.Wins {
			if w.Kind == state.KChannel {
				w.Joined, w.Nicks = false, nil
			}
		}
		if n == a.home && a.stopOn(msg.err) {
			return nil
		}
		a.addLine(n.Status(), state.RoleQuit, "* Disconnected ("+msg.err.Error()+"); retrying in 5 seconds")
		return tea.Tick(reconnectDelay, func(time.Time) tea.Msg { return reconnectMsg{n} })
	case reconnectMsg:
		n := msg.net
		switch {
		case !slices.Contains(a.nets, n) || a.connFor(n) != nil || (n == a.home && a.stopped):
		case n == a.home:
			return a.connectBouncer()
		default:
			return a.dial(n, n.ID, networkCaps)
		}
	}
	return nil
}

// handleIRC applies one message from network n to the UI state.
func (a *App) handleIRC(n *state.Network, m ircmsg.Message) tea.Cmd {
	t := time.Now()
	if ok, v := m.GetTag("time"); ok {
		if pt, err := time.Parse(time.RFC3339Nano, v); err == nil {
			t = pt.Local()
		}
	}
	if ok, id := m.GetTag("batch"); ok {
		if b := a.batches[n.ID+" "+id]; b != nil {
			b.msgs = append(b.msgs, m)
			return nil
		}
	}
	p := m.Params
	arg := func(i int) string {
		if i < len(p) {
			return p[i]
		}
		return ""
	}
	nick := m.Nick()
	isMe := n.Same(nick, n.Nick)
	status := n.Status()
	line := func(w *state.Window, role state.Role, text string) { a.addLineAt(w, role, text, t) }
	// event is a channel event that the window's Events option can send
	// to the status window or hide.
	event := func(w *state.Window, role state.Role, text string) {
		a.eventLine(w, state.Line{Time: t, Role: role, Text: text})
	}
	mask := func() string {
		if nuh, err := m.NUH(); err == nil && nuh.User != "" && a.showAddress { // Options > Messages
			return nick + " (" + nuh.User + "@" + nuh.Host + ")"
		}
		return nick
	}
	reason := func(i int) string {
		if r := arg(i); r != "" {
			return " (" + r + ")"
		}
		return ""
	}

	switch m.Command {
	case "BATCH":
		a.batch(n, m)
	case "BOUNCER":
		if b, ok := irc.ParseBouncerNetwork(m); ok {
			return a.bouncerNetwork(b)
		}
	case "PRIVMSG", "NOTICE":
		if a.servReply(n, m) || a.ignored(n, m) || a.ctcp(n, m, t) {
			return nil
		}
		peer := nick
		if isMe {
			peer = arg(0)
		}
		// Options > Messages, for people you don't already have a query
		// window with.
		if m.Command == "PRIVMSG" && !n.IsChannel(arg(0)) && a.privateTo != 0 && n.Find(state.KQuery, peer) == nil {
			a.privateElsewhere(n, nick, arg(0), arg(1), isMe, t)
			return nil
		}
		newQuery := m.Command == "PRIVMSG" && !isMe && !n.IsChannel(arg(0)) && n.Find(state.KQuery, nick) == nil
		if w, l, ok := a.message(n, m, t); ok {
			if newQuery && a.whoisOnQuery && w.Kind == state.KQuery { // Options > IRC, like mIRC
				a.sendNet(n, "WHOIS", nick)
			}
			if m.Command == "NOTICE" && !n.IsChannel(arg(0)) && strings.Contains(m.Source, "!") {
				// Like mIRC, a private notice shows in the active window
				// (or, by Options > IRC, the status window), never a window
				// of its own; server notices go to status.
				w = status
				if a.noticesActive {
					w = a.whoisWin(n)
				}
				if isMe { // yours, from this or another client
					l.Text = "-> -" + arg(0) + "- " + arg(1)
				}
			}
			if e, ok := a.ignoreMatch(n, m); ok && strings.Contains(e.flags, "k") {
				l.Text = stripCodes(l.Text) // an ignore of their codes only
			}
			l.Time = t
			a.addLineL(w, l)
			a.noteURLs(w, nick, arg(1), t)
			if w.Kind != state.KStatus && t.After(w.Latest) {
				w.Latest = t
			}
			a.beepFor(w, l)
			if w.Kind == state.KQuery && l.Role != state.RoleOwn {
				a.lastQuery = w
			}
		}
	case "001":
		n.Nick = arg(0)
		line(status, state.RoleNormal, arg(1))
	case "005":
		is := irc.ISupport(m)
		if modes, prefixes, ok := irc.ParsePrefix(is["PREFIX"]); ok {
			n.PrefixModes, n.Prefixes = modes, prefixes
		}
		if v, ok := is["CHANMODES"]; ok {
			n.ChanModes = v
		}
		if v, ok := is["soju.im/FILEHOST"]; ok {
			n.FileHost = v
		}
		if v, ok := is["CASEMAPPING"]; ok {
			n.CaseMapping = v
		}
		if _, ok := is["MONITOR"]; ok {
			n.Monitor = true
		}
		line(status, state.RoleNormal, strings.Join(p[1:], " "))
	case "JOIN":
		ch := arg(0)
		w := n.Find(state.KChannel, ch)
		if isMe {
			if w == nil {
				w = n.AddChannel(&state.Window{Kind: state.KChannel, Name: ch})
			}
			w.Joined, w.Nicks = true, nil
			if a.nicksOnJoin { // Options > Messages: list them once NAMES is in
				a.namesOnJoin[w] = true
			}
			line(w, state.RoleEvent, "* Now talking in "+ch)
			a.sendNet(n, "MODE", ch)
			if a.sendNet(n, "WHO", ch) { // for who's away
				a.quietWho[whoKey(n, ch)] = true
			}
			a.fetchHistory(w)
			if a.joining[strings.ToLower(ch)] {
				delete(a.joining, strings.ToLower(ch))
				a.activate(w)
			}
		} else if w != nil {
			w.AddNick(state.Nick{Name: nick})
			event(w, state.RoleEvent, "* Joins: "+mask())
			if isOp(w) { // Address Book > Control: auto-op and auto-voice
				return a.controlJoin(n, w, nick, m.Source)
			}
		}
	case "PART":
		w := n.Find(state.KChannel, arg(0))
		switch {
		case w == nil:
		case isMe && (a.hopping[n.Fold(w.Name)] || a.keepOpenOnPart):
			// /hop, or Options > IRC: the window stays, like mIRC's
			// "Keep channels open".
			delete(a.hopping, n.Fold(w.Name))
			w.Joined, w.Nicks = false, nil
			line(w, state.RoleEvent, "* You left "+w.Name)
		case isMe:
			delete(a.history, w)
			a.removeWindow(w)
		case w.RemoveNick(nick):
			event(w, state.RoleEvent, "* Parts: "+mask()+reason(1))
		}
	case "KICK":
		w := n.Find(state.KChannel, arg(0))
		switch {
		case w == nil:
		case n.Same(arg(1), n.Nick):
			w.Joined, w.Nicks = false, nil
			line(w, state.RoleEvent, "* You were kicked from "+w.Name+" by "+nick+reason(2))
			if a.rejoinOnKick { // Options > IRC, like mIRC
				line(w, state.RoleEvent, "* Rejoining "+w.Name)
				a.sendNet(n, "JOIN", w.Name)
			}
		default:
			w.RemoveNick(arg(1))
			line(w, state.RoleEvent, "* "+arg(1)+" was kicked by "+nick+reason(2))
		}
	case "QUIT":
		toStatus := false // once, however many channels send it there
		for _, w := range n.Wins {
			switch {
			case w.Kind == state.KChannel && w.RemoveNick(nick):
				if a.winOpts(w).Events == "status" {
					toStatus = true
				} else {
					event(w, state.RoleQuit, "* Quits: "+mask()+reason(0))
				}
			case w.Kind == state.KQuery && strings.EqualFold(w.Name, nick):
				line(w, state.RoleQuit, "* Quits: "+mask()+reason(0))
			}
		}
		if toStatus {
			line(status, state.RoleQuit, "* Quits: "+mask()+reason(0))
		}
	case "NICK":
		to := arg(0)
		if isMe {
			n.Nick = to
		}
		toStatus := false // once, however many channels send it there
		for _, w := range n.Wins {
			switch {
			case isMe:
				if i := w.NickIndex(nick); i >= 0 {
					w.Nicks[i].Name = to
					w.SortNicks()
				}
				line(w, state.RoleEvent, "* Your nick is now "+to)
			case w.Kind == state.KChannel && w.NickIndex(nick) >= 0:
				w.Nicks[w.NickIndex(nick)].Name = to
				w.SortNicks()
				if a.winOpts(w).Events == "status" {
					toStatus = true
				} else {
					event(w, state.RoleEvent, "* "+nick+" is now known as "+to)
				}
			case w.Kind == state.KQuery && strings.EqualFold(w.Name, nick):
				w.Name = to
				line(w, state.RoleEvent, "* "+nick+" is now known as "+to)
			}
		}
		if toStatus {
			line(status, state.RoleEvent, "* "+nick+" is now known as "+to)
		}
	case "MODE":
		who := cmp.Or(nick, m.Source)
		change := strings.Join(p[1:], " ")
		if !n.IsChannel(arg(0)) {
			n.Umodes = applyFlags(n.Umodes, arg(1))
			line(status, state.RoleEvent, "* "+who+" sets mode: "+change)
		} else if w := n.Find(state.KChannel, arg(0)); w != nil {
			applyChanModes(w, arg(1), p[min(2, len(p)):])
			a.listModes(w, nick, arg(1), p[min(2, len(p)):], t)
			a.protect(n, w, nick, arg(1), p[min(2, len(p)):]) // Address Book > Control
			event(w, state.RoleEvent, "* "+who+" sets mode: "+change)
		}
	case "TOPIC":
		if w := n.Find(state.KChannel, arg(0)); w != nil {
			w.Topic = arg(1)
			w.Topics = append(w.Topics, state.TopicChange{Text: arg(1), By: nick, At: t})
			line(w, state.RoleEvent, "* "+nick+" changes topic to '"+arg(1)+"'")
		}
	case "324": // RPL_CHANNELMODEIS
		if w := n.Find(state.KChannel, arg(1)); w != nil {
			w.Modes = ""
			applyChanModes(w, arg(2), p[min(3, len(p)):])
		}
	case "332": // RPL_TOPIC
		if w := n.Find(state.KChannel, arg(1)); w != nil {
			w.Topic = arg(2)
			line(w, state.RoleEvent, "* Topic is '"+arg(2)+"'")
		}
	case "333": // RPL_TOPICWHOTIME
		if w := n.Find(state.KChannel, arg(1)); w != nil {
			setter, _, _ := strings.Cut(arg(2), "!")
			when := arg(3)
			if ts, err := strconv.ParseInt(when, 10, 64); err == nil {
				when = time.Unix(ts, 0).Format("Mon Jan 2 15:04:05")
				if len(w.Topics) == 0 {
					w.Topics = []state.TopicChange{{Text: w.Topic, By: setter, At: time.Unix(ts, 0)}}
				}
			}
			line(w, state.RoleEvent, "* Set by "+setter+" on "+when)
		}
	case "353": // RPL_NAMREPLY
		if w := n.Find(state.KChannel, arg(2)); w != nil {
			for name := range strings.FieldsSeq(arg(3)) {
				w.AddNames(n.SplitPrefix(name))
			}
		}
	case "366": // RPL_ENDOFNAMES
		if w := n.Find(state.KChannel, arg(1)); w != nil {
			w.EndNames()
			if a.namesOnJoin[w] {
				delete(a.namesOnJoin, w)
				var names []string
				for _, nk := range w.Nicks {
					names = append(names, nk.Prefix+nk.Name)
				}
				line(w, state.RoleInfo, "* Nicks "+w.Name+": "+strings.Join(names, " "))
			}
		}
	case "AWAY": // away-notify
		a.awayNotify(n, nick, arg(0), t)
	case "305", "306": // RPL_UNAWAY, RPL_NOWAWAY: setAway already said so
		a.awayUpstream(n)
	case "INVITE":
		if !a.ignored(n, m) {
			a.invitedTo[n] = arg(1) // for /join -i
		}
		switch {
		case a.ignored(n, m):
		case a.joinOnInvite: // Options > IRC, like mIRC
			line(a.whoisWin(n), state.RoleInfo, "* "+nick+" invites you to "+arg(1)+"; joining")
			a.join(n, arg(1))
		default:
			line(a.whoisWin(n), state.RoleInfo, "* "+nick+" invites you to "+arg(1)+" (/join "+arg(1)+")")
		}
	case "730", "731": // RPL_MONONLINE, RPL_MONOFFLINE: the notify list
		a.monitorReply(n, m)
	case "376", "422": // the end of the MOTD (or none): registration is done
		line(status, state.RoleNormal, strings.Join(p[1:], " "))
		if n != a.home {
			a.monitor(n)     // now ISUPPORT has said whether there's MONITOR
			if a.invisible { // Options > Messages
				a.sendNet(n, "MODE", n.Nick, "+i")
			}
			// Like mIRC, the Favorites pop up when you connect; once a
			// session, for the first network that has some.
			if a.popFavorites && !a.popped && a.dlg == nil && len(a.favorites(n)) > 0 {
				a.popped = true
				a.openFavoritesFor(n)
			}
		}
	case "303": // RPL_ISON: the notify list, where there's no MONITOR
		a.isonReply(n, arg(1))
	case "WALLOPS": // like mIRC: !nick! text, in the status window
		line(status, state.RoleNotice, "!"+cmp.Or(nick, m.Source)+"! "+arg(0))
	case "CHGHOST": // a new user@host, for an open query's title
		if q := n.Find(state.KQuery, nick); q != nil && len(p) >= 2 {
			q.Host = arg(0) + "@" + arg(1)
		}
	case "732", "733": // the MONITOR list, when asked
	case "CLIENTCERT": // soju.im/client-cert
		if n == a.home && strings.EqualFold(arg(0), "CREATE") {
			a.clientCertPinned()
		}
	case "367", "346", "348", "728": // ban, invite, exception, quiet list entries
		a.listEntry(n, m)
	case "368", "347", "349", "729": // ...and their ends
		a.listEnd(n, m)
	case "MARKREAD":
		a.markRead(n, arg(0), arg(1))
	case "329", "321", "CAP", "ACCOUNT", "PONG":
	case "322", "323": // RPL_LIST, RPL_LISTEND
		a.listReply(n, m.Command, p)
	case "263": // RPL_TRYAGAIN: rate-limited, like LIST on Rizon
		if strings.EqualFold(arg(1), "LIST") {
			a.listFailed(n, tryAgainText(arg(2)))
		}
		line(a.whoisWin(n), state.RoleInfo, "* "+strings.Join(p[1:], " "))
	case "ERROR":
		line(status, state.RoleQuit, "* "+arg(0))
	case "FAIL", "WARN", "NOTE":
		if arg(0) == "CLIENTCERT" { // the bouncer can't take certificates; say so once
			if !a.certNoted {
				a.certNoted = true
				line(status, state.RoleInfo, "* The bouncer can't take a certificate login ("+lastParam(p)+"); trout keeps using the password")
			}
			break
		}
		line(status, state.RoleInfo, "* "+m.Command+" "+strings.Join(p, " "))
	default:
		if a.who(n, m) {
			return nil
		}
		if who, text, ok := whoisLine(m); ok {
			a.whoisReply(n, m, who, text, t)
			return nil
		}
		code, err := strconv.Atoi(m.Command)
		if err != nil || len(p) < 2 {
			return nil
		}
		w := status
		if code >= 400 && code < 600 && a.active.Net == n {
			w = a.active // errors show where you are, like mIRC
		}
		line(w, state.RoleNormal, strings.Join(p[1:], " "))
	}
	return nil
}

// whoisReply shows one WHOIS line where you asked: the active window if
// it's on the same network, else the status window. RPL_AWAY outside a
// WHOIS is someone you messaged being away.
func (a *App) whoisReply(n *state.Network, m ircmsg.Message, nick, text string, t time.Time) {
	switch m.Command {
	case "311", "314":
		a.whois[n] = nick
	case "301":
		if !strings.EqualFold(a.whois[n], nick) {
			a.awayNotify(n, nick, m.Params[len(m.Params)-1], t)
			if n.Find(state.KQuery, nick) == nil {
				a.addLineAt(a.whoisWin(n), state.RoleInfo, "* "+text, t)
			}
			return
		}
	case "318", "369":
		delete(a.whois, n)
	}
	a.addLineAt(a.whoisWin(n), state.RoleNormal, text, t)
}

func (a *App) whoisWin(n *state.Network) *state.Window {
	if a.active.Net == n {
		return a.active
	}
	return n.Status()
}

// message turns a PRIVMSG or NOTICE into a line and the window it belongs
// in, opening a query window for a new private message. ok is false for
// CTCPs other than ACTION, which trout doesn't support.
func (a *App) message(n *state.Network, m ircmsg.Message, t time.Time) (*state.Window, state.Line, bool) {
	if len(m.Params) < 2 {
		return nil, state.Line{}, false
	}
	target, text := m.Params[0], m.Params[1]
	action := strings.HasPrefix(text, "\x01ACTION ")
	if strings.HasPrefix(text, "\x01") && !action {
		return nil, state.Line{}, false
	}
	from := cmp.Or(m.Nick(), m.Source)
	fromMe := strings.EqualFold(from, n.Nick)
	notice := m.Command == "NOTICE"
	peer := from
	if fromMe {
		peer = target
	}

	var w *state.Window
	switch {
	case n.IsChannel(target):
		w = n.Find(state.KChannel, target)
	case notice:
		w = n.Find(state.KQuery, peer) // notices don't open windows
	default:
		host := ""
		if nuh, err := m.NUH(); err == nil && !fromMe && nuh.User != "" {
			host = nuh.User + "@" + nuh.Host
		}
		w = a.query(n, peer, host)
	}
	if w == nil {
		w = n.Status()
	}

	l := state.Line{Time: t, Role: state.RoleNormal}
	switch {
	case action:
		l.Role, l.Text = state.RoleAction, "* "+from+" "+strings.TrimSuffix(text[len("\x01ACTION "):], "\x01")
	case notice:
		l.Role, l.Text = state.RoleNotice, "-"+from+"- "+text
	default:
		l.Text = "<" + from + "> " + text
	}
	switch {
	case fromMe && l.Role == state.RoleNormal:
		l.Role = state.RoleOwn
	case !fromMe && !notice:
		if hit, color, beep := a.highlight(w, from, text); hit {
			l.Role, l.NoBeep, l.Color = state.RoleHighlight, !beep, color+1
		}
	}
	return w, l, true
}

// batch starts or finishes a chathistory batch, which is merged into its
// window in time order, or a TARGETS list.
func (a *App) batch(n *state.Network, m ircmsg.Message) {
	if len(m.Params) == 0 {
		return
	}
	if id, ok := strings.CutPrefix(m.Params[0], "+"); ok {
		switch {
		case len(m.Params) >= 3 && m.Params[1] == "chathistory":
			a.batches[n.ID+" "+id] = &histBatch{target: m.Params[2]}
		case len(m.Params) >= 2 && m.Params[1] == "draft/chathistory-targets":
			a.batches[n.ID+" "+id] = &histBatch{targets: true}
		case len(m.Params) >= 2 && (m.Params[1] == "soju.im/search" || m.Params[1] == "search"):
			a.batches[n.ID+" "+id] = &histBatch{search: true}
		}
		return
	}
	key := n.ID + " " + strings.TrimPrefix(m.Params[0], "-")
	b := a.batches[key]
	delete(a.batches, key)
	switch {
	case b == nil:
		return
	case b.targets:
		a.checkTargets(n, b.msgs)
		return
	case b.search:
		a.searchResults(n, b.msgs)
		return
	}
	kind := state.KQuery
	if n.IsChannel(b.target) {
		kind = state.KChannel
	}
	w := n.Find(kind, b.target)
	var readAt time.Time
	switch probe, ok := a.probing[n][strings.ToLower(b.target)]; {
	case w != nil:
		readAt = w.ReadAt
	case ok: // missed private messages, from checkTargets
		delete(a.probing[n], strings.ToLower(b.target))
		readAt = probe
	default:
		return
	}
	type histMsg struct {
		m ircmsg.Message
		t time.Time
	}
	var msgs []histMsg
	chat := false // an unread private message, not a notice or CTCP
	for _, hm := range b.msgs {
		if (hm.Command != "PRIVMSG" && hm.Command != "NOTICE") || a.ignored(n, hm) {
			continue
		}
		t := time.Now()
		if ok, v := hm.GetTag("time"); ok {
			if pt, err := time.Parse(time.RFC3339Nano, v); err == nil {
				t = pt.Local()
			}
		}
		msgs = append(msgs, histMsg{hm, t})
		_, _, ctcp := ctcpSplit(lastParam(hm.Params))
		if hm.Command == "PRIVMSG" && !ctcp && t.After(readAt) && !strings.EqualFold(hm.Nick(), n.Nick) {
			chat = true
		}
	}
	if w == nil {
		if chat { // like a live private message, it opens a window
			w = a.query(n, b.target, "")
			w.ReadAt, a.history[w] = readAt, true
		}
	}
	// Private notices never get a window of their own, like mIRC: the ones
	// you hadn't read go to the status window, where they'd have shown.
	var lines, notices []state.Line
	for _, h := range msgs {
		if kind == state.KQuery && h.m.Command == "NOTICE" {
			if h.t.After(readAt) && strings.Contains(h.m.Source, "!") {
				if _, l, ok := a.message(n, h.m, h.t); ok {
					if strings.EqualFold(h.m.Nick(), n.Nick) {
						l.Text = "-> -" + b.target + "- " + lastParam(h.m.Params)
					}
					notices = append(notices, l)
				}
			}
			continue
		}
		if w == nil {
			continue
		}
		if hw, l, ok := a.message(n, h.m, h.t); ok && hw == w {
			lines = append(lines, l)
			if h.t.After(w.Latest) {
				w.Latest = h.t
			}
		}
	}
	if status := n.Status(); len(notices) > 0 && status.Merge(notices) > 0 && status != a.active {
		status.Act = max(status.Act, state.ActMsg)
	}
	if w != nil {
		w.Merge(lines)
		a.applyRead(w)
		a.arrived(w)
	}
}

// bouncerNetwork handles one entry of soju's network list or a change
// notification.
func (a *App) bouncerNetwork(b irc.BouncerNetwork) tea.Cmd {
	i := slices.IndexFunc(a.nets, func(n *state.Network) bool { return n != a.home && n.ID == b.ID })
	home := a.home.Status()
	if b.Deleted {
		if i < 0 {
			return nil
		}
		n := a.nets[i]
		if c := a.conns[n]; c != nil {
			c.Close()
		}
		a.nets = slices.Delete(a.nets, i, i+1)
		if a.active.Net == n {
			a.activate(home)
		}
		a.addLine(home, state.RoleInfo, "* Bouncer: network "+n.Name+" was removed")
		return nil
	}
	if i >= 0 {
		a.networkAttrs(a.nets[i], b.Attrs)
		return nil
	}
	n := state.NewNetwork(cmp.Or(b.Attrs["name"], b.Attrs["host"], "network "+b.ID))
	n.ID = b.ID
	n.Nick = cmp.Or(b.Attrs["nickname"], a.bnc.User)
	a.nets = append(a.nets, n)
	a.addLine(home, state.RoleInfo, "* Bouncer: found network "+n.Name+" ("+b.Attrs["host"]+")")
	a.networkAttrs(n, b.Attrs)
	if strings.EqualFold(a.focusNet, n.Name) {
		a.focusNet = ""
		a.activate(n.Status())
	}
	return a.dial(n, n.ID, networkCaps)
}

// networkAttrs merges soju's attributes for n, saying in its status window
// when soju connects or disconnects. soju sends only what changed, and an
// attribute without a value (like "error") was cleared.
func (a *App) networkAttrs(n *state.Network, attrs map[string]string) {
	if n.Attrs == nil {
		n.Attrs = map[string]string{}
	}
	was := n.Attrs["state"]
	for k, v := range attrs {
		if v == "" {
			delete(n.Attrs, k)
		} else {
			n.Attrs[k] = v
		}
	}
	n.Name = cmp.Or(n.Attrs["name"], n.Name)
	n.Server = cmp.Or(n.Attrs["host"], n.Server)
	if port, err := strconv.Atoi(n.Attrs["port"]); err == nil {
		n.Port = port
	}
	st := n.Attrs["state"]
	if st == was {
		return
	}
	switch {
	case st == "connecting" || st == "connected":
		n.Disabled = false
		if st == "connected" && a.away != "" {
			a.awayUpstream(n) // a new connection to the network isn't away yet
		}
	case st == "disconnected" && was != "":
		a.refreshDisabled() // switched off, or just dropped?
	}
	status := n.Status()
	switch st {
	case "connecting":
		a.addLine(status, state.RoleInfo, fmt.Sprintf("* Bouncer: connecting to %s (%s:%d)...", n.Name, n.Server, n.Port))
	case "connected":
		a.addLine(status, state.RoleInfo, "* Bouncer: connected to "+n.Name)
	case "disconnected":
		// soju doesn't part the channels on our side; they come back with
		// JOINs when it reconnects.
		for _, w := range n.Wins {
			if w.Kind == state.KChannel && w.Joined {
				w.Joined, w.Nicks = false, nil
				a.addLine(w, state.RoleQuit, "* Disconnected")
			}
		}
		why := ""
		if e := n.Attrs["error"]; e != "" {
			why = " (" + e + ")"
		}
		a.addLine(status, state.RoleQuit, "* Bouncer: disconnected from "+n.Name+why)
	}
}

// fetchHistory asks soju for a window's recent messages, once.
func (a *App) fetchHistory(w *state.Window) {
	c := a.conns[w.Net]
	if c == nil || !c.Caps["draft/chathistory"] || a.history[w] {
		return
	}
	a.history[w] = true
	c.Send("CHATHISTORY", "LATEST", w.Name, "*", "50")
}

// sendNet sends a command on network n's connection, saying so in the
// status window when there isn't one.
func (a *App) sendNet(n *state.Network, command string, params ...string) bool {
	c := a.connFor(n)
	if c == nil {
		a.addLine(n.Status(), state.RoleInfo, "* Not connected to "+n.Name)
		return false
	}
	if err := c.Send(command, params...); err != nil {
		a.addLine(n.Status(), state.RoleInfo, "* Couldn't send: "+err.Error())
		return false
	}
	// Who this trout messaged, for DCC offers you asked for. Not from
	// echoes: the bouncer relays what your other clients send too, and
	// their requests shouldn't make this trout download anything.
	if command == "PRIVMSG" && len(params) > 0 && !n.IsChannel(params[0]) {
		a.messaged[n.ID+" "+strings.ToLower(params[0])] = time.Now()
	}
	return true
}

func (a *App) sendTo(w *state.Window, command string, params ...string) {
	a.sendNet(w.Net, command, params...)
}

func (a *App) rawNet(n *state.Network, line string) {
	if c := a.connFor(n); c != nil {
		c.Raw(line)
	} else {
		a.addLine(n.Status(), state.RoleInfo, "* Not connected to "+n.Name)
	}
}

// echoed reports whether the server echoes our own messages back
// (echo-message), so they shouldn't also be shown locally.
func (a *App) echoed(n *state.Network) bool {
	c := a.connFor(n)
	return c != nil && c.Caps["echo-message"]
}

func (a *App) privmsg(w *state.Window, target, text string) {
	for _, part := range a.sendText(w.Net, "PRIVMSG", target, text, plain) {
		if !a.echoed(w.Net) {
			a.addLine(w, state.RoleOwn, "<"+w.Net.Nick+"> "+part)
		}
	}
}

// action is /me.
func (a *App) action(w *state.Window, text string) {
	if w.Kind == state.KChat {
		a.chatSay(w, text, true)
		return
	}
	if a.opts.Demo {
		a.addLine(w, state.RoleAction, "* "+w.Net.Nick+" "+text)
		return
	}
	for _, part := range a.sendText(w.Net, "PRIVMSG", w.Name, text, asAction) {
		if !a.echoed(w.Net) {
			a.addLine(w, state.RoleAction, "* "+w.Net.Nick+" "+part)
		}
	}
}

// ircCommand handles slash commands for real. Like mIRC, commands it
// doesn't know go to the server as they are, so /whois and friends work.
func (a *App) ircCommand(w *state.Window, line string) tea.Cmd {
	name, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	switches, rest := stripSwitches(name, rest)
	if strings.EqualFold(name, "join") && strings.Contains(switches, "i") { // /join -i: the last invite
		rest = strings.TrimSpace(a.lastInvited(w.Net) + " " + rest)
	}
	arg, tail, _ := strings.Cut(rest, " ")
	n := w.Net
	channel := ""
	if w.Kind == state.KChannel {
		channel = w.Name
	}
	need := func() { a.addLine(w, state.RoleInfo, "* /"+name+": insufficient parameters") }
	opMode := func(m string) {
		if channel == "" || arg == "" {
			need()
			return
		}
		a.sendNet(n, "MODE", channel, m, arg)
	}
	switch strings.ToLower(name) {
	case "join", "j":
		if arg == "" {
			need()
			break
		}
		if !n.IsChannel(arg) {
			arg = "#" + arg
		}
		a.joining[strings.ToLower(arg)] = true
		if tail != "" {
			a.sendNet(n, "JOIN", arg, tail)
		} else {
			a.sendNet(n, "JOIN", arg)
		}
	case "part", "leave":
		ch, why := channel, rest
		if n.IsChannel(arg) {
			ch, why = arg, tail
		}
		switch {
		case ch == "":
			a.closeWindow(w)
		case why != "":
			a.sendNet(n, "PART", ch, why)
		default:
			a.sendNet(n, "PART", ch)
		}
	case "close":
		a.closeWindow(w)
	case "query":
		if arg == "" {
			need()
			break
		}
		if q := a.openQuery(n, arg); tail != "" {
			a.privmsg(q, arg, tail)
		}
	case "msg":
		if tail == "" {
			need()
			break
		}
		for _, part := range a.sendText(n, "PRIVMSG", arg, tail, plain) {
			if !a.echoed(n) {
				a.addLine(w, state.RoleOwn, "-> *"+arg+"* "+part)
			}
		}
	case "notice":
		if tail == "" {
			need()
			break
		}
		for _, part := range a.sendText(n, "NOTICE", arg, tail, plain) {
			if !a.echoed(n) {
				a.addLine(w, state.RoleNotice, "-> -"+arg+"- "+part)
			}
		}
	case "me":
		if w.Kind == state.KStatus || rest == "" {
			need()
			break
		}
		a.action(w, rest)
	case "nick":
		if arg == "" {
			need()
			break
		}
		a.sendNet(n, "NICK", arg)
	case "topic": // /topic [#chan] [text]
		ch, text := a.channelArg(w, rest)
		switch {
		case ch == "":
			need()
		case text == "":
			a.sendNet(n, "TOPIC", ch)
		default:
			a.sendNet(n, "TOPIC", ch, text)
		}
	case "mode":
		if channel != "" && (rest == "" || strings.ContainsRune("+-", rune(rest[0]))) {
			rest = strings.TrimSpace(channel + " " + rest)
		}
		a.rawNet(n, "MODE "+rest)
	case "kick": // /kick [#chan] nick [reason]
		ch, more := a.channelArg(w, rest)
		who, why, _ := strings.Cut(more, " ")
		switch {
		case ch == "" || who == "":
			need()
		case why != "":
			a.sendNet(n, "KICK", ch, who, why)
		default:
			a.sendNet(n, "KICK", ch, who)
		}
	case "op":
		opMode("+o")
	case "deop":
		opMode("-o")
	case "voice":
		opMode("+v")
	case "devoice":
		opMode("-v")
	case "ban":
		if !strings.ContainsAny(arg, "!@") {
			arg += "!*@*"
		}
		opMode("+b")
	case "quote", "raw":
		if rest == "" {
			need()
			break
		}
		a.rawNet(n, rest)
	case "server":
		return a.serverCommand(w, rest)
	case "list":
		if rest == "" { // like mIRC, the Channels List; with filters, the server's own
			return a.openChannelsList(true)
		}
		a.rawNet(n, "LIST "+rest)
	case "away":
		a.setAway(rest)
	case "notify":
		a.notifyCommand(w, rest)
	case "amsg", "ame":
		if rest == "" {
			need()
			break
		}
		a.allChannels(w, rest, strings.EqualFold(name, "ame"))
	case "invite": // like mIRC, the channel defaults to this one
		switch {
		case arg == "" || (tail == "" && channel == ""):
			need()
		case tail == "":
			a.sendNet(n, "INVITE", arg, channel)
		default:
			a.sendNet(n, "INVITE", arg, tail)
		}
	case "ctcp":
		if tail == "" {
			need()
			break
		}
		a.sendCTCP(w, arg, tail)
	case "ignore":
		a.ignoreCommand(w, true, rest)
	case "unignore":
		if arg == "" {
			need()
			break
		}
		a.ignoreCommand(w, false, rest)
	case "clear": // /clear [window]
		cw := w
		if arg != "" {
			cw = cmp.Or(a.windowFor(n, arg), w)
		}
		cw.Lines, cw.Scroll, cw.Marker = nil, 0, 0
	case "exit", "quit":
		return a.quit()
	default:
		if cmd, ok := a.mircCommand(w, name, rest); ok {
			return cmd
		}
		a.rawNet(n, strings.TrimSpace(strings.ToUpper(name)+" "+rest))
	}
	return nil
}

// applyChanModes applies a channel mode change: prefix modes update the
// nicklist, list modes (bans) are ignored, and the rest are kept as the
// letters shown in the title bar.
func applyChanModes(w *state.Window, modes string, args []string) {
	n := w.Net
	groups := strings.Split(cmp.Or(n.ChanModes, "beI,k,l,imnpst")+",,,", ",")
	takesArg := func(c byte, on bool) bool {
		return strings.IndexByte(groups[0], c) >= 0 || strings.IndexByte(groups[1], c) >= 0 ||
			(on && strings.IndexByte(groups[2], c) >= 0)
	}
	on := true
	for i := range len(modes) {
		c := modes[i]
		switch {
		case c == '+' || c == '-':
			on = c == '+'
		case strings.IndexByte(n.PrefixModes, c) >= 0:
			if len(args) > 0 {
				w.SetPrefix(args[0], c, on)
				args = args[1:]
			}
		default:
			if takesArg(c, on) && len(args) > 0 {
				args = args[1:]
			}
			if strings.IndexByte(groups[0], c) < 0 {
				w.Modes = applyFlags(w.Modes, string([]byte{"-+"[b2i(on)], c}))
			}
		}
	}
}

// applyFlags applies a "+ab-c" change to a set of mode letters.
func applyFlags(set, change string) string {
	on := true
	for _, c := range change {
		switch {
		case c == '+' || c == '-':
			on = c == '+'
		case on && !strings.ContainsRune(set, c):
			set += string(c)
		case !on:
			set = strings.ReplaceAll(set, string(c), "")
		}
	}
	return set
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// lastParam is a message's last parameter, usually its text.
func lastParam(p []string) string {
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}
