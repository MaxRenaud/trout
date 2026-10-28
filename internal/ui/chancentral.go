package ui

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

// Channel Central, like mIRC's (the channel popup's Channel Modes): the
// topic and its history, the modes, a key and a limit, and the ban,
// exception and invite lists. For people who aren't operators it's
// read-only, as mIRC greys it out.

// listNames are the list modes Channel Central shows, by letter.
var listNames = map[byte]string{'b': "Bans", 'e': "Exceptions", 'I': "Invites", 'q': "Quiets"}

// isOp reports whether you can change w's modes: op, or half-op and up.
func isOp(w *state.Window) bool {
	return strings.ContainsAny(w.PrefixOf(w.Net.Nick), "~&@%")
}

// channelLists are the list modes w's network has (CHANMODES' first
// group) that Channel Central knows how to show.
func channelLists(n *state.Network) []byte {
	group, _, _ := strings.Cut(n.ChanModes, ",")
	if group == "" {
		group = "b"
	}
	var out []byte
	for i := range len(group) {
		if _, ok := listNames[group[i]]; ok {
			out = append(out, group[i])
		}
	}
	return out
}

// chanModeFlags are the flag modes Channel Central shows as checkboxes.
var chanModeFlags = []struct {
	mode  byte
	label string
}{
	{'t', "Only ops set the topic (+t)"},
	{'n', "No messages from outside (+n)"},
	{'i', "Invite only (+i)"},
	{'m', "Moderated: only voiced people talk (+m)"},
	{'p', "Private (+p)"},
	{'s', "Secret (+s)"},
}

// openChannelModes is Channel Central.
func (a *App) openChannelModes(w *state.Window) {
	op := isOp(w)
	fields := []*formField{newField("Topic:", stripCodes(w.Topic), false)}
	for _, f := range chanModeFlags {
		fields = append(fields, newCheck(f.label, strings.IndexByte(w.Modes, f.mode) >= 0))
	}
	fields = append(fields, newField("Key:", "", false), newField("Limit:", "", false))
	lines := []string{"An empty key or limit leaves it as it is; \"-\" removes it."}
	if !op {
		lines = []string{"You're not an operator here, so this is read-only."}
	}
	f := &formBox{Title: "Channel Central - " + w.Name, Compact: true, Lines: lines, Fields: fields,
		Buttons: []string{"OK", "Bans...", "Topics...", "Cancel"},
		OnButton: map[string]func(*App) tea.Cmd{
			"Bans...":   func(a *App) tea.Cmd { return a.openChannelLists(w, channelLists(w.Net)[0]) },
			"Topics...": func(a *App) tea.Cmd { a.openTopics(w); return nil },
		}}
	f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
		if !op {
			return nil, nil
		}
		return nil, a.applyChannelCentral(w, v)
	}
	a.openDialog(f)
}

// applyChannelCentral sends what changed: the topic, then one MODE with
// its arguments in the order of their letters.
func (a *App) applyChannelCentral(w *state.Window, v []string) error {
	if topic := strings.TrimSpace(v[0]); topic != stripCodes(w.Topic) {
		a.sendTo(w, "TOPIC", w.Name, topic)
	}
	var plus, minus string
	var plusArgs, minusArgs []string
	for i, f := range chanModeFlags {
		on, was := v[1+i] == "1", strings.IndexByte(w.Modes, f.mode) >= 0
		switch {
		case on && !was:
			plus += string(f.mode)
		case !on && was:
			minus += string(f.mode)
		}
	}
	key, limit := strings.TrimSpace(v[len(v)-2]), strings.TrimSpace(v[len(v)-1])
	switch key {
	case "":
	case "-":
		minus += "k"
		minusArgs = append(minusArgs, "*")
	default:
		plus += "k"
		plusArgs = append(plusArgs, key)
	}
	switch limit {
	case "":
	case "-":
		minus += "l"
	default:
		if _, err := strconv.Atoi(limit); err != nil {
			return errors.New("the limit is a number of people")
		}
		plus += "l"
		plusArgs = append(plusArgs, limit)
	}
	change := ""
	if plus != "" {
		change += "+" + plus
	}
	if minus != "" {
		change += "-" + minus
	}
	if change != "" {
		a.sendTo(w, "MODE", slices.Concat([]string{w.Name, change}, plusArgs, minusArgs)...)
	}
	return nil
}

// listFetchKey names a list being fetched.
func listFetchKey(n *state.Network, ch string, mode byte) string {
	return n.ID + " " + n.Fold(ch) + " " + string(mode)
}

// openChannelLists shows one of w's lists (bans first), fetching it from
// the server; Next List goes on to the exceptions and invites.
func (a *App) openChannelLists(w *state.Window, mode byte) tea.Cmd {
	modes := channelLists(w.Net)
	l := &listBox{Header: "Mask                                By and when"}
	a.chanListBox, a.chanListWin, a.chanListMode = l, w, mode
	if isOp(w) {
		l.Buttons = []string{"Add...", "Remove", "Next List", "Close"}
	} else {
		l.Buttons = []string{"Next List", "Close"}
	}
	l.OnPress = func(a *App, l *listBox, button, mask string) tea.Cmd {
		switch button {
		case "Next List":
			i := slices.Index(modes, mode)
			return a.openChannelLists(w, modes[(i+1)%len(modes)])
		case "Remove":
			if mask != "" {
				a.sendTo(w, "MODE", w.Name, "-"+string(mode), mask)
			}
		case "Add...":
			what := strings.TrimSuffix(strings.ToLower(listNames[mode]), "s")
			a.openDialog(a.prompt("Add "+what, "A mask, like nick!*@* or *!*@host.", "Mask:", "",
				func(a *App, v string) {
					a.sendTo(w, "MODE", w.Name, "+"+string(mode), ignoreMask(v))
					a.openChannelLists(w, mode)
				}))
		}
		return nil
	}
	a.fillChannelList()
	if w.Lists[mode] == nil {
		a.listFetch[listFetchKey(w.Net, w.Name, mode)] = []state.ListEntry{}
		a.sendTo(w, "MODE", w.Name, "+"+string(mode))
	}
	a.openDialog(l)
	return nil
}

