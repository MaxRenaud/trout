package ui

import (
	"errors"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/state"
)

// Searching the bouncer's history (soju.im/search), from Ctrl+F: results
// in a list, and Go To loads the history around one into its window and
// highlights it, as if Find had found it there.

// searchLimit is how many results the bouncer is asked for.
const searchLimit = 200

// goTo is a search result being loaded into its window.
type goTo struct {
	win    *state.Window
	at     time.Time
	needle string
}

// canSearch reports whether n's bouncer connection can search history.
func (a *App) canSearch(n *state.Network) bool {
	c := a.conns[n]
	return c != nil && c.Caps["soju.im/search"]
}

// searchHistory asks the bouncer for messages with text in w (or the
// whole network, from its status window).
func (a *App) searchHistory(w *state.Window, text string) error {
	if !a.canSearch(w.Net) {
		return errors.New("this bouncer can't search its history")
	}
	attrs := "text=" + ircmsg.EscapeTagValue(text) + ";limit=" + strconv.Itoa(searchLimit)
	if w.Kind == state.KChannel || w.Kind == state.KQuery {
		attrs = "in=" + ircmsg.EscapeTagValue(w.Name) + ";" + attrs
	}
	a.searching[w.Net] = &goTo{win: w, needle: strings.ToLower(text)}
	a.sendNet(w.Net, "SEARCH", attrs)
	return nil
}

// searchResults shows a finished soju.im/search batch, newest first.
func (a *App) searchResults(n *state.Network, msgs []ircmsg.Message) {
	s := a.searching[n]
	delete(a.searching, n)
	if s == nil {
		return
	}
	where := s.win.Name
	if s.win.Kind == state.KStatus {
		where = n.Name
	}
	l := &listBox{Title: "Search Results - " + where + ": \"" + s.needle + "\"",
		Header: "When          Where          Message", Buttons: []string{"Go To", "Close"}}
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if (m.Command != "PRIVMSG" && m.Command != "NOTICE") || len(m.Params) < 2 || a.ignored(n, m) {
			continue
		}
		t := msgTime(m)
		target := m.Params[0]
		if !n.IsChannel(target) && !n.Same(m.Nick(), n.Nick) {
			target = m.Nick() // a private message's window is the other side's
		}
		l.Rows = append(l.Rows, t.Format("Jan 02 15:04")+"  "+padRight(truncate(target, 14), 15)+stripCodes(searchLine(m)))
		l.Keys = append(l.Keys, target+"\t"+t.UTC().Format(time.RFC3339Nano))
	}
	if len(l.Rows) == 0 {
		l.Rows = []string{"Nothing found."}
	}
	l.OnPress = func(a *App, _ *listBox, _ string, key string) tea.Cmd {
		target, ts, ok := strings.Cut(key, "\t")
		at, err := time.Parse(time.RFC3339Nano, ts)
		if !ok || err != nil {
			return nil
		}
		a.dlg = nil
		a.goToHistory(n, target, at, s.needle)
		return nil
	}
	a.openDialog(l)
}

// searchLine is a result as a window would show it.
func searchLine(m ircmsg.Message) string {
	text := m.Params[1]
	if inner, ok := strings.CutPrefix(text, "\x01ACTION "); ok {
		return "* " + m.Nick() + " " + strings.TrimSuffix(inner, "\x01")
	}
	if m.Command == "NOTICE" {
		return "-" + m.Nick() + "- " + text
	}
	return "<" + m.Nick() + "> " + text
}

func msgTime(m ircmsg.Message) time.Time {
	if ok, v := m.GetTag("time"); ok {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.Local()
		}
	}
	return time.Now()
}

// goToHistory opens target's window (a channel you've left gets one, not
// joined) and asks for the history around at; batch then highlights it.
func (a *App) goToHistory(n *state.Network, target string, at time.Time, needle string) {
	var w *state.Window
	if n.IsChannel(target) {
		if w = n.Find(state.KChannel, target); w == nil {
			w = n.AddChannel(&state.Window{Kind: state.KChannel, Name: target})
		}
	} else {
		w = a.query(n, target, "")
	}
	a.activate(w)
	a.history[w] = true
	a.goingTo = &goTo{win: w, at: at, needle: needle}
	a.sendNet(n, "CHATHISTORY", "AROUND", target, ircTime(at), "100")
}

// arrived highlights a search result once its history is in its window.
func (a *App) arrived(w *state.Window) {
	g := a.goingTo
	if g == nil || g.win != w {
		return
	}
	a.goingTo = nil
	for i, l := range w.Lines {
		if l.Time.Sub(g.at).Abs() < time.Millisecond && strings.Contains(strings.ToLower(stripCodes(l.Text)), g.needle) {
			a.activate(w)
			a.find = &finder{win: w, text: g.needle, line: i}
			a.findText = g.needle
			a.scrollToLine(w, i)
			return
		}
	}
}
