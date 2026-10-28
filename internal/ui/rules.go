package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

// The Address Book's rules, like mIRC's: highlights, nick colors,
// auto-op / auto-voice / protect, typed and timed ignores, and notify
// entries with notes.

// privateModes are where private messages go (Options > Messages), like
// mIRC's query options: their own windows, one Messages window, or the
// active window.
var privateModes = []string{"windows", "single", "active"}

// messagesWindow is the single message window's name: parentheses can't
// be in a nick, so nothing mistakes it for a query.
const messagesWindow = "(Messages)"

// book is where the Address Book lives: config.toml, or memory (tests).
func (a *App) book() *config.Config {
	if a.cfg != nil {
		return a.cfg
	}
	return &a.memBook
}

// onNetwork reports whether an entry for network (empty: all) applies on n.
func onNetwork(network string, n *state.Network) bool {
	return network == "" || strings.EqualFold(network, n.Name)
}

// wildMatch matches s against a mask with * and ?, ignoring case. [ and ]
// are plain characters, as they're common in nicks.
func wildMatch(mask, s string) bool {
	return state.Wild(strings.ToLower(mask), strings.ToLower(s))
}

// containsWord reports whether text has word (with wildcards) as a word.
func containsWord(text, word string) bool {
	if !strings.ContainsAny(word, "*?") {
		return mentions(text, word)
	}
	for f := range strings.FieldsSeq(strings.ToLower(stripCodes(text))) {
		if wildMatch(word, strings.Trim(f, ".,:;!?()\"'")) {
			return true
		}
	}
	return false
}

// highlight decides whether a line from nick in window w highlights: your
// nick, or a Highlight rule. It returns the rule's color (-1 for the
// highlight color) and whether to beep.
func (a *App) highlight(w *state.Window, nick, text string) (hit bool, color int, beep bool) {
	n := w.Net
	if mentions(text, n.Nick) {
		return true, -1, true
	}
	for _, r := range a.book().Highlights {
		if !onNetwork(r.Network, n) || !inList(r.In, w.Name, nick) {
			continue
		}
		for word := range strings.SplitSeq(r.Words, ",") {
			word = strings.TrimSpace(word)
			if word == "" {
				continue
			}
			if (r.MatchText && containsWord(text, word)) || (r.MatchNick && wildMatch(word, nick)) {
				return true, r.Color, r.Beep
			}
		}
	}
	return false, -1, false
}

// inList reports whether a rule's channels/nicks list (comma separated,
// empty for anywhere) includes this window or sender.
func inList(list, where, nick string) bool {
	if strings.TrimSpace(list) == "" {
		return true
	}
	for x := range strings.SplitSeq(list, ",") {
		x = strings.TrimSpace(x)
		if x != "" && (wildMatch(x, where) || wildMatch(x, nick)) {
			return true
		}
	}
	return false
}

// nickRuleColor is the Nick Colors rule for nick in w, or -1.
func (a *App) nickRuleColor(w *state.Window, nick string) int {
	for _, r := range a.book().NickColors {
		if !onNetwork(r.Network, w.Net) {
			continue
		}
		maskNick, _, _ := strings.Cut(r.Mask, "!") // trout matches on nicks
		if !wildMatch(maskNick, nick) {
			continue
		}
		if r.Modes != "" && !strings.ContainsAny(w.PrefixOf(nick), r.Modes) {
			continue
		}
		return r.Color
	}
	return -1
}

// controlFor finds the Control entries for someone joining or changed in
// ch: which of auto-op, auto-voice and protect apply.
func (a *App) controlFor(n *state.Network, ch, mask string) (kinds string) {
	for _, e := range a.book().Control {
		if !onNetwork(e.Network, n) || !wildMatch(ignoreMask(e.Mask), mask) {
			continue
		}
		if len(e.Channels) > 0 && !slices.ContainsFunc(e.Channels, func(c string) bool { return n.Same(c, ch) }) {
			continue
		}
		kinds += e.Kinds
	}
	return kinds
}

// controlDelay is how long auto-op waits, like mIRC's "random delay", so
// several ops don't all set the same mode.
const controlDelay = 2 * time.Second

type controlMsg struct {
	net      *state.Network
	ch, nick string
	mode     string
}

// controlJoin handles someone joining a channel where you're an operator:
// auto-op and auto-voice them after a moment.
func (a *App) controlJoin(n *state.Network, w *state.Window, nick, mask string) tea.Cmd {
	kinds := a.controlFor(n, w.Name, mask)
	var modes string
	if strings.Contains(kinds, "o") {
		modes = "+o"
	} else if strings.Contains(kinds, "v") {
		modes = "+v"
	}
	if modes == "" {
		return nil
	}
	return tea.Tick(controlDelay, func(time.Time) tea.Msg { return controlMsg{n, w.Name, nick, modes} })
}

