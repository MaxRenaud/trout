package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// Catching up and read markers. soju keeps history and, with
// draft/read-marker, how far you've read each conversation in any client.
// trout reads a window when it's the active one and the terminal has
// focus; its own line marker stays mIRC's, where you were when you left.

// readEvery limits how often reading along in a busy window tells soju.
const readEvery = 2 * time.Second

type readTickMsg struct{}

// ircTime formats t for CHATHISTORY and MARKREAD.
func ircTime(t time.Time) string {
	return "timestamp=" + t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// catchUp asks soju, once connected to n, for what trout missed: each open
// window's messages since its newest one (after a dropped connection), and
// private conversations from the last week, to open the unread ones.
func (a *App) catchUp(n *state.Network, c *irc.Conn) {
	if !c.Caps["draft/chathistory"] {
		return
	}
	for _, w := range n.Wins {
		if w.Kind != state.KStatus && !w.Latest.IsZero() {
			c.Send("CHATHISTORY", "AFTER", w.Name, ircTime(w.Latest), "500")
		}
	}
	if c.Caps["draft/read-marker"] {
		now := time.Now()
		c.Send("CHATHISTORY", "TARGETS", ircTime(now.Add(-7*24*time.Hour)), ircTime(now.Add(time.Minute)), "50")
	}
}

// checkTargets handles the TARGETS list: for each private conversation
// without a window, it asks soju for the read marker, and markRead opens
// the window if there's anything newer.
func (a *App) checkTargets(n *state.Network, msgs []ircmsg.Message) {
	for _, m := range msgs {
		if m.Command != "CHATHISTORY" || len(m.Params) < 3 || m.Params[0] != "TARGETS" {
			continue
		}
		target := m.Params[1]
		latest, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(m.Params[2], "timestamp="))
		if err != nil || n.IsChannel(target) || strings.EqualFold(target, "BouncerServ") ||
			strings.EqualFold(target, n.Nick) || n.Find(state.KQuery, target) != nil {
			continue
		}
		if a.unread[n] == nil {
			a.unread[n] = map[string]time.Time{}
		}
		a.unread[n][strings.ToLower(target)] = latest
		a.sendNet(n, "MARKREAD", target)
	}
}

// markRead handles MARKREAD, soju's read marker for target ("*" for never
// read). soju sends it on JOIN, when asked, and whenever a client moves it.
func (a *App) markRead(n *state.Network, target, v string) {
	var at time.Time
	if ts, ok := strings.CutPrefix(v, "timestamp="); ok {
		at, _ = time.Parse(time.RFC3339Nano, ts)
	}
	key := strings.ToLower(target)
	if latest, ok := a.unread[n][key]; ok {
		delete(a.unread[n], key)
		if at.Before(latest) { // something came in after it was read
			// Its history decides: a private message opens a window,
			// notices go to the status window (see batch).
			if a.probing[n] == nil {
				a.probing[n] = map[string]time.Time{}
			}
			a.probing[n][key] = at
			a.sendNet(n, "CHATHISTORY", "LATEST", target, "*", "50")
		}
		return
	}
	kind := state.KQuery
	if n.IsChannel(target) {
		kind = state.KChannel
	}
	if w := n.Find(kind, target); w != nil && at.After(w.ReadAt) {
		w.ReadAt = at
		a.applyRead(w)
	}
}

// applyRead lines a window up with soju's read marker: what's been read,
// here or elsewhere, goes behind the line marker and stops counting as
// activity. The active window is left alone; it's being read.
func (a *App) applyRead(w *state.Window) {
	c := a.conns[w.Net]
	if w == a.active || c == nil || !c.Caps["draft/read-marker"] {
		return
	}
	read := w.LinesUpTo(w.ReadAt)
	w.Marker = max(w.Marker, read)
	w.Act = state.ActNone
	for _, l := range w.Lines[read:] {
		w.Act = max(w.Act, state.ActivityFor(l.Role))
	}
}

// sendRead tells soju that w has been read up to its newest message.
func (a *App) sendRead(w *state.Window) {
	if a.opts.Demo || !a.focused || w.Kind == state.KStatus || !w.Latest.After(w.ReadAt) {
		return
	}
	c := a.conns[w.Net]
	if c == nil || !c.Caps["draft/read-marker"] {
		return
	}
	w.ReadAt, a.readSent = w.Latest, time.Now()
	c.Send("MARKREAD", w.Name, ircTime(w.Latest))
}

// syncRead reads the active window along as messages arrive, telling soju
// at most every readEvery.
func (a *App) syncRead() tea.Cmd {
	w := a.active
	if a.opts.Demo || !a.focused || a.readTick || w == nil || !w.Latest.After(w.ReadAt) {
		return nil
	}
	if wait := readEvery - time.Since(a.readSent); wait > 0 {
		a.readTick = true
		return tea.Tick(wait, func(time.Time) tea.Msg { return readTickMsg{} })
	}
	a.sendRead(w)
	return nil
}
