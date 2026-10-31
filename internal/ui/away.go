package ui

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ergochat/irc-go/ircmsg"

	"github.com/MaxRenaud/trout/internal/irc"
	"github.com/MaxRenaud/trout/internal/state"
)

// Away, WHO and WHOIS, and the bell.

// setAway is /away: away with a message, or back without one, on every
// network, like mIRC's "away on all servers". Each connection tells soju
// (which then tells other clients), and awayUpstream puts the message on
// the networks themselves.
func (a *App) setAway(msg string) {
	if a.opts.Demo {
		a.addLine(a.active, state.RoleInfo, "* /away isn't part of the demo")
		return
	}
	a.away = msg
	for _, n := range a.nets {
		a.sendAway(a.connFor(n)) // its RPL_(UN)AWAY calls awayUpstream
	}
	if msg == "" {
		a.addLine(a.active, state.RoleInfo, "* You are no longer away")
	} else {
		a.addLine(a.active, state.RoleInfo, "* You are now away ("+msg+")")
	}
}

// sendAway tells one connection the away state, also after reconnecting.
func (a *App) sendAway(c *irc.Conn) {
	switch {
	case c == nil:
	case a.away == "":
		c.Send("AWAY")
	default:
		c.Send("AWAY", a.away)
	}
}

// awayUpstream sets the away message on network n itself. soju keeps its
// clients' AWAY to itself: once they're all away it sends its own "AWAY *"
// (or "Auto away"), and servers with draft/pre-away, ergo among them, don't
// show "*" at all. So trout has BouncerServ quote the real message to the
// network, again whenever soju or trout reconnects, which resets it. It
// waits for soju's reply to our AWAY (RPL_NOWAWAY or RPL_UNAWAY): soju
// sends its own AWAY upstream just before, and the quote must come after.
func (a *App) awayUpstream(n *state.Network) {
	if a.opts.Demo || n == a.home || n.Down() || a.control == nil {
		return
	}
	line := "AWAY"
	if a.away != "" {
		line = "AWAY :" + a.away
	}
	a.serv(a.home, n.Status(), true, "network", "quote", n.Name, line)
}

// openSetAway is Commands > Set Away.
func (a *App) openSetAway() {
	a.openDialog(&formBox{Title: "Set Away", Lines: []string{"You're away on every network until you come back.",
		"An empty message means you're back."},
		Fields:  []*formField{newField("Message:", cmp.Or(a.away, "Gone fishing"), false)},
		Buttons: []string{"OK", "Cancel"},
		OnOK: func(a *App, vals []string) (tea.Cmd, error) {
			a.setAway(strings.TrimSpace(vals[0]))
			return nil, nil
		}})
}

// whoKey names a WHO trout asked for itself (on joining a channel, for the
// nicklist's away state), so its replies stay out of the status window.
func whoKey(n *state.Network, ch string) string { return n.ID + " " + strings.ToLower(ch) }

// who handles WHO replies to trout's own WHO: 352 marks who's away ("G"
// for gone), 315 ends it. It reports whether m was one of those.
func (a *App) who(n *state.Network, m ircmsg.Message) bool {
	p := m.Params
	switch {
	case m.Command == "352" && len(p) >= 7 && a.quietWho[whoKey(n, p[1])]:
		if w := n.Find(state.KChannel, p[1]); w != nil {
			if i := w.NickIndex(p[5]); i >= 0 {
				w.Nicks[i].Away = strings.HasPrefix(p[6], "G")
			}
		}
		return true
	case m.Command == "315" && len(p) >= 2 && a.quietWho[whoKey(n, p[1])]:
		delete(a.quietWho, whoKey(n, p[1]))
		return true
	}
	return false
}

// awayNotify handles AWAY from someone else (away-notify) and RPL_AWAY:
// the nicklist greys them out, and an open query says so.
func (a *App) awayNotify(n *state.Network, nick, msg string, t time.Time) {
	n.SetAway(nick, msg != "")
	q := n.Find(state.KQuery, nick)
	if q == nil {
		return
	}
	switch {
	case msg != "" && q.Away != msg:
		a.addLineAt(q, state.RoleInfo, "* "+nick+" is away: "+msg, t)
	case msg == "" && q.Away != "":
		a.addLineAt(q, state.RoleInfo, "* "+nick+" is back", t)
	}
	q.Away = msg
}