// controlDue sets an auto-op or auto-voice if it's still needed and you
// can.
func (a *App) controlDue(msg controlMsg) {
	w := msg.net.Find(state.KChannel, msg.ch)
	if w == nil || !w.Joined || !isOp(w) || w.NickIndex(msg.nick) < 0 {
		return
	}
	want := map[string]string{"+o": "@", "+v": "+"}[msg.mode]
	if strings.Contains(w.PrefixOf(msg.nick), want) || (want == "+" && w.PrefixOf(msg.nick) != "") {
		return
	}
	a.sendNet(msg.net, "MODE", w.Name, msg.mode, msg.nick)
}

// protect handles a mode change by someone else: like mIRC's protect
// list, a protected person deopped or banned gets it undone, and the one
// who did it is deopped.
func (a *App) protect(n *state.Network, w *state.Window, by, modes string, args []string) {
	if !isOp(w) || n.Same(by, n.Nick) || strings.Contains(a.controlFor(n, w.Name, by+"!*@*"), "p") {
		return
	}
	on, ai := true, 0
	groups := strings.Split(n.ChanModes+",,,", ",")
	for i := range len(modes) {
		c := modes[i]
		switch {
		case c == '+' || c == '-':
			on = c == '+'
			continue
		case strings.IndexByte(n.PrefixModes, c) < 0 && strings.IndexByte(groups[0], c) < 0 &&
			strings.IndexByte(groups[1], c) < 0 && !(on && strings.IndexByte(groups[2], c) >= 0):
			continue
		}
		if ai >= len(args) {
			return
		}
		arg := args[ai]
		ai++
		switch {
		case c == 'o' && !on && strings.Contains(a.controlFor(n, w.Name, arg+"!*@*"), "p"):
			a.sendNet(n, "MODE", w.Name, "-o+o", by, arg)
		case c == 'b' && on && a.protectedBan(n, w, arg):
			a.sendNet(n, "MODE", w.Name, "-o-b", by, arg)
		}
	}
}

// protectedBan reports whether a ban mask covers someone protected there.
func (a *App) protectedBan(n *state.Network, w *state.Window, ban string) bool {
	for _, e := range a.book().Control {
		if strings.Contains(e.Kinds, "p") && onNetwork(e.Network, n) && wildMatch(ban, ignoreMask(e.Mask)) {
			return true
		}
	}
	return false
}

// ignoreEntry is one ignore list entry, "mask [flags] [until=unix]", like
// mIRC's /ignore -pcntikd: private, channel, notice, ctcp, invite, codes
// (show their lines without colors), dcc. No flags is all but codes.
type ignoreEntry struct {
	mask, flags string
	until       time.Time
}

func parseIgnore(s string) ignoreEntry {
	f := strings.Fields(s)
	if len(f) == 0 { // a blank entry in a hand-edited config.toml
		return ignoreEntry{}
	}
	e := ignoreEntry{mask: ignoreMask(f[0]), flags: "pcntid"}
	for _, x := range f[1:] {
		if ts, ok := strings.CutPrefix(x, "until="); ok {
			if v, err := strconv.ParseInt(ts, 10, 64); err == nil {
				e.until = time.Unix(v, 0)
			}
		} else {
			e.flags = x
		}
	}
	return e
}

func (e ignoreEntry) String() string {
	s := e.mask
	if e.flags != "pcntid" {
		s += " " + e.flags
	}
	if !e.until.IsZero() {
		s += " until=" + strconv.FormatInt(e.until.Unix(), 10)
	}
	return s
}

// ignoreKind is which of mIRC's ignore types a message is.
func ignoreKind(n *state.Network, m ircmsg.Message) byte {
	switch {
	case m.Command == "INVITE":
		return 'i'
	case len(m.Params) > 1 && strings.HasPrefix(m.Params[1], "\x01DCC"):
		return 'd'
	case len(m.Params) > 1 && strings.HasPrefix(m.Params[1], "\x01") && !strings.HasPrefix(m.Params[1], "\x01ACTION"):
		return 't'
	case m.Command == "NOTICE":
		return 'n'
	case len(m.Params) > 0 && n.IsChannel(m.Params[0]):
		return 'c'
	}
	return 'p'
}

// ignoreMatch finds the ignore entry for m's sender, if one is in force.
func (a *App) ignoreMatch(n *state.Network, m ircmsg.Message) (ignoreEntry, bool) {
	if m.Source == "" {
		return ignoreEntry{}, false
	}
	src := strings.ToLower(m.Source)
	if !strings.Contains(src, "!") {
		src += "!*@*"
	}
	for _, s := range a.netList(listIgnore, n) {
		e := parseIgnore(s)
		if e.mask == "" || !e.until.IsZero() && time.Now().After(e.until) {
			continue // blank, or a timed ignore that's run out
		}
		if wildMatch(e.mask, src) {
			return e, true
		}
	}
	return ignoreEntry{}, false
}

// notifyEntry is a notify list entry, "[+]nick [note]": + is mIRC's
// "perform /whois" when they come on.
type notifyEntry struct {
	nick, note string
	whois      bool
}

func parseNotify(s string) notifyEntry {
	nick, note, _ := strings.Cut(strings.TrimSpace(s), " ")
	e := notifyEntry{nick: strings.TrimPrefix(nick, "+"), note: strings.TrimSpace(note), whois: strings.HasPrefix(nick, "+")}
	return e
}

