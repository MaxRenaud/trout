package ui

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/demo"
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

// histBatch collects a chathistory batch until it ends.
type histBatch struct {
	target string
	msgs   []ircmsg.Message
}

var (
	controlCaps = []string{"soju.im/bouncer-networks", "soju.im/bouncer-networks-notify",
		"server-time", "message-tags", "batch", "echo-message"}
	networkCaps = []string{"soju.im/bouncer-networks", "server-time", "message-tags", "batch",
		"echo-message", "multi-prefix", "draft/chathistory"}
)

const reconnectDelay = 5 * time.Second

func (a *App) dial(n *state.Network, bind string, caps []string) tea.Cmd {
	cfg := irc.Config{Addr: a.opts.Addr, Nick: a.opts.User, User: a.opts.User, Password: a.opts.Password,
		Caps: caps, Bind: bind, Debug: a.opts.Debug}
	return func() tea.Msg {
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
	a.addLine(a.home.Status(), state.RoleInfo, "* Connecting to soju at "+a.opts.Addr+" as "+a.opts.User+"...")
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
		if !slices.Contains(a.nets, n) { // removed while connecting
			c.Close()
			return nil
		}
		if n == a.home {
			a.control = c
			a.addLine(n.Status(), state.RoleInfo, "* Connected to soju")
			c.Send("BOUNCER", "LISTNETWORKS")
		} else {
			a.conns[n] = c
			a.addLine(n.Status(), state.RoleInfo, "* Connected to "+n.Name+" through soju")
		}
		go func() {
			err := c.Run(func(m ircmsg.Message) { a.send(ircLineMsg{n, m}) })
			a.send(ircDownMsg{n, err})
		}()
	case ircLineMsg:
		return a.handleIRC(msg.net, msg.m)
	case ircDownMsg:
		n := msg.net
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
		a.addLine(n.Status(), state.RoleQuit, "* Disconnected ("+msg.err.Error()+"); retrying in 5 seconds")
		return tea.Tick(reconnectDelay, func(time.Time) tea.Msg { return reconnectMsg{n} })
	case reconnectMsg:
		n := msg.net
		switch {
		case !slices.Contains(a.nets, n) || a.connFor(n) != nil:
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
	isMe := strings.EqualFold(nick, n.Nick)
	status := n.Status()
	line := func(w *state.Window, role state.Role, text string) { a.addLineAt(w, role, text, t) }
	mask := func() string {
		if nuh, err := m.NUH(); err == nil && nuh.User != "" {
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
		if w, l, ok := a.message(n, m, t); ok {
			a.addLineAt(w, l.Role, l.Text, t)
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
		line(status, state.RoleNormal, strings.Join(p[1:], " "))
	case "JOIN":
		ch := arg(0)
		w := n.Find(state.KChannel, ch)
		if isMe {
			if w == nil {
				w = n.AddChannel(&state.Window{Kind: state.KChannel, Name: ch})
			}
			w.Joined, w.Nicks = true, nil
			line(w, state.RoleEvent, "* Now talking in "+ch)
			a.sendNet(n, "MODE", ch)
			a.fetchHistory(w)
			if a.joining[strings.ToLower(ch)] {
				delete(a.joining, strings.ToLower(ch))
				a.activate(w)
			}
		} else if w != nil {
			w.AddNick(state.Nick{Name: nick})
			line(w, state.RoleEvent, "* Joins: "+mask())
		}
	case "PART":
		w := n.Find(state.KChannel, arg(0))
		switch {
		case w == nil:
		case isMe:
			delete(a.history, w)
			a.removeWindow(w)
		case w.RemoveNick(nick):
			line(w, state.RoleEvent, "* Parts: "+mask()+reason(1))
		}
	case "KICK":
		w := n.Find(state.KChannel, arg(0))
		switch {
		case w == nil:
		case strings.EqualFold(arg(1), n.Nick):
			w.Joined, w.Nicks = false, nil
			line(w, state.RoleEvent, "* You were kicked from "+w.Name+" by "+nick+reason(2))
		default:
			w.RemoveNick(arg(1))
			line(w, state.RoleEvent, "* "+arg(1)+" was kicked by "+nick+reason(2))
		}
	case "QUIT":
		for _, w := range n.Wins {
			if (w.Kind == state.KChannel && w.RemoveNick(nick)) || (w.Kind == state.KQuery && strings.EqualFold(w.Name, nick)) {
				line(w, state.RoleQuit, "* Quits: "+mask()+reason(0))
			}
		}
	case "NICK":
		to := arg(0)
		if isMe {
			n.Nick = to
		}
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
				line(w, state.RoleEvent, "* "+nick+" is now known as "+to)
			case w.Kind == state.KQuery && strings.EqualFold(w.Name, nick):
				w.Name = to
				line(w, state.RoleEvent, "* "+nick+" is now known as "+to)
			}
		}
	case "MODE":
		who := cmp.Or(nick, m.Source)
		change := strings.Join(p[1:], " ")
		if !n.IsChannel(arg(0)) {
			n.Umodes = applyFlags(n.Umodes, arg(1))
			line(status, state.RoleEvent, "* "+who+" sets mode: "+change)
		} else if w := n.Find(state.KChannel, arg(0)); w != nil {
			applyChanModes(w, arg(1), p[min(2, len(p)):])
			line(w, state.RoleEvent, "* "+who+" sets mode: "+change)
		}
	case "TOPIC":
		if w := n.Find(state.KChannel, arg(0)); w != nil {
			w.Topic = arg(1)
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
		}
	case "329", "321", "CAP", "AWAY", "ACCOUNT", "PONG":
	case "322": // RPL_LIST
		users, _ := strconv.Atoi(arg(2))
		a.listRows[n] = append(a.listRows[n], demo.ChanInfo{Name: arg(1), Users: users, Topic: arg(3)})
	case "323": // RPL_LISTEND
		rows := a.listRows[n]
		slices.SortFunc(rows, func(x, y demo.ChanInfo) int { return cmp.Compare(y.Users, x.Users) })
		if l := a.listing[n]; l != nil {
			l.setChannels(rows)
			if len(rows) == 0 {
				l.Rows = []string{"No channels"}
			}
		}
		delete(a.listing, n)
		delete(a.listRows, n)
	case "ERROR":
		line(status, state.RoleQuit, "* "+arg(0))
	case "FAIL", "WARN", "NOTE":
		line(status, state.RoleInfo, "* "+m.Command+" "+strings.Join(p, " "))
	default:
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
	case !fromMe && !notice && mentions(text, n.Nick):
		l.Role = state.RoleHighlight
	}
	return w, l, true
}

func mentions(text, nick string) bool {
	return nick != "" && strings.Contains(strings.ToLower(text), strings.ToLower(nick))
}

// batch starts or finishes a chathistory batch. History goes in front of
// what the window already shows, skipping lines it already has.
func (a *App) batch(n *state.Network, m ircmsg.Message) {
	if len(m.Params) == 0 {
		return
	}
	if id, ok := strings.CutPrefix(m.Params[0], "+"); ok {
		if len(m.Params) >= 3 && m.Params[1] == "chathistory" {
			a.batches[n.ID+" "+id] = &histBatch{target: m.Params[2]}
		}
		return
	}
	key := n.ID + " " + strings.TrimPrefix(m.Params[0], "-")
	b := a.batches[key]
	delete(a.batches, key)
	if b == nil {
		return
	}
	kind := state.KQuery
	if n.IsChannel(b.target) {
		kind = state.KChannel
	}
	w := n.Find(kind, b.target)
	if w == nil {
		return
	}
	type seen struct {
		t    int64
		text string
	}
	have := map[seen]bool{}
	for _, l := range w.Lines {
		have[seen{l.Time.UnixMilli(), l.Text}] = true
	}
	var lines []state.Line
	for _, hm := range b.msgs {
		t := time.Now()
		if ok, v := hm.GetTag("time"); ok {
			if pt, err := time.Parse(time.RFC3339Nano, v); err == nil {
				t = pt.Local()
			}
		}
		if hm.Command != "PRIVMSG" && hm.Command != "NOTICE" {
			continue
		}
		if hw, l, ok := a.message(n, hm, t); ok && hw == w && !have[seen{t.UnixMilli(), l.Text}] {
			lines = append(lines, l)
		}
	}
	w.Lines = slices.Concat(lines, w.Lines)
	if w.Marker > 0 {
		w.Marker += len(lines)
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
		a.addLine(home, state.RoleInfo, "* soju: network "+n.Name+" was removed")
		return nil
	}
	if i >= 0 {
		n := a.nets[i]
		if name := b.Attrs["name"]; name != "" {
			n.Name = name
		}
		if st := b.Attrs["state"]; st != "" {
			a.addLine(n.Status(), state.RoleInfo, "* soju: "+n.Name+" is "+st)
		}
		return nil
	}
	n := state.NewNetwork(cmp.Or(b.Attrs["name"], b.Attrs["host"], "network "+b.ID))
	n.ID, n.Server = b.ID, b.Attrs["host"]
	if port, err := strconv.Atoi(b.Attrs["port"]); err == nil {
		n.Port = port
	}
	n.Nick = cmp.Or(b.Attrs["nickname"], a.opts.User)
	a.nets = append(a.nets, n)
	a.addLine(home, state.RoleInfo, "* soju: found network "+n.Name+" ("+n.Server+")")
	a.addLine(n.Status(), state.RoleInfo, "* Connecting to "+n.Name+" through soju...")
	return a.dial(n, n.ID, networkCaps)
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
	if a.sendNet(w.Net, "PRIVMSG", target, text) && !a.echoed(w.Net) {
		a.addLine(w, state.RoleOwn, "<"+w.Net.Nick+"> "+text)
	}
}

// action is /me.
func (a *App) action(w *state.Window, text string) {
	if a.opts.Demo {
		a.addLine(w, state.RoleAction, "* "+w.Net.Nick+" "+text)
		return
	}
	if a.sendNet(w.Net, "PRIVMSG", w.Name, "\x01ACTION "+text+"\x01") && !a.echoed(w.Net) {
		a.addLine(w, state.RoleAction, "* "+w.Net.Nick+" "+text)
	}
}

// ircCommand handles slash commands for real. Like mIRC, commands it
// doesn't know go to the server as they are, so /whois and friends work.
func (a *App) ircCommand(w *state.Window, line string) tea.Cmd {
	name, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
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
		if a.sendNet(n, "PRIVMSG", arg, tail) && !a.echoed(n) {
			a.addLine(w, state.RoleOwn, "-> *"+arg+"* "+tail)
		}
	case "notice":
		if tail == "" {
			need()
			break
		}
		if a.sendNet(n, "NOTICE", arg, tail) {
			a.addLine(w, state.RoleNotice, "-> -"+arg+"- "+tail)
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
	case "topic":
		switch {
		case channel == "":
			need()
		case rest == "":
			a.sendNet(n, "TOPIC", channel)
		default:
			a.sendNet(n, "TOPIC", channel, rest)
		}
	case "mode":
		if channel != "" && (rest == "" || strings.ContainsRune("+-", rune(rest[0]))) {
			rest = strings.TrimSpace(channel + " " + rest)
		}
		a.rawNet(n, "MODE "+rest)
	case "kick":
		switch {
		case channel == "" || arg == "":
			need()
		case tail != "":
			a.sendNet(n, "KICK", channel, arg, tail)
		default:
			a.sendNet(n, "KICK", channel, arg)
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
	case "clear":
		w.Lines, w.Scroll, w.Marker = nil, 0, 0
	case "exit", "quit":
		return tea.Quit
	default:
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