// whoisLine turns a WHOIS (or WHOWAS) reply into the line mIRC shows, or
// reports false for numerics that aren't part of one.
func whoisLine(m ircmsg.Message) (nick, text string, ok bool) {
	p := m.Params
	if len(p) < 3 {
		return "", "", false
	}
	nick, last := p[1], p[len(p)-1]
	switch m.Command {
	case "311", "314": // RPL_WHOISUSER, RPL_WHOWASUSER: nick user host * :real name
		if len(p) < 6 {
			return "", "", false
		}
		verb := " is "
		if m.Command == "314" {
			verb = " was "
		}
		return nick, nick + verb + p[2] + "@" + p[3] + " * " + p[5], true
	case "319": // RPL_WHOISCHANNELS
		return nick, nick + " on " + last, true
	case "312": // RPL_WHOISSERVER: nick server :info
		return nick, nick + " using " + strings.Join(p[2:], " "), true
	case "317": // RPL_WHOISIDLE: nick idle signon :text
		idle, _ := strconv.Atoi(p[2])
		text := nick + " has been idle " + duration(idle)
		if len(p) >= 5 {
			if on, err := strconv.ParseInt(p[3], 10, 64); err == nil {
				text += ", signed on " + time.Unix(on, 0).Format("Mon Jan 2 15:04:05")
			}
		}
		return nick, text, true
	case "330": // RPL_WHOISACCOUNT: nick account :is logged in as
		return nick, nick + " is logged in as " + p[2], true
	case "301": // RPL_AWAY
		return nick, nick + " is away: " + last, true
	case "318", "369": // RPL_ENDOFWHOIS, RPL_ENDOFWHOWAS
		return nick, nick + " " + last, true
	case "307", "313", "320", "335", "338", "378", "379", "671", "276":
		return nick, nick + " " + strings.Join(p[2:], " "), true
	}
	return "", "", false
}

// duration is mIRC's $duration: "1wk 2days 3hrs 4mins 5secs".
func duration(secs int) string {
	units := []struct {
		name string
		size int
	}{{"wk", 7 * 86400}, {"day", 86400}, {"hr", 3600}, {"min", 60}, {"sec", 1}}
	var parts []string
	for _, u := range units {
		if v := secs / u.size; v > 0 || (u.size == 1 && len(parts) == 0) {
			secs -= v * u.size
			name := u.name
			if v != 1 {
				name += "s"
			}
			parts = append(parts, fmt.Sprintf("%d%s", v, name))
		}
	}
	return strings.Join(parts, " ")
}

// bellGap keeps a burst of highlights to one beep.
const bellGap = 2 * time.Second

// beepFor rings the terminal bell for a highlight or private message that
// arrives where you aren't looking.
func (a *App) beepFor(w *state.Window, l state.Line) {
	if a.hideLines { // a script's ^ event hid the line
		return
	}
	said := l.Role == state.RoleNormal || l.Role == state.RoleAction || l.Role == state.RoleHighlight
	private := (w.Kind == state.KQuery || w.Kind == state.KChat) && said
	// A window's own Beeping beeps on every message there, like mIRC's.
	own := a.winOpts(w).Beep == "on" && said
	if (own || a.beep && ((l.Role == state.RoleHighlight && !l.NoBeep) || private)) && (w != a.active || !a.focused) {
		a.bellDue = true
	}
}

// bell is the beep, if one is due. Writing BEL beside Bubble Tea's
// renderer is safe: each frame goes out in a single write, and writes to
// the terminal don't interleave.
func (a *App) bell() tea.Cmd {
	if !a.bellDue {
		return nil
	}
	a.bellDue = false
	if a.bellOut == nil || time.Since(a.lastBell) < bellGap {
		return nil
	}
	a.lastBell = time.Now()
	out := a.bellOut
	return func() tea.Msg {
		out.Write([]byte("\a"))
		return nil
	}
}
