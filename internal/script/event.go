package script

import (
	"strings"

	"github.com/ergochat/irc-go/ircmsg"
)

// Event is something that happened, for scripts to react to.
type Event struct {
	// Kind is the mIRC event name: TEXT ACTION NOTICE CTCP CTCPREPLY JOIN
	// PART QUIT NICK KICK TOPIC INVITE CONNECT DISCONNECT, or RAW for
	// anything else.
	Kind    string
	Network string
	Nick    string // who did it: $nick
	Address string // their user@host: $address
	Chan    string // the channel, "" for private events: $chan
	Target  string // who was kicked ($knick), the new nick ($newnick), or a message's target
	Text    string // the message, reason or CTCP: $1-
	Params  []string
	Command string // the IRC command or numeric, like PRIVMSG or 001
	Numeric int    // for RAW: the numeric, or 0 for a command
	// Own is true for our own messages. mIRC never sees those as events
	// (they aren't echoed back to it), so on TEXT, ACTION, NOTICE and
	// CTCP skip them. Our own JOIN, PART and NICK do trigger events, like
	// mIRC's: scripts use the me and ! prefixes to tell.
	Own bool
}

// EventFromMessage makes an Event from any IRC message from network. own
// says whether we sent it (the client knows its casemapping; the engine
// doesn't).
func EventFromMessage(network string, m ircmsg.Message, own bool) Event {
	ev := Event{Kind: "RAW", Network: network, Params: m.Params, Command: m.Command, Own: own}
	nick, addr, _ := strings.Cut(m.Source, "!")
	ev.Nick, ev.Address = nick, addr
	param := func(i int) string {
		if i < len(m.Params) {
			return m.Params[i]
		}
		return ""
	}
	switch m.Command {
	case "PRIVMSG", "NOTICE":
		ev.Kind = "TEXT"
		if m.Command == "NOTICE" {
			ev.Kind = "NOTICE"
		}
		ev.Target, ev.Text = param(0), param(1)
		if isChannel(ev.Target) {
			ev.Chan = ev.Target
		}
		if inner, ok := strings.CutPrefix(ev.Text, "\x01"); ok {
			inner = strings.TrimSuffix(inner, "\x01")
			switch {
			case m.Command == "NOTICE":
				ev.Kind, ev.Text = "CTCPREPLY", inner
			case strings.HasPrefix(strings.ToUpper(inner), "ACTION ") || strings.EqualFold(inner, "ACTION"):
				ev.Kind, ev.Text = "ACTION", strings.TrimSpace(inner[len("ACTION"):])
			default:
				ev.Kind, ev.Text = "CTCP", inner
			}
		}
	case "JOIN":
		ev.Kind, ev.Chan = "JOIN", param(0)
	case "PART":
		ev.Kind, ev.Chan, ev.Text = "PART", param(0), param(1)
	case "KICK":
		ev.Kind, ev.Chan, ev.Target, ev.Text = "KICK", param(0), param(1), param(2)
	case "QUIT":
		ev.Kind, ev.Text = "QUIT", param(0)
	case "NICK":
		ev.Kind, ev.Target = "NICK", param(0)
	case "TOPIC":
		ev.Kind, ev.Chan, ev.Text = "TOPIC", param(0), param(1)
	case "INVITE":
		ev.Kind, ev.Target, ev.Chan = "INVITE", param(0), param(1)
	default:
		// A numeric, or a command trout doesn't handle: raw events see its
		// parameters as $1-.
		ev.Numeric = atoi(m.Command)
		ev.Text = strings.Join(m.Params, " ")
	}
	return ev
}

// isChannel is a guess from the name, since the engine doesn't see
// CHANTYPES: #, & and ! are channels, like most networks' defaults.
func isChannel(name string) bool {
	return name != "" && strings.ContainsRune("#&!", rune(name[0]))
}

// Event kinds this engine runs, and the fields each one's definition has
// between the event name and the commands, like mIRC: on TEXT has
// matchtext and location, on JOIN a location, on QUIT nothing.
var eventFields = map[string]int{
	"TEXT": 2, "ACTION": 2, "NOTICE": 2,
	"JOIN": 1, "PART": 1, "KICK": 1, "TOPIC": 1, "INVITE": 1,
	"QUIT": 0, "NICK": 0, "CONNECT": 0, "DISCONNECT": 0,
	"LOAD": 0, "START": 0,
}

