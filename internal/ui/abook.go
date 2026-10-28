package ui

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/MaxRenaud/trout/internal/config"
	"github.com/MaxRenaud/trout/internal/state"
)

// The Address Book (Alt+B), like mIRC's: tabs for people, the notify
// list, ignores, Control (auto-op, auto-voice, protect), Nick Colors and
// Highlight, each a list with Add, Edit and Remove. Notify and Ignore are
// per network; the rest apply everywhere unless an entry names one.

type abookTab int

const (
	tabAddress abookTab = iota
	tabNotify
	tabIgnore
	tabControl
	tabNickColors
	tabHighlight
)

var abookTabs = []string{"Address", "Notify", "Ignore", "Control", "Nick Colors", "Highlight"}

type abookBox struct {
	n        *state.Network
	tab      abookTab
	sel, top int
}

func (a *App) openAddressBook()            { a.openBook(tabAddress) }
func (a *App) openBook(tab abookTab)       { a.openDialog(&abookBox{n: a.active.Net, tab: tab}) }
func (a *App) openHighlight()              { a.openBook(tabHighlight) }
func (d *abookBox) title() string          { return "Address Book - " + d.n.Name }
func (d *abookBox) size(a *App) (int, int) { return min(80, a.w-2), clamp(a.h-4, 14, 20) }

// rows are the open tab's entries.
func (d *abookBox) rows(a *App) []string {
	b, n := a.book(), d.n
	var rows []string
	switch d.tab {
	case tabAddress:
		for _, e := range b.Addresses {
			rows = append(rows, padRight(e.Nick, 16)+padRight(e.Name, 20)+e.Email)
		}
	case tabNotify:
		for _, s := range a.netList(listNotify, n) {
			e := parseNotify(s)
			st := "offline"
			if n.Online[n.Fold(e.nick)] {
				st = "online"
			}
			if e.whois {
				st += ", whois"
			}
			rows = append(rows, padRight(e.nick, 16)+padRight(st, 18)+e.note)
		}
	case tabIgnore:
		for _, s := range a.netList(listIgnore, n) {
			e := parseIgnore(s)
			until := "always"
			if !e.until.IsZero() {
				until = "until " + e.until.Format("Jan 2 15:04")
			}
			rows = append(rows, padRight(e.mask, 30)+padRight(ignoreFlagNames(e.flags), 26)+until)
		}
	case tabControl:
		for _, e := range b.Control {
			rows = append(rows, padRight(e.Mask, 24)+padRight(controlNames(e.Kinds), 22)+
				padRight(cmp.Or(strings.Join(e.Channels, ","), "all channels"), 18)+e.Network)
		}
	case tabNickColors:
		for _, r := range b.NickColors {
			rows = append(rows, padRight(r.Mask, 24)+padRight(cmp.Or(r.Modes, "any mode"), 10)+
				padRight(fmt.Sprintf("color %d", r.Color), 10)+r.Network)
		}
	case tabHighlight:
		for _, r := range b.Highlights {
			color := "highlight color"
			if r.Color >= 0 {
				color = fmt.Sprintf("color %d", r.Color)
			}
			beep := ""
			if r.Beep {
				beep = ", beep"
			}
			rows = append(rows, padRight(r.Words, 24)+padRight(cmp.Or(r.In, "anywhere"), 16)+color+beep)
		}
	}
	return rows
}

