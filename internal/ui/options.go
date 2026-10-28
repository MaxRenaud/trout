package ui

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	optMessages
	optDisplay
	optBouncer
	optDCC
	optOther
)

func (a *App) openOptions(page int) {
	n := a.active.Net
	a.openDialog(&optionsBox{page: page, pages: []optPage{
		optConnect: a.connectPage(n),
		optIRC: {Name: "IRC", Items: []optItem{
			{Label: "Show mode prefix", Bool: &a.modePrefix},
			{Label: "Timestamps", Bool: &a.stamps},
			{Label: "Timestamp format: " + a.stampFormat + "...", Action: (*App).openStampFormat},
			{Label: "Highlight words...", Action: (*App).openHighlight},
			{Label: "Beep on highlights and private messages", Bool: &a.beep},
			{Label: "Show notices in the active window", Bool: &a.noticesActive},
			{Label: "Rejoin channel when kicked", Bool: &a.rejoinOnKick},
			{Label: "Join channels when invited", Bool: &a.joinOnInvite},
			{Label: "Whois when someone opens a query", Bool: &a.whoisOnQuery},
		}},
		optMessages: {Name: "Messages", Items: []optItem{
			{Label: "Strip colors and codes from messages", Bool: &a.stripCodes},
			{Label: "Prefix own messages with your nick", Bool: &a.prefixOwn},
			{Label: "Keep channel windows open on part", Bool: &a.keepOpenOnPart},
			{Label: "Show CTCP requests in the active window", Bool: &a.ctcpActive},
			{Label: "Come back from away when you talk", Bool: &a.awayCancel},
			{Label: "Invisible mode (+i)", Bool: &a.invisible},
			{Label: "Pop up Favorites when connecting", Bool: &a.popFavorites},
			{Label: "Show user addresses on joins and parts", Bool: &a.showAddress},
			{Label: "List the nicks when joining a channel", Bool: &a.nicksOnJoin},
			{Label: "Show notify messages in the active window", Bool: &a.notifyActive},
			{Label: "Private messages in their own windows", Int: &a.privateTo, Val: 0},
			{Label: "Private messages in one Messages window", Int: &a.privateTo, Val: 1},
			{Label: "Private messages in the active window", Int: &a.privateTo, Val: 2},
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
			{Label: "Colors...", Action: (*App).openColors},
			{Label: "Sort switchbar buttons", Bool: &a.sortSwitch},
			{Label: "Fill the switchbar", Bool: &a.fillSwitch},
			{Label: "Highlight the active window's button too", Bool: &a.hiActive},
		}},
		optBouncer: {Name: "Bouncer", Items: a.bouncerItems(), Notes: []string{
			"",
			"Your networks connect through the",
			"selected bouncer, which keeps you",
			"online while trout is closed.",
		}},
		optDCC: {Name: "DCC", Items: []optItem{
			{Label: "Download folder: " + a.downloadLabel() + "...", Action: (*App).openDownloadDir},
			{Label: "Ask before getting a file", Int: &a.dccGet, Val: 0},
			{Label: "Get files without asking", Int: &a.dccGet, Val: 1},
			{Label: "Ignore all file offers", Int: &a.dccGet, Val: 2},
			{Label: "Get files I asked for (XDCC)", Bool: &a.dccAutoAsked},
			{Label: "Close Get windows when done", Bool: &a.dccClose},
			{Label: "Ask before chatting", Int: &a.dccChat, Val: 0},
			{Label: "Accept chats without asking", Int: &a.dccChat, Val: 1},
			{Label: "Ignore all chat offers", Int: &a.dccChat, Val: 2},
			{Label: "Show the privacy notice first", Bool: &a.dccWarn},
			{Label: "Trusted users...", Action: (*App).openDCCTrusted},
			{Label: "Folders by file type...", Action: (*App).openDCCFolders},
			{Label: truncate(a.dccIgnoreLabel(), 40) + "...", Action: (*App).openDCCIgnore},
			{Label: fmt.Sprintf("Time-out: %d seconds...", a.dccTimeoutS), Action: (*App).openDCCTimeout},
		}},
		optOther: {Name: "Other", Items: []optItem{
			{Label: "Confirm pasting several lines", Bool: &a.confirmPaste},
			{Label: "Confirm before quitting", Bool: &a.confirmExit},
			{Label: "Command prefix: " + a.cmdPrefix + "...", Action: (*App).openCmdPrefix},
			{Label: "After a Tab-completed nick: \"" + a.tabSuffix + "\"...", Action: (*App).openTabSuffix},
			{Label: fmt.Sprintf("Window buffer: %d lines...", state.MaxLines), Action: (*App).openBuffer},
		}},
	}})
	o := a.dlg.(*optionsBox)
	if a.term.Name != "" { // a Mac terminal whose Option key matters
		o.pages[optOther].Items = append(o.pages[optOther].Items,
			optItem{Label: "Option key as Alt (" + a.term.Name + ")...", Action: (*App).openOptionKey})
	}
	if !a.opts.Demo { // setting up and managing bouncers is a mock-up so far
		notes := []string{"", "Address: " + a.bncAddress(), "User:    " + a.bnc.User}
		if a.bnc.Pin != "" {
			notes = append(notes, "Trusted: "+a.bnc.Pin)
		}
		notes = append(notes, "", "Setting up new bouncers is in the", "demo only (trout --demo).")
		o.pages[optBouncer] = optPage{Name: "Bouncer", Notes: notes, Items: []optItem{
			{Label: "Manage bouncers...", Action: (*App).openSavedBouncers},
		}}
	}
}

