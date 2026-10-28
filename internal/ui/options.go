package ui

import (
	"fmt"
	"strings"

	"github.com/MaxRenaud/trout/internal/demo"
	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// optItem is a checkbox bound to Bool, a radio button that sets *Int to
// Val, or a button that runs Action.
type optItem struct {
	Label  string
	Bool   *bool
	Int    *int
	Val    int
	Action func(*App)
}

func (it optItem) toggle(a *App) {
	switch {
	case it.Bool != nil:
		*it.Bool = !*it.Bool
	case it.Int != nil:
		*it.Int = it.Val
	case it.Action != nil:
		it.Action(a)
	}
}

type optPage struct {
	Name  string
	Items []optItem
	Notes []string
}

// optionsBox is a small take on mIRC's Options dialog (Alt+O): categories
// on the left, settings on the right. Changes apply as you click them.
type optionsBox struct {
	pages   []optPage
	page    int
	sel     int
	onPages bool // keyboard focus is in the category list
}

const (
	optConnect = iota
	optIRC
	optDisplay
	optBouncer
)

func (a *App) openOptions(page int) {
	n := a.active.Net
	a.openDialog(&optionsBox{page: page, pages: []optPage{
		optConnect: {Name: "Connect", Notes: []string{
			"Nickname:     " + n.Nick,
			"Alternative:  " + n.Nick + "_",
			fmt.Sprintf("Server:       %s (%s:%d)", n.Name, n.Server, n.Port),
			"",
			"Editing servers isn't part of the mock.",
		}},
		optIRC: {Name: "IRC", Items: []optItem{
			{Label: "Show mode prefix", Bool: &a.modePrefix},
			{Label: "Timestamp events", Bool: &a.stamps},
		}},
		optDisplay: {Name: "Display", Items: []optItem{
			{Label: "Toolbar", Bool: &a.showToolbar},
			{Label: "Switchbar", Bool: &a.showSwitchbar},
			{Label: "Treebar", Bool: &a.showTreebar},
			{Label: "Nicklist", Bool: &a.showNicklist},
			{Label: "Line marker", Bool: &a.showMarker},
			{Label: "Line shading", Bool: &a.shading},
			{Label: "Nick colors", Bool: &a.nickColors},
			{Label: "Classic theme", Int: &a.themeIdx, Val: 0},
			{Label: "Dark theme", Int: &a.themeIdx, Val: 1},
		}},
		optBouncer: {Name: "Bouncer", Items: a.bouncerItems(), Notes: []string{
			"",
			"Your networks connect through the",
			"selected bouncer, which keeps you",
			"online while trout is closed.",
		}},
	}})
	if !a.opts.Demo { // bouncer management is a mock-up so far
		o := a.dlg.(*optionsBox)
		o.pages[optBouncer] = optPage{Name: "Bouncer", Notes: []string{
			"Connected to soju at " + a.opts.Addr,
			"as " + a.opts.User + ".",
			"",
			"Setting up and managing bouncers is",
			"in the demo only (trout --demo).",
		}}
	}
}

func (o *optionsBox) title() string { return "trout Options" }

func (o *optionsBox) size(a *App) (int, int) { return 64, 19 }

func (o *optionsBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	lx, ly, lw := x+2, y+2, 13
	c.Fill(lx, ly, lw, h-6, Style{BG: t.TextBG})
	for i, p := range o.pages {
		st := Style{FG: t.TextFG, BG: t.TextBG}
		switch {
		case i == o.page && o.onPages:
			st = Style{FG: t.SelFG, BG: t.SelBG}
		case i == o.page:
			st = face
		}
		c.Fill(lx, ly+i, lw, 1, st)
		c.Text(lx+1, ly+i, lw-1, p.Name, st)
		a.addRegion(Region{X: lx, Y: ly + i, W: lw, H: 1, Kind: HitDlg, S: "page", I: i})
	}

	px := lx + lw + 3
	pw := x + w - 2 - px
	p := o.pages[o.page]
	c.Text(px, ly, pw, p.Name, Style{FG: t.FaceText, BG: t.Face, Bold: true})
	row := ly + 2
	for i, it := range p.Items {
		box := "[ ]"
		switch {
		case it.Action != nil:
			box = "  »"
		case it.Bool != nil && *it.Bool:
			box = "[x]"
		case it.Int != nil && *it.Int == it.Val:
			box = "(•)"
		case it.Int != nil:
			box = "( )"
		}
		label := box + " " + it.Label
		st := face
		if i == o.sel && !o.onPages {
			st = Style{FG: t.MenuSelText, BG: t.MenuSel}
		}
		lwid := min(runewidth.StringWidth(label)+2, pw+1)
		c.Fill(px-1, row, lwid, 1, st)
		c.Text(px, row, pw, label, st)
		a.addRegion(Region{X: px - 1, Y: row, W: lwid, H: 1, Kind: HitDlg, S: "item", I: i})
		row++
	}
	for _, n := range p.Notes {
		c.Text(px, row, pw, n, face)
		row++
	}
	a.dlgButtons(c, x, y+h-3, w, []string{"OK"}, 0)
}

func (o *optionsBox) key(a *App, k string) tea.Cmd {
	items := o.pages[o.page].Items
	step := func(d int) {
		if o.onPages {
			o.page, o.sel = (o.page+d+len(o.pages))%len(o.pages), 0
		} else if len(items) > 0 {
			o.sel = (o.sel + d + len(items)) % len(items)
		}
	}
	switch k {
	case "tab", "shift+tab":
		o.onPages = !o.onPages
	case "left":
		o.onPages = true
	case "right":
		o.onPages = false
	case "up":
		step(-1)
	case "down":
		step(1)
	case " ":
		if !o.onPages && len(items) > 0 {
			items[o.sel].toggle(a)
		}
	case "enter", "esc":
		a.dlg = nil
	}
	return nil
}

func (o *optionsBox) mouse(a *App, m tea.MouseMsg, r Region, _ bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "page":
		o.page, o.sel, o.onPages = r.I, 0, true
	case "item":
		o.sel, o.onPages = r.I, false
		o.pages[o.page].Items[r.I].toggle(a)
	case "button", "close":
		a.dlg = nil
	}
	return nil
}