func ignoreFlagNames(flags string) string {
	if flags == "pcntid" {
		return "everything"
	}
	names := map[rune]string{'p': "private", 'c': "channel", 'n': "notice", 't': "ctcp", 'i': "invite", 'k': "codes", 'd': "dcc"}
	var out []string
	for _, f := range flags {
		if s, ok := names[f]; ok {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

func controlNames(kinds string) string {
	var out []string
	for _, k := range []struct{ k, name string }{{"o", "auto-op"}, {"v", "auto-voice"}, {"p", "protect"}} {
		if strings.Contains(kinds, k.k) {
			out = append(out, k.name)
		}
	}
	return strings.Join(out, ",")
}

var abookButtons = []string{"Add...", "Edit...", "Remove", "Close"}

func (d *abookBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	// The tabs, like a Windows tab strip.
	tx := x + 2
	for i, name := range abookTabs {
		st, label := face, " "+name+" "
		if abookTab(i) == d.tab {
			st = Style{FG: t.SelFG, BG: t.SelBG, Bold: true}
		}
		c.Text(tx, y+2, runewidth.StringWidth(label), label, st)
		a.addRegion(Region{X: tx, Y: y + 2, W: runewidth.StringWidth(label), H: 1, Kind: HitDlg, S: "tab", I: i})
		tx += runewidth.StringWidth(label) + 1
	}
	rows := d.rows(a)
	lx, ly, lw, lh := x+2, y+4, w-4, h-8
	c.Fill(lx, ly, lw, lh, Style{BG: t.TextBG})
	d.sel = clamp(d.sel, 0, len(rows)-1)
	d.top = clamp(d.top, max(0, d.sel-lh+1), d.sel)
	if len(rows) == 0 {
		c.Text(lx+1, ly, lw-1, abookEmpty[d.tab], Style{FG: t.TextFG, BG: t.TextBG})
	}
	for i := 0; i < lh && d.top+i < len(rows); i++ {
		idx := d.top + i
		st := Style{FG: t.TextFG, BG: t.TextBG}
		if idx == d.sel {
			st = Style{FG: t.SelFG, BG: t.SelBG}
		}
		c.Fill(lx, ly+i, lw, 1, st)
		c.Text(lx+1, ly+i, lw-1, rows[idx], st)
		a.addRegion(Region{X: lx, Y: ly + i, W: lw, H: 1, Kind: HitDlg, S: "item", I: idx})
	}
	a.dlgButtons(c, x, y+h-3, w, abookButtons, 0)
}

var abookEmpty = map[abookTab]string{
	tabAddress:    "Nobody yet. Add keeps a name, email, website and notes for a nick.",
	tabNotify:     "Nobody yet. Notify tells you when someone comes on or leaves.",
	tabIgnore:     "Nobody ignored on this network.",
	tabControl:    "Nobody yet. Auto-op and auto-voice people when they join; protect them.",
	tabNickColors: "No rules yet. Color nicks by who they are or their mode.",
	tabHighlight:  "No rules yet. Highlight lines with words, or from someone.",
}

func (d *abookBox) press(a *App, button string) tea.Cmd {
	switch button {
	case "Add...":
		a.editBookEntry(d, -1)
	case "Edit...":
		if len(d.rows(a)) > 0 {
			a.editBookEntry(d, d.sel)
		}
	case "Remove":
		if len(d.rows(a)) > 0 {
			a.removeBookEntry(d, d.sel)
		}
	default:
		a.dlg = nil
	}
	return nil
}

func (d *abookBox) key(a *App, k string) tea.Cmd {
	switch k {
	case "left", "shift+tab":
		d.tab, d.sel = (d.tab+abookTab(len(abookTabs))-1)%abookTab(len(abookTabs)), 0
	case "right", "tab":
		d.tab, d.sel = (d.tab+1)%abookTab(len(abookTabs)), 0
	case "up":
		d.sel--
	case "down":
		d.sel++
	case "enter":
		return d.press(a, "Edit...")
	case "insert":
		return d.press(a, "Add...")
	case "delete", "backspace":
		return d.press(a, "Remove")
	case "esc":
		a.dlg = nil
	}
	return nil
}

func (d *abookBox) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	switch {
	case m.Button == tea.MouseButtonWheelUp:
		d.sel--
	case m.Button == tea.MouseButtonWheelDown:
		d.sel++
	case m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft:
	case r.S == "tab":
		d.tab, d.sel = abookTab(r.I), 0
	case r.S == "item":
		d.sel = r.I
		if double {
			return d.press(a, "Edit...")
		}
	case r.S == "button":
		return d.press(a, abookButtons[r.I])
	case r.S == "close":
		a.dlg = nil
	}
	return nil
}

// removeBookEntry removes entry i of the open tab.
func (a *App) removeBookEntry(d *abookBox, i int) {
	b, n := a.book(), d.n
	switch d.tab {
	case tabAddress:
		b.Addresses = slices.Delete(b.Addresses, i, i+1)
	case tabNotify:
		a.setNotifyEntry(n, parseNotify(a.netList(listNotify, n)[i]), false)
	case tabIgnore:
		a.setIgnoreEntry(n.Status(), parseIgnore(a.netList(listIgnore, n)[i]), false)
	case tabControl:
		b.Control = slices.Delete(b.Control, i, i+1)
	case tabNickColors:
		b.NickColors = slices.Delete(b.NickColors, i, i+1)
	case tabHighlight:
		b.Highlights = slices.Delete(b.Highlights, i, i+1)
	}
	a.saveConfig()
}

// editBookEntry opens the form for entry i of the open tab (-1 for a new
// one); OK saves it, and both OK and Cancel go back to the book.
func (a *App) editBookEntry(d *abookBox, i int) {
	b, n := a.book(), d.n
	back := func(a *App) tea.Cmd { a.openDialog(d); return nil }
	f := &formBox{Buttons: []string{"OK", "Cancel"}, OnCancel: back}
	text, check := newField, newCheck
	done := func(err error) (tea.Cmd, error) {
		if err != nil {
			return nil, err
		}
		a.saveConfig()
		a.openDialog(d)
		return nil, nil
	}
	switch d.tab {
	case tabAddress:
		var e config.Address
		if i >= 0 {
			e = b.Addresses[i]
		}
		f.Title = "Address"
		f.Fields = []*formField{text("Nick:", e.Nick, false), text("Name:", e.Name, false),
			text("Email:", e.Email, false), text("Website:", e.Website, false), text("Notes:", e.Notes, false)}
		f.Buttons = []string{"OK", "Whois", "Cancel"}
		f.OnButton = map[string]func(*App) tea.Cmd{"Whois": func(a *App) tea.Cmd {
			if nick := strings.TrimSpace(string(f.Fields[0].Edit.Buf)); nick != "" {
				a.dlg = nil
				a.sendNet(n, "WHOIS", nick) // mIRC's /uwho
			}
			return nil
		}}
		f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
			e := config.Address{Nick: strings.TrimSpace(v[0]), Name: v[1], Email: v[2], Website: v[3], Notes: v[4]}
			if e.Nick == "" {
				return nil, errors.New("give a nick")
			}
			b.Addresses = putAt(b.Addresses, i, e)
			return done(nil)
		}
	case tabNotify:
		var e notifyEntry
		if i >= 0 {
			e = parseNotify(a.netList(listNotify, n)[i])
		}
		f.Title = "Notify"
		f.Fields = []*formField{text("Nick:", e.nick, false), text("Note:", e.note, false),
			check("Whois them when they come on", e.whois)}
		f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
			ne := notifyEntry{nick: strings.TrimSpace(v[0]), note: strings.TrimSpace(v[1]), whois: v[2] == "1"}
			if ne.nick == "" {
				return nil, errors.New("give a nick")
			}
			if i >= 0 && !n.Same(e.nick, ne.nick) {
				a.setNotifyEntry(n, e, false)
			}
			a.setNotifyEntry(n, ne, true)
			return done(nil)
		}
	case tabIgnore:
		e := ignoreEntry{flags: "pcntid"}
		if i >= 0 {
			e = parseIgnore(a.netList(listIgnore, n)[i])
		}
		mins := ""
		if !e.until.IsZero() {
			mins = strconv.Itoa(max(1, int(time.Until(e.until).Minutes())))
		}
		f.Title, f.Compact = "Ignore", true
		f.Fields = []*formField{text("Mask:", e.mask, false)}
		kinds := []struct {
			flag  byte
			label string
		}{{'p', "Private messages"}, {'c', "Channel messages"}, {'n', "Notices"}, {'t', "CTCPs"},
			{'i', "Invites"}, {'k', "Colors and codes (show their lines plain)"}, {'d', "DCC offers"}}
		for _, k := range kinds {
			f.Fields = append(f.Fields, check(k.label, strings.IndexByte(e.flags, k.flag) >= 0))
		}
		f.Fields = append(f.Fields, text("Minutes:", mins, false))
		f.Lines = []string{"Empty minutes is for good."}
		f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
			ne := ignoreEntry{mask: ignoreMask(strings.TrimSpace(v[0]))}
			if strings.TrimSpace(v[0]) == "" {
				return nil, errors.New("give a nick or mask")
			}
			for j, k := range kinds {
				if v[1+j] == "1" {
					ne.flags += string(k.flag)
				}
			}
			if ne.flags == "" {
				return nil, errors.New("pick what to ignore")
			}
			if m := strings.TrimSpace(v[len(v)-1]); m != "" {
				mins, err := strconv.Atoi(m)
				if err != nil {
					return nil, errors.New("minutes is a number")
				}
				ne.until = time.Now().Add(time.Duration(mins) * time.Minute)
			}
			if i >= 0 {
				a.setIgnoreEntry(n.Status(), e, false)
			}
			a.setIgnoreEntry(n.Status(), ne, true)
			return done(nil)
		}
	case tabControl:
		var e config.ControlEntry
		if i >= 0 {
			e = b.Control[i]
		}
		f.Title, f.Compact = "Control", true
		f.Lines = []string{"Auto-op and auto-voice them when they join where you're an", "operator; protect them from being deopped or banned."}
		f.Fields = []*formField{text("Mask:", e.Mask, false), check("Auto-op", strings.Contains(e.Kinds, "o")),
			check("Auto-voice", strings.Contains(e.Kinds, "v")), check("Protect", strings.Contains(e.Kinds, "p")),
			text("Channels:", strings.Join(e.Channels, ","), false), text("Network:", e.Network, false)}
		f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
			ne := config.ControlEntry{Mask: strings.TrimSpace(v[0]), Network: strings.TrimSpace(v[5])}
			for j, k := range []string{"o", "v", "p"} {
				if v[1+j] == "1" {
					ne.Kinds += k
				}
			}
			for ch := range strings.SplitSeq(v[4], ",") {
				if ch = strings.TrimSpace(ch); ch != "" {
					ne.Channels = append(ne.Channels, ch)
				}
			}
			if ne.Mask == "" || ne.Kinds == "" {
				return nil, errors.New("give a mask and pick auto-op, auto-voice or protect")
			}
			b.Control = putAt(b.Control, i, ne)
			return done(nil)
		}
	case tabNickColors:
		r := config.NickColorRule{Color: 4}
		if i >= 0 {
			r = b.NickColors[i]
		}
		f.Title = "Nick Color"
		f.Lines = []string{"The first matching rule colors the nick. Modes like @ or +;", "empty for anyone. Color is 0-15 (Alt+K shows them)."}
		f.Fields = []*formField{text("Nick mask:", r.Mask, false), text("Modes:", r.Modes, false),
			text("Color:", strconv.Itoa(r.Color), false), text("Network:", r.Network, false)}
		f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
			color, err := strconv.Atoi(strings.TrimSpace(v[2]))
			if err != nil || color < 0 || color > 15 {
				return nil, errors.New("the color is 0 to 15")
			}
			nr := config.NickColorRule{Mask: strings.TrimSpace(v[0]), Modes: strings.TrimSpace(v[1]), Color: color, Network: strings.TrimSpace(v[3])}
			if nr.Mask == "" {
				return nil, errors.New("give a nick or mask (* for anyone)")
			}
			b.NickColors = putAt(b.NickColors, i, nr)
			return done(nil)
		}
	case tabHighlight:
		r := config.HighlightRule{MatchText: true, Color: -1, Beep: true}
		if i >= 0 {
			r = b.Highlights[i]
		}
		color := ""
		if r.Color >= 0 {
			color = strconv.Itoa(r.Color)
		}
		f.Title, f.Compact = "Highlight", true
		f.Lines = []string{"Words separated by commas, * and ? allowed. In: channels", "or nicks, empty for anywhere. Empty color: the highlight color."}
		f.Fields = []*formField{text("Words:", r.Words, false), text("In:", r.In, false),
			check("Match the message", r.MatchText), check("Match the nick", r.MatchNick),
			text("Color:", color, false), check("Beep", r.Beep), text("Network:", r.Network, false)}
		f.OnOK = func(a *App, v []string) (tea.Cmd, error) {
			nr := config.HighlightRule{Words: strings.TrimSpace(v[0]), In: strings.TrimSpace(v[1]),
				MatchText: v[2] == "1", MatchNick: v[3] == "1", Color: -1, Beep: v[5] == "1", Network: strings.TrimSpace(v[6])}
			if c := strings.TrimSpace(v[4]); c != "" {
				var err error
				if nr.Color, err = strconv.Atoi(c); err != nil || nr.Color < 0 || nr.Color > 15 {
					return nil, errors.New("the color is 0 to 15, or empty")
				}
			}
			if nr.Words == "" || (!nr.MatchText && !nr.MatchNick) {
				return nil, errors.New("give words, and match the message or the nick")
			}
			b.Highlights = putAt(b.Highlights, i, nr)
			return done(nil)
		}
	}
	a.openDialog(f)
}

// putAt replaces s[i], or appends for i < 0.
func putAt[T any](s []T, i int, v T) []T {
	if i >= 0 && i < len(s) {
		s[i] = v
		return s
	}
	return append(s, v)
}