// connectPage is Options > Connect, like mIRC's: your nickname and real
// name, which the bouncer uses on every network (a network can have its
// own in Select Server > Edit, with its username), and where to change
// servers and bouncers.
func (a *App) connectPage(n *state.Network) optPage {
	notes := []string{
		"Network:   " + n.Name,
		fmt.Sprintf("Server:    %s:%d", n.Server, n.Port),
		"Nickname:  " + n.Nick,
	}
	if a.opts.Demo {
		return optPage{Name: "Connect", Notes: notes}
	}
	id := n // whose names to show: a network's, even from the bouncer's window
	if n == a.home && len(a.nets) > 1 {
		id = a.nets[1]
	}
	nick, real := cmp.Or(id.Nick, id.Attrs["nickname"]), id.Attrs["realname"]
	return optPage{Name: "Connect", Items: []optItem{
		{Label: "Nickname: " + nick + "...", Action: func(a *App) { a.openIdentity("Nickname", "-nick", nick) }},
		{Label: "Real name: " + cmp.Or(real, "(none)") + "...", Action: func(a *App) { a.openIdentity("Real name", "-realname", real) }},
		{Label: "Servers, and a network's own names...", Action: (*App).openServers},
		{Label: "Perform on connect (" + id.Name + ")...", Action: func(a *App) { a.openPerform(id) }},
		{Label: a.proxyLabel(), Action: (*App).openProxy},
		{Label: "Bouncers...", Action: (*App).openSavedBouncers},
	}, Notes: []string{"", "Bouncer:   " + a.bncAddress() + " as " + a.bnc.User,
		"Network:   " + id.Name}}
}