// dispatchFile runs f's handler for ev, if one matches. Like mIRC, a
// script runs at most one handler per event: the matching one with the
// highest level, or the first of those. It reports whether it /halted.
func (r *run) dispatchFile(f *file, ev *Event) bool {
	if ev.Own && (ev.Kind == "TEXT" || ev.Kind == "ACTION" || ev.Kind == "NOTICE" || ev.Kind == "CTCP") {
		return false
	}
	var best *event
	for _, h := range f.events {
		if h.kind != ev.Kind || (h.group != "" && !r.e.groups[strings.ToLower(h.group)]) {
			continue
		}
		if best != nil && h.level <= best.level {
			continue
		}
		if r.matches(f, h, ev) {
			best = h
		}
	}
	if best == nil {
		return false
	}
	// Each script's handler has its own budget: one that never ends
	// doesn't stop the others.
	r.steps = 0
	fr := r.eventFrame(f, ev)
	return r.runBody(fr, best.body)
}

func (r *run) eventFrame(f *file, ev *Event) *frame {
	fr := &frame{file: f, ev: ev, network: ev.Network, channel: ev.Chan, params: fields(ev.Text)}
	switch {
	case ev.Chan != "":
		fr.window = ev.Chan
	case ev.Kind == "TEXT" || ev.Kind == "ACTION" || ev.Kind == "NOTICE" || ev.Kind == "CTCP":
		fr.window = ev.Nick
	}
	return fr
}

// matches reports whether handler h triggers for ev.
func (r *run) matches(f *file, h *event, ev *Event) bool {
	// Without a Users list everyone has the default level, 1, like an
	// unlisted user in mIRC: they get * events and levels up to 1, or
	// exactly 1 with the + prefix. Named levels need the list, so they
	// never trigger (Load says so).
	switch {
	case h.named:
		return false
	case h.exact && h.level != 1, !h.any && h.level > 1:
		return false
	case h.me && !ev.Own, h.notMe && ev.Own:
		return false
	}
	fr := r.eventFrame(f, ev)
	if h.op && (ev.Chan == "" || !r.prefixed(ev.Network, ev.Chan, r.h.Me(ev.Network), "@~&")) {
		return false
	}
	switch {
	case h.kind == "RAW":
		// raw <numeric>:<matchtext>: the numeric or command name, which can
		// be a wildcard.
		if !wildMatch(h.loc, ev.Command, false) {
			return false
		}
	case h.loc != "" && !r.matchLocation(fr, h.loc, ev):
		return false
	}
	if h.match != "" {
		pattern := h.match
		if strings.HasPrefix(pattern, "%") || strings.HasPrefix(pattern, "$(") {
			v, err := r.eval(fr, pattern)
			if err != nil {
				return false
			}
			pattern = v
		}
		text := ev.Text
		if h.regex {
			// The $ prefix: matchtext is a regular expression, and its
			// captures are $regml's.
			n, err := r.regex("", text, pattern)
			if err != nil {
				r.report(fr, err)
			}
			return n > 0
		}
		if !wildMatch(pattern, text, false) {
			// A CTCP's matchtext is usually just its command, as in
			// mIRC's "ctcp 5:opme:?:/mode $2 +o $nick".
			if ev.Kind != "CTCP" || !wildMatch(pattern, firstWord(text), false) {
				return false
			}
		}
	}
	return true
}

// matchLocation checks an event's location field: # for any channel, ?
// for private, * for both, or a comma-separated list of channels. It can
// also be a %variable holding those.
func (r *run) matchLocation(fr *frame, loc string, ev *Event) bool {
	if strings.HasPrefix(loc, "%") {
		v, err := r.eval(fr, loc)
		if err != nil {
			return false
		}
		loc = v
	}
	for l := range strings.SplitSeq(loc, ",") {
		switch {
		case l == "*":
			return true
		case l == "#":
			if ev.Chan != "" {
				return true
			}
		case l == "?":
			if ev.Chan == "" {
				return true
			}
		case ev.Chan != "" && wildMatch(l, ev.Chan, false):
			return true
		}
	}
	return false
}

func firstWord(s string) string {
	w, _, _ := strings.Cut(s, " ")
	return w
}
