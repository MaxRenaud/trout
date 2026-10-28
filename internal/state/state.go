// Package state is trout's data model: networks, their windows, lines and
// nicklists. It does no I/O; IRC events and the UI both work on it.
package state

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

type Kind int

const (
	KStatus Kind = iota
	KChannel
	KQuery
)

// Activity is unseen activity in a window, shown as switchbar and treebar
// colors.
type Activity int

const (
	ActNone Activity = iota
	ActEvent
	ActMsg
	ActHighlight
)

// Role is what kind of line a message is. Themes map roles to colors, like
// mIRC's Options > Display > Colors.
type Role int

const (
	RoleNormal Role = iota
	RoleOwn
	RoleAction
	RoleEvent // joins, parts, modes, nick and topic changes
	RoleQuit
	RoleNotice
	RoleInfo
	RoleHighlight
	RoleTimestamp
	RoleCount
)

// ActivityFor is the activity a new line of role r marks a window with.
func ActivityFor(r Role) Activity {
	switch r {
	case RoleOwn:
		return ActNone
	case RoleHighlight:
		return ActHighlight
	case RoleNormal, RoleAction, RoleNotice:
		return ActMsg
	}
	return ActEvent
}

type Line struct {
	Time time.Time
	Role Role
	Text string // may contain mIRC formatting codes
}

type Nick struct{ Prefix, Name string }

type Window struct {
	Kind    Kind
	Net     *Network
	Name    string
	Topic   string // channels
	Modes   string // channels
	Host    string // queries
	Lines   []Line
	Nicks   []Nick
	NickSel string
	NickTop int
	Scroll  int // rows scrolled up from the bottom
	Marker  int // len(Lines) when you last left the window; 0 means none
	Act     Activity
	Edit    EditBox

	// Joined is false for a channel window you've left or been kicked from.
	Joined bool
	// names collects a NAMES reply until RPL_ENDOFNAMES.
	names []Nick
}

type Network struct {
	ID           string // soju's netid
	Name, Server string
	Port         int
	Nick, Umodes string
	Collapsed    bool
	Wins         []*Window // Wins[0] is the status window

	// From RPL_ISUPPORT PREFIX, e.g. modes "ov" for prefixes "@+".
	PrefixModes, Prefixes string
	// From RPL_ISUPPORT CHANMODES: list, always-arg, arg-when-set, flag modes.
	ChanModes string
}

// NewNetwork returns a network with its status window.
func NewNetwork(name string) *Network {
	n := &Network{Name: name, Port: 6667, PrefixModes: "ov", Prefixes: "73", ChanModes: "beI,k,l,imnpst"}
	n.Wins = []*Window{{Kind: KStatus, Net: n, Name: "Status"}}
	return n
}

func (n *Network) Status() *Window { return n.Wins[0] }

func (n *Network) Add(w *Window) *Window {
	w.Net = n
	n.Wins = append(n.Wins, w)
	return w
}

// AddChannel adds a channel window after the network's other channels, so
// queries stay at the end.
func (n *Network) AddChannel(w *Window) *Window {
	w.Net = n
	at := 1
	for i, x := range n.Wins {
		if x.Kind == KChannel {
			at = i + 1
		}
	}
	n.Wins = slices.Insert(n.Wins, at, w)
	return w
}

// Find returns the window named name, ignoring case like IRC does.
func (n *Network) Find(kind Kind, name string) *Window {
	for _, w := range n.Wins {
		if w.Kind == kind && strings.EqualFold(w.Name, name) {
			return w
		}
	}
	return nil
}

// IsChannel reports whether target names a channel rather than a nick.
func (n *Network) IsChannel(target string) bool {
	return target != "" && strings.ContainsRune("#&+!", rune(target[0]))
}

// SplitPrefix splits a NAMES entry like "@+alice" into its highest prefix
// and the nick.
func (n *Network) SplitPrefix(s string) Nick {
	i := 0
	for i < len(s) && strings.IndexByte(n.Prefixes, s[i]) >= 0 {
		i++
	}
	if i == 0 {
		return Nick{Name: s}
	}
	return Nick{Prefix: s[:1], Name: s[i:]}
}

// PrefixOf returns nick's mode prefix (@ or +) in a channel window.
func (w *Window) PrefixOf(nick string) string {
	if i := w.NickIndex(nick); i >= 0 {
		return w.Nicks[i].Prefix
	}
	return ""
}

func (w *Window) NickIndex(nick string) int {
	return slices.IndexFunc(w.Nicks, func(n Nick) bool { return strings.EqualFold(n.Name, nick) })
}

// AddNick adds or updates a nick and keeps the list sorted.
func (w *Window) AddNick(n Nick) {
	if i := w.NickIndex(n.Name); i >= 0 {
		w.Nicks[i] = n
	} else {
		w.Nicks = append(w.Nicks, n)
	}
	w.SortNicks()
}

func (w *Window) RemoveNick(nick string) bool {
	i := w.NickIndex(nick)
	if i < 0 {
		return false
	}
	w.Nicks = slices.Delete(w.Nicks, i, i+1)
	return true
}

// BeginNames and AddNames collect a NAMES reply; EndNames replaces the
// nicklist with it.
func (w *Window) AddNames(ns ...Nick) { w.names = append(w.names, ns...) }

func (w *Window) EndNames() {
	w.Nicks, w.names = w.names, nil
	w.SortNicks()
}

// SortNicks orders the nicklist the way mIRC does: by prefix rank (ops
// first), then alphabetically.
func (w *Window) SortNicks() {
	prefixes := "@+"
	if w.Net != nil {
		prefixes = w.Net.Prefixes
	}
	rank := func(p string) int {
		if p == "" {
			return len(prefixes)
		}
		return strings.Index(prefixes, p)
	}
	slices.SortFunc(w.Nicks, func(a, b Nick) int {
		return cmp.Or(
			cmp.Compare(rank(a.Prefix), rank(b.Prefix)),
			strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
		)
	})
}

// SetPrefix applies a +o/-o style mode change to nick: mode is a letter from
// PrefixModes.
func (w *Window) SetPrefix(nick string, mode byte, on bool) {
	i := w.NickIndex(nick)
	m := strings.IndexByte(w.Net.PrefixModes, mode)
	if i < 0 || m < 0 {
		return
	}
	p := w.Net.Prefixes[m : m+1]
	switch {
	case on && (w.Nicks[i].Prefix == "" || strings.Index(w.Net.Prefixes, w.Nicks[i].Prefix) > m):
		w.Nicks[i].Prefix = p
	case !on && w.Nicks[i].Prefix == p:
		w.Nicks[i].Prefix = "" // without multi-prefix we can't know a lower one
	}
	w.SortNicks()
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	return max(lo, min(v, hi))
}