// openFavorites shows the Favorites folder (Alt+J) for the active network.
func (a *App) openFavorites() {
	n := a.active.Net
	names := demo.Favorites[n.Name] // real favorites aren't stored anywhere yet
	a.openDialog(&listBox{Title: "Favorites - " + n.Name, Header: "Channel", Rows: names, Keys: names,
		OnJoin: func(a *App, ch string) { a.join(n, ch) }})
}

// openChannelsList shows the Channels List (Alt+L) for the active network.
// For real it asks the server with LIST and fills in when the reply ends.
func (a *App) openChannelsList() {
	n := a.active.Net
	l := &listBox{Title: "Channels List - " + n.Name,
		Header: fmt.Sprintf("%-18s %5s  %s", "Channel", "Users", "Topic"),
		OnJoin: func(a *App, ch string) { a.join(n, ch) }}
	if a.opts.Demo {
		l.setChannels(demo.Channels[n.Name])
	} else if n != a.home && a.sendNet(n, "LIST") {
		l.Rows = []string{"Downloading the channel list..."}
		a.listing[n], a.listRows[n] = l, nil
	}
	a.openDialog(l)
}

func (l *listBox) setChannels(chans []demo.ChanInfo) {
	l.Rows, l.Keys = nil, nil
	for _, ch := range chans {
		l.Rows = append(l.Rows, fmt.Sprintf("%-18s %5d  %s", ch.Name, ch.Users, stripCodes(ch.Topic)))
		l.Keys = append(l.Keys, ch.Name)
	}
}

// join is joining from a dialog: for real, or a made-up channel in demo
// mode.
func (a *App) join(n *state.Network, ch string) {
	if a.opts.Demo {
		a.joinChannel(n, ch)
		return
	}
	a.joining[strings.ToLower(ch)] = true
	a.sendNet(n, "JOIN", ch)
}

func shortcutsBox() *msgBox {
	d := message(
		"Ctrl+N / Ctrl+Q      Next channel / next query",
		"Alt+1..9             Window N (as listed in the Window menu)",
		"Alt+Left / Right     Previous / next window",
		"Ctrl+W               Close window (Shift+click a switchbar button)",
		"PgUp / PgDn          Scroll        Ctrl+L  Jump to line marker",
		"Tab                  Complete nick; empty line: /msg last query",
		"Up / Down            Editbox history",
		"Ctrl+B Ctrl+U        Bold, underline",
		"Ctrl+R Ctrl+O        Reverse, reset",
		"Ctrl+K               Color palette",
		"Shift+Tab            Nicklist focus: arrows, Enter query, Space menu",
		"Alt+O Alt+L Alt+J    Options, Channels List, Favorites",
		"F2                   Classic / Dark      Ctrl+C  Quit",
		"",
		"Nick popup: right-click, Ctrl+click or press and hold a nick.",
		"Double-click a nick to query it, or a URL to open it.",
	)
	d.Title = "Keyboard Shortcuts"
	return d
}

func aboutBox() *msgBox {
	d := message(
		"trout",
		"Slapping people around with large trout since 1995.",
		"",
		"An mIRC-inspired IRC client for the terminal.",
		"This build is a UI mock-up: every message is fake.",
		"",
		"No trout were harmed in the making of this client.",
		"Several were used.",
	)
	d.Title = "About trout"
	return d
}