// openIdentity asks for your nickname or real name for every network,
// which the bouncer keeps (BouncerServ user update), then shows Connect
// again.
func (a *App) openIdentity(what, flag, value string) {
	a.openDialog(&formBox{Title: what, Buttons: []string{"OK", "Cancel"},
		Lines:  []string{"Used on every network, except one with its own", "(Select Server > Edit)."},
		Fields: []*formField{newField(what+":", value, false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			val := strings.TrimSpace(v[0])
			if val == "" {
				return nil, errors.New("it can't be empty")
			}
			if a.control == nil {
				return nil, errors.New("not connected to the bouncer")
			}
			a.serv(a.home, a.home.Status(), false, "user", "update", flag, val)
			// The bouncer sends NICK or SETNAME to the networks (or
			// reconnects), but doesn't tell clients the new defaults: note
			// them on the networks that used the old one.
			key := map[string]string{"-nick": "nickname", "-realname": "realname"}[flag]
			for _, n := range a.nets[1:] {
				if n.Attrs != nil && (n.Attrs[key] == value || n.Attrs[key] == "") {
					n.Attrs[key] = val
				}
			}
			a.openOptions(optConnect)
			return nil, nil
		},
		OnCancel: func(a *App) tea.Cmd { a.openOptions(optConnect); return nil }})
}

// openStampFormat asks for the timestamp format, in mIRC's notation.
func (a *App) openStampFormat() {
	a.openDialog(&formBox{Title: "Timestamp Format", Buttons: []string{"OK", "Cancel"},
		Lines: []string{"HH:nn is 24-hour time, hh:nn tt 12-hour,",
			"ss seconds, dd/mm/yy the date, ddd the weekday."},
		Fields: []*formField{newField("Format:", a.stampFormat, false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			a.stampFormat = cmp.Or(strings.TrimSpace(v[0]), "[HH:nn]")
			a.openOptions(optIRC)
			return nil, nil
		},
		OnCancel: func(a *App) tea.Cmd { a.openOptions(optIRC); return nil }})
}

// downloadLabel is the download folder, ~ for home.
func (a *App) downloadLabel() string {
	dir, _ := a.downloads()
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(dir, home); ok {
			dir = "~" + rest
		}
	}
	return dir
}

// openDownloadDir asks where DCC files go, like mIRC's DCC > Folders.
func (a *App) openDownloadDir() {
	a.openDialog(&formBox{Title: "Download Folder", Buttons: []string{"OK", "Cancel"},
		Lines:  []string{"Where files people send you go. Empty for ~/Downloads."},
		Fields: []*formField{newField("Folder:", a.downloadLabel(), false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			dir := strings.TrimSpace(v[0])
			if home, err := os.UserHomeDir(); err == nil {
				if rest, ok := strings.CutPrefix(dir, "~"); ok {
					dir = filepath.Join(home, rest)
				}
				if dir == filepath.Join(home, "Downloads") {
					dir = "" // the default, which follows a new home
				}
			}
			if dir != "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return nil, err
				}
				if err := os.MkdirAll(abs, 0o755); err != nil {
					return nil, fmt.Errorf("can't use that folder: %v", err)
				}
				dir = abs
			}
			a.downloadDir = dir
			a.openOptions(optDCC)
			return nil, nil
		},
		OnCancel: func(a *App) tea.Cmd { a.openOptions(optDCC); return nil }})
}

func (o *optionsBox) title() string { return "trout Options" }

// size fits the longest page: its items and notes, plus the frame.
func (o *optionsBox) size(a *App) (int, int) {
	rows := len(o.pages)
	for _, p := range o.pages {
		rows = max(rows, len(p.Items)+len(p.Notes))
	}
	return 64, max(19, rows+8)
}

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
func (a *App) openFavorites() { a.openFavoritesFor(a.active.Net) }

// openFavoritesFor shows network n's Favorites folder, with mIRC's Add.
func (a *App) openFavoritesFor(n *state.Network) {
	names := slices.Clone(a.favorites(n))
	join := joinList(func(a *App, ch string) { a.join(n, ch) })
	l := &listBox{Title: "Favorites - " + n.Name, Header: "Channel", Rows: names, Keys: slices.Clone(names), OnPress: join}
	if !a.opts.Demo {
		l.Buttons = []string{"Join", "Add...", "Remove", "Close"}
		l.OnPress = func(a *App, l *listBox, button, ch string) tea.Cmd {
			if button == "Add..." {
				a.openDialog(a.prompt("Add to Favorites", "A channel on "+n.Name+".", "Channel:", "#",
					func(a *App, v string) {
						if !n.IsChannel(v) {
							v = "#" + v
						}
						a.setFavorite(n, v, true)
						a.openFavoritesFor(n)
					}))
				return nil
			}
			if button == "Remove" {
				if ch != "" {
					a.setFavorite(n, ch, false)
					l.removeSel()
				}
				return nil
			}
			return join(a, l, button, ch)
		}
		if len(names) == 0 {
			l.Rows = []string{"No favorites yet: Favorites > Add to Favorites adds a channel you're in."}
		}
	}
	a.openDialog(l)
}

