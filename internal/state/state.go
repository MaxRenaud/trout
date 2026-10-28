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
	KTransfer // a DCC Get window, like mIRC's: a download's progress
	KChat     // a DCC chat, =nick like mIRC
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
	// Color, if set, is a highlight rule's palette color plus one; NoBeep
	// is a rule that highlights without the bell.
	Color  int
	NoBeep bool
}

// ListEntry is one entry of a channel's ban, exception or invite list.
type ListEntry struct {
	Mask, By string
	At       time.Time
}

// TopicChange is a topic a channel had, and who set it when.
type TopicChange struct {
	Text, By string
	At       time.Time
}

type Nick struct {
	Prefix, Name string
	Away         bool // from WHO and away-notify
}

type Window struct {
	Kind    Kind
	Net     *Network
	Name    string
	Topic   string // channels
	Modes   string // channels
	Host    string // queries
	Away    string // queries: their away message
	Lines   []Line
	Nicks   []Nick
	NickSel string
	NickTop int
	Scroll  int // rows scrolled up from the bottom
	Marker  int // len(Lines) when you last left the window; 0 means none
	Act     Activity
	Edit    EditBox
	// Edit2 is mIRC's second editbox (Alt+Q), shown when Edit2On;
	// Edit2Focus means typing goes there.
	Edit2               EditBox
	Edit2On, Edit2Focus bool

	// Latest is the server time of the newest message, and ReadAt soju's
	// read marker (draft/read-marker): everything up to it has been read,
	// here or in another client.
	Latest, ReadAt time.Time

	// Lists are a channel's ban, exception and invite lists (and other
	// list modes), by mode letter, once fetched for Channel Central; Topics
	// is the topics seen while you're in it.
	Lists  map[byte][]ListEntry
	Topics []TopicChange

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

	// Attrs is how soju describes the network (soju.im/bouncer-networks):
	// name, host, port, tls, nickname, and state ("connected", "connecting"
	// or "disconnected") with any error.
	Attrs map[string]string
	// Disabled is a network soju was told not to connect to, here or in
	// another client (BouncerServ's network status says so).
	Disabled bool
	// Online is who on the notify list is on the network (MONITOR), by
	// lowercased nick.
	Online map[string]bool
	// FileHost is where the bouncer takes uploads (soju.im/FILEHOST).
	FileHost string
	// Monitor is whether the network takes MONITOR for the notify list;
	// without it trout polls with ISON.
	Monitor bool
	// CaseMapping is RPL_ISUPPORT's CASEMAPPING: "ascii", or "rfc1459"
	// (the default), where []\~ are the capitals of {}|^.
	CaseMapping string
}

// Down reports whether soju says it isn't connected to the network.
func (n *Network) Down() bool { return n.Disabled || n.Attrs["state"] == "disconnected" }

// State is soju's word for the network's connection, "disabled" when it
// was switched off.
func (n *Network) State() string {
	if n.Disabled {
		return "disabled"
	}
	return n.Attrs["state"]
}

// SetAway marks nick away or back in every channel window.
func (n *Network) SetAway(nick string, away bool) {
	for _, w := range n.Wins {
		if i := w.NickIndex(nick); i >= 0 {
			w.Nicks[i].Away = away
		}
	}
}

// NewNetwork returns a network with its status window.
func NewNetwork(name string) *Network {
	n := &Network{Name: name, Port: 6667, PrefixModes: "ov", Prefixes: "@+", ChanModes: "beI,k,l,imnpst"}
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
		if w.Kind == kind && n.Same(w.Name, name) {
			return w
		}
	}
	return nil
}

// foldByte lowercases one byte by the network's CASEMAPPING. Case
// mappings are ASCII-only, so other bytes stay as they are.
func (n *Network) foldByte(c byte) byte {
	switch {
	case c >= 'A' && c <= 'Z':
		return c + 'a' - 'A'
	case n.CaseMapping == "ascii":
		return c
	case c == '[':
		return '{'
	case c == ']':
		return '}'
	case c == '\\':
		return '|'
	case c == '~' && n.CaseMapping != "rfc1459-strict":
		return '^'
	}
	return c
}

// Fold lowercases a nick or channel name by the network's CASEMAPPING.
func (n *Network) Fold(s string) string {
	b := []byte(s)
	for i, c := range b {
		b[i] = n.foldByte(c)
	}
	return string(b)
}

// Same reports whether two nicks or channel names are the same on n.
func (n *Network) Same(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range len(a) {
		if n.foldByte(a[i]) != n.foldByte(b[i]) {
			return false
		}
	}
	return true
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

// MaxLines is how many lines a window keeps (Options > Other's window
// buffer).
var MaxLines = 500

// Merge adds history lines to a window in time order, skipping ones it
// already has (the same time and text), and keeps the newest MaxLines.
// The line marker stays after the same line. It returns how many lines
// were new.
func (w *Window) Merge(lines []Line) int {
	type key struct {
		t    int64
		text string
	}
	have := map[key]bool{}
	for _, l := range w.Lines {
		have[key{l.Time.UnixMilli(), l.Text}] = true
	}
	var markerAt time.Time
	if w.Marker > 0 && w.Marker <= len(w.Lines) {
		markerAt = w.Lines[w.Marker-1].Time
	}
	added := 0
	for _, l := range lines {
		if k := (key{l.Time.UnixMilli(), l.Text}); !have[k] {
			have[k] = true
			w.Lines = append(w.Lines, l)
			added++
		}
	}
	slices.SortStableFunc(w.Lines, func(a, b Line) int { return a.Time.Compare(b.Time) })
	if n := len(w.Lines) - MaxLines; n > 0 {
		w.Lines = slices.Delete(w.Lines, 0, n)
	}
	if !markerAt.IsZero() {
		w.Marker = w.LinesUpTo(markerAt)
	}
	return added
}

// LinesUpTo counts the lines from no later than t.
func (w *Window) LinesUpTo(t time.Time) int {
	i, _ := slices.BinarySearchFunc(w.Lines, t, func(l Line, t time.Time) int {
		if l.Time.After(t) {
			return 1
		}
		return -1
	})
	return i
}

// PrefixOf returns nick's mode prefix (@ or +) in a channel window.
func (w *Window) PrefixOf(nick string) string {
	if i := w.NickIndex(nick); i >= 0 {
		return w.Nicks[i].Prefix
	}
	return ""
}

func (w *Window) NickIndex(nick string) int {
	same := strings.EqualFold
	if w.Net != nil {
		same = w.Net.Same
	}
	return slices.IndexFunc(w.Nicks, func(n Nick) bool { return same(n.Name, nick) })
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