func (e notifyEntry) String() string {
	s := e.nick
	if e.whois {
		s = "+" + s
	}
	if e.note != "" {
		s += " " + e.note
	}
	return s
}

// notifyNicks are the nicks on n's notify list.
func (a *App) notifyNicks(n *state.Network) []string {
	var out []string
	for _, s := range a.netList(listNotify, n) {
		out = append(out, parseNotify(s).nick)
	}
	return out
}

// privateElsewhere shows a private message in one Messages window or the
// active one (Options > Messages), the way mIRC does: "*nick* text" in
// the active window, and "-> *nick* text" for your own.
func (a *App) privateElsewhere(n *state.Network, from, target, text string, mine bool, t time.Time) {
	var w *state.Window
	if a.privateTo == 1 {
		if w = n.Find(state.KQuery, messagesWindow); w == nil {
			w = n.Add(&state.Window{Kind: state.KQuery, Name: messagesWindow})
		}
	} else {
		w = a.whoisWin(n)
	}
	l := state.Line{Time: t, Role: state.RoleNormal}
	action, isAction := strings.CutPrefix(text, "\x01ACTION ")
	action = strings.TrimSuffix(action, "\x01")
	switch {
	case mine && isAction:
		l.Role, l.Text = state.RoleAction, "-> *"+target+"* "+n.Nick+" "+action
	case mine:
		l.Role, l.Text = state.RoleOwn, "-> *"+target+"* "+text
	case isAction:
		l.Role, l.Text = state.RoleAction, "* "+from+" "+action
	case a.privateTo == 1:
		l.Text = "<" + from + "> " + text
	default:
		l.Text = "*" + from + "* " + text
	}
	if !mine {
		if hit, color, beep := a.highlight(w, from, text); hit {
			l.Role, l.NoBeep, l.Color = state.RoleHighlight, !beep, color+1
		}
		a.lastFrom, a.lastFromNet = from, n
		if a.beep && (w != a.active || !a.focused) {
			a.bellDue = true // a private message, like a query's
		}
	}
	a.addLineL(w, l)
}

// performLine turns a Perform command written like mIRC's into the IRC
// line the bouncer sends: /msg, /notice, /join, /mode, /raw and the rest.
func performLine(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	rest, slash := strings.CutPrefix(cmd, "/")
	if !slash {
		return cmd
	}
	name, args, _ := strings.Cut(rest, " ")
	target, text, _ := strings.Cut(strings.TrimSpace(args), " ")
	switch strings.ToLower(name) {
	case "msg", "privmsg":
		return "PRIVMSG " + target + " :" + text
	case "notice":
		return "NOTICE " + target + " :" + text
	case "raw", "quote":
		return strings.TrimSpace(args)
	}
	return strings.TrimSpace(strings.ToUpper(name) + " " + args)
}

// openPerform is Options > Connect > Perform for network n: commands the
// bouncer runs when it connects there.
func (a *App) openPerform(n *state.Network) {
	b := a.book()
	if b.Perform == nil {
		b.Perform = map[string][]string{}
	}
	l := &listBox{Title: "Perform - " + n.Name, Header: "Commands, like /msg NickServ identify secret",
		Buttons: []string{"Add...", "Remove", "Close"}}
	fill := func() {
		l.Rows, l.Keys = nil, nil
		for i, c := range b.Perform[n.Name] {
			l.Rows = append(l.Rows, c)
			l.Keys = append(l.Keys, strconv.Itoa(i))
		}
		if len(l.Rows) == 0 {
			l.Rows = []string{"None. The bouncer runs these whenever it connects to " + n.Name + "."}
		}
	}
	fill()
	l.OnPress = func(a *App, l *listBox, button, key string) tea.Cmd {
		switch button {
		case "Add...":
			a.openDialog(a.prompt("Perform", "A command for when the bouncer connects to "+n.Name+".", "Command:", "/",
				func(a *App, v string) {
					b.Perform[n.Name] = append(b.Perform[n.Name], v)
					a.pushPerform(n)
					fill()
					a.openDialog(l)
				}))
		case "Remove":
			if i, err := strconv.Atoi(key); err == nil && i < len(b.Perform[n.Name]) {
				b.Perform[n.Name] = slices.Delete(b.Perform[n.Name], i, i+1)
				a.pushPerform(n)
				fill()
			}
		}
		return nil
	}
	a.openDialog(l)
}

// pushPerform hands network n's Perform to the bouncer as soju's connect
// commands (all of them each time; one empty one clears the list).
func (a *App) pushPerform(n *state.Network) {
	cmds := a.book().Perform[n.Name]
	args := []string{"network", "update", n.Name}
	for _, c := range cmds {
		args = append(args, "-connect-command", performLine(c))
	}
	if len(cmds) == 0 {
		args = append(args, "-connect-command", "")
	}
	a.serv(a.home, n.Status(), false, args...)
	a.saveConfig()
}