// favorites are network n's favorite channels: config.toml's, or the
// demo's made-up ones.
func (a *App) favorites(n *state.Network) []string {
	if a.opts.Demo {
		return demo.Favorites[n.Name]
	}
	return a.netList(listFavorites, n)
}

// setFavorite adds or removes channel ch from n's favorites and saves
// them.
func (a *App) setFavorite(n *state.Network, ch string, on bool) {
	a.setInList(listFavorites, n, ch, on)
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
		"Ctrl+W               Close window (or its × on the switchbar, Shift+click)",
		"PgUp / PgDn          Scroll        Ctrl+L  Jump to line marker",
		"Tab                  Complete nick (*wild*); empty line: /msg last query",
		"Up / Down            Editbox history (Ctrl+Up / Down in several lines)",
		"Alt+Enter, Ctrl+J    New line in the editbox; Enter sends every line",
		"Alt+Q                Second editbox; Alt+Up / Down moves between them",
		"Shift+arrows         Select; Ctrl+C copy, Ctrl+X cut, Ctrl+V paste",
		"Ctrl+B U E R O       Bold, underline, strike, reverse, reset (wrap a selection)",
		"Ctrl+K               Color palette",
		"Shift+Tab            Nicklist focus: arrows, Enter query, Space menu",
		"Alt+O Alt+L Alt+J    Options, Channels List, Favorites",
		"Alt+B Alt+N Alt+U    Address Book, Notify, URLs List   Alt+K  Colors",
		"Ctrl+F  F3 Shift+F3  Find text: newest first, older, newer",
		"F2                   Classic / Dark      /exit   Quit",
		"",
		"Nick popup: right-click, Ctrl+click or press and hold a nick.",
		"Double-click a nick to query it, or a URL to open it.",
		"",
		"On a Mac, Option is Alt once the terminal sends it as Alt: Terminal's Use",
		"Option as Meta key, iTerm2's Esc+, Ghostty's macos-option-as-alt.",
	)
	d.Title = "Keyboard Shortcuts"
	return d
}

func aboutBox(demo bool) *msgBox {
	d := message(
		"trout",
		"Slapping people around with large trout since 1995.",
		"",
		"An mIRC-inspired IRC client for the terminal.",
	)
	if demo {
		d.Lines = append(d.Lines, "This is the demo: every message is fake.")
	}
	d.Lines = append(d.Lines, "",
		"No trout were harmed in the making of this client.",
		"Some were slapped around a bit.", "",
		"The bouncer trout sets up and runs is soju (AGPL-3.0):",
		"https://codeberg.org/emersion/soju")
	d.Title = "About trout"
	return d
}

// openCmdPrefix, openTabSuffix and openBuffer are Options > Other's
// text settings; each goes back to Other.
func (a *App) openCmdPrefix() {
	a.otherPrompt("Command Prefix", "What starts a command, usually /.", "Prefix:", a.cmdPrefix,
		func(v string) error {
			if v == "" {
				return errors.New("it can't be empty")
			}
			a.cmdPrefix = v
			return nil
		})
}

func (a *App) openTabSuffix() {
	a.otherPrompt("Tab Completion", "What Tab adds after a nick at the start of a line, like \": \".", "Suffix:", a.tabSuffix,
		func(v string) error { a.tabSuffix = v; return nil })
}

func (a *App) openBuffer() {
	a.otherPrompt("Window Buffer", "How many lines each window keeps (100 or more).", "Lines:", strconv.Itoa(state.MaxLines),
		func(v string) error {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < 100 {
				return errors.New("give a number, 100 or more")
			}
			state.MaxLines = n
			return nil
		})
}

func (a *App) otherPrompt(title, line, label, value string, set func(string) error) {
	a.openDialog(&formBox{Title: title, Lines: []string{line}, Buttons: []string{"OK", "Cancel"},
		Fields: []*formField{newField(label, value, false)},
		OnOK: func(a *App, v []string) (tea.Cmd, error) {
			if err := set(v[0]); err != nil {
				return nil, err
			}
			a.openOptions(optOther)
			return nil, nil
		},
		OnCancel: func(a *App) tea.Cmd { a.openOptions(optOther); return nil }})
}