// fillChannelList shows the open list dialog's entries.
func (a *App) fillChannelList() {
	l, w, mode := a.chanListBox, a.chanListWin, a.chanListMode
	if l == nil || w == nil {
		return
	}
	l.Title = listNames[mode] + " - " + w.Name
	entries, fetched := w.Lists[mode]
	l.Rows, l.Keys = nil, nil
	for _, e := range entries {
		by := e.By
		if !e.At.IsZero() {
			by += ", " + e.At.Format("Jan 2 2006")
		}
		l.Rows = append(l.Rows, padRight(e.Mask, 36)+by)
		l.Keys = append(l.Keys, e.Mask)
	}
	switch {
	case !fetched:
		l.Rows = []string{"Getting the list..."}
	case len(entries) == 0:
		l.Rows = []string{"None."}
	}
}

// listEntry is RPL_BANLIST and friends: <me> <chan> [q] <mask> [by] [when].
func (a *App) listEntry(n *state.Network, m ircmsg.Message) {
	p := m.Params
	mode := map[string]byte{"367": 'b', "346": 'I', "348": 'e', "728": 'q'}[m.Command]
	if m.Command == "728" && len(p) > 2 { // RPL_QUIETLIST has the mode letter
		p = append(p[:2:2], p[3:]...)
	}
	if len(p) < 3 {
		return
	}
	key := listFetchKey(n, p[1], mode)
	entries, ok := a.listFetch[key]
	if !ok {
		a.addLine(n.Status(), state.RoleNormal, strings.Join(p[1:], " ")) // a list you typed /mode for
		return
	}
	e := state.ListEntry{Mask: p[2]}
	if len(p) > 3 {
		e.By, _, _ = strings.Cut(p[3], "!")
	}
	if len(p) > 4 {
		if ts, err := strconv.ParseInt(p[4], 10, 64); err == nil {
			e.At = time.Unix(ts, 0)
		}
	}
	a.listFetch[key] = append(entries, e)
}

// listEnd stores a fetched list on its channel window.
func (a *App) listEnd(n *state.Network, m ircmsg.Message) {
	if len(m.Params) < 2 {
		return
	}
	mode := map[string]byte{"368": 'b', "347": 'I', "349": 'e', "729": 'q'}[m.Command]
	key := listFetchKey(n, m.Params[1], mode)
	entries, ok := a.listFetch[key]
	delete(a.listFetch, key)
	if !ok {
		return
	}
	if w := n.Find(state.KChannel, m.Params[1]); w != nil {
		if w.Lists == nil {
			w.Lists = map[byte][]state.ListEntry{}
		}
		w.Lists[mode] = entries
	}
	a.fillChannelList()
}

// listModes keeps fetched lists up to date as bans and the like are set
// and removed.
func (a *App) listModes(w *state.Window, by, modes string, args []string, t time.Time) {
	if w.Lists == nil {
		return
	}
	groups := strings.Split(w.Net.ChanModes+",,,", ",")
	on, ai := true, 0
	for i := range len(modes) {
		c := modes[i]
		switch {
		case c == '+' || c == '-':
			on = c == '+'
			continue
		case strings.IndexByte(groups[0], c) >= 0: // a list mode: always an argument
		case strings.IndexByte(w.Net.PrefixModes, c) >= 0 || strings.IndexByte(groups[1], c) >= 0 ||
			(on && strings.IndexByte(groups[2], c) >= 0):
			ai++
			continue
		default:
			continue
		}
		if ai >= len(args) {
			return
		}
		mask := args[ai]
		ai++
		list, fetched := w.Lists[c]
		if !fetched {
			continue
		}
		list = slices.DeleteFunc(list, func(e state.ListEntry) bool { return strings.EqualFold(e.Mask, mask) })
		if on {
			list = append(list, state.ListEntry{Mask: mask, By: by, At: t})
		}
		w.Lists[c] = list
	}
	if a.chanListWin == w {
		a.fillChannelList()
	}
}

// openTopics is Channel Central's topic history: what the topic was while
// you've been in the channel, newest first.
func (a *App) openTopics(w *state.Window) {
	var lines []string
	for i := len(w.Topics) - 1; i >= 0 && len(lines) < 14; i-- {
		tc := w.Topics[i]
		lines = append(lines, tc.At.Format("Jan 2 15:04")+"  "+tc.By+": "+truncate(stripCodes(tc.Text), 60))
	}
	if len(lines) == 0 {
		lines = []string{"No topic seen yet."}
	}
	d := message(lines...)
	d.Title = "Topics - " + w.Name
	d.OnClose = func(a *App, _ int) tea.Cmd { a.openChannelModes(w); return nil }
	a.openDialog(d)
}
