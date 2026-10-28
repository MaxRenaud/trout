package ui

import (
	"fmt"

	"github.com/MaxRenaud/trout/internal/demo"
	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type MenuItem struct {
	Label    string
	Sep      bool
	Disabled bool
	Check    bool
	Key      string // shortcut shown right-aligned, like "Alt+O"
	Sub      []MenuItem
	Action   func(*App) tea.Cmd // nil items just close the menu
}

type openMenu struct {
	Items      []MenuItem
	X, Y, W, H int
	Sel        int // highlighted item, or -1
}

func (m *openMenu) contains(x, y int) bool {
	return x >= m.X && x < m.X+m.W && y >= m.Y && y < m.Y+m.H
}

func (m *openMenu) itemAt(x, y int) (int, bool) {
	i := y - m.Y - 1
	if x <= m.X || x >= m.X+m.W-1 || i < 0 || i >= len(m.Items) {
		return 0, false
	}
	return i, true
}

func (a *App) pushMenu(items []MenuItem, x, y int) {
	lw, kw := 0, 0
	for _, it := range items {
		lw = max(lw, runewidth.StringWidth(it.Label))
		kw = max(kw, runewidth.StringWidth(it.Key))
	}
	if kw > 0 {
		lw += kw + 3
	}
	m := openMenu{Items: items, W: lw + 8, H: len(items) + 2, Sel: -1}
	m.X = clamp(x, 0, a.w-m.W)
	m.Y = clamp(y, 0, a.h-m.H)
	a.menus = append(a.menus, m)
}

func (a *App) closeMenus() { a.menus, a.menuBar = nil, -1 }

func (a *App) openMenuBar(i int) {
	a.closeMenus()
	a.menuBar = i
	a.pushMenu(a.menuBarItems(i), a.menuBarX[i], 2)
}

// openSub closes everything below menus[level] and opens item i's submenu,
// to the right of its parent if it fits and to the left otherwise.
func (a *App) openSub(level, i int) {
	a.menus = a.menus[:level+1]
	p := a.menus[level]
	it := p.Items[i]
	if it.Sub == nil || it.Disabled {
		return
	}
	a.pushMenu(it.Sub, p.X+p.W, p.Y+i)
	if sub := &a.menus[len(a.menus)-1]; p.X+p.W+sub.W > a.w {
		sub.X = max(0, p.X-sub.W)
	}
}

func (a *App) activateItem(level, i int) tea.Cmd {
	it := a.menus[level].Items[i]
	switch {
	case it.Sep || it.Disabled:
		return nil
	case it.Sub != nil:
		a.menus[level].Sel = i
		a.openSub(level, i)
		a.menus[len(a.menus)-1].Sel = 0
		return nil
	}
	a.closeMenus()
	if it.Action != nil {
		return it.Action(a)
	}
	return nil
}

// menuMotion does hover highlighting and opens submenus under the pointer.
// Sliding along the menubar switches between its menus, like Windows.
func (a *App) menuMotion(x, y int) {
	for l := len(a.menus) - 1; l >= 0; l-- {
		m := &a.menus[l]
		if !m.contains(x, y) {
			continue
		}
		if i, ok := m.itemAt(x, y); ok && !m.Items[i].Sep && m.Sel != i {
			m.Sel = i
			a.openSub(l, i)
		}
		return
	}
	if r, ok := a.hit(x, y); ok && r.Kind == HitMenuBar && a.menuBar >= 0 && r.I != a.menuBar {
		a.openMenuBar(r.I)
	}
}

func (a *App) menuClick(x, y int) tea.Cmd {
	for l := len(a.menus) - 1; l >= 0; l-- {
		m := &a.menus[l]
		if !m.contains(x, y) {
			continue
		}
		if i, ok := m.itemAt(x, y); ok {
			return a.activateItem(l, i)
		}
		return nil
	}
	if r, ok := a.hit(x, y); ok && r.Kind == HitMenuBar && r.I != a.menuBar {
		a.openMenuBar(r.I)
		return nil
	}
	a.closeMenus()
	return nil
}

func (a *App) menuKey(k string) tea.Cmd {
	l := len(a.menus) - 1
	m := &a.menus[l]
	switch k {
	case "esc":
		if l > 0 {
			a.menus = a.menus[:l]
		} else {
			a.closeMenus()
		}
	case "up", "down":
		d, n := 1, len(m.Items)
		if k == "up" {
			d = -1
		}
		i := m.Sel
		if i < 0 && d < 0 {
			i = 0
		}
		for range n {
			i = (i + d + n) % n
			if !m.Items[i].Sep {
				break
			}
		}
		m.Sel = i
	case "right":
		if m.Sel >= 0 && m.Items[m.Sel].Sub != nil {
			return a.activateItem(l, m.Sel)
		}
		if a.menuBar >= 0 {
			a.openMenuBar((a.menuBar + 1) % len(menuBarNames))
			a.menus[0].Sel = 0
		}
	case "left":
		if l > 0 {
			a.menus = a.menus[:l]
		} else if a.menuBar >= 0 {
			a.openMenuBar((a.menuBar + len(menuBarNames) - 1) % len(menuBarNames))
			a.menus[0].Sel = 0
		}
	case "enter", " ":
		if m.Sel >= 0 {
			return a.activateItem(l, m.Sel)
		}
	}
	return nil
}

func (a *App) drawMenus(c *Canvas) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	hi, lo := Style{FG: t.Light, BG: t.Face}, Style{FG: t.Shadow, BG: t.Face}
	for mi := range a.menus {
		m := &a.menus[mi]
		a.raisedBox(c, m.X, m.Y, m.W, m.H)
		right := m.X + m.W - 1
		for i, it := range m.Items {
			y := m.Y + 1 + i
			if it.Sep {
				for x := m.X + 1; x < right; x++ {
					c.Set(x, y, '─', lo)
				}
				c.Set(m.X, y, '├', hi)
				c.Set(right, y, '┤', lo)
				continue
			}
			st := face
			if i == m.Sel {
				st = Style{FG: t.MenuSelText, BG: t.MenuSel}
			}
			if it.Disabled {
				st.FG = t.Disabled
			}
			c.Fill(m.X+1, y, m.W-2, 1, st)
			if it.Check {
				c.Set(m.X+2, y, '✓', st)
			}
			c.Text(m.X+4, y, m.W-8, it.Label, st)
			if it.Key != "" {
				kw := runewidth.StringWidth(it.Key)
				c.Text(right-2-kw, y, kw, it.Key, st)
			}
			if it.Sub != nil {
				c.Set(right-2, y, '►', st)
			}
		}
	}
}

func (a *App) menuBarItems(i int) []MenuItem {
	sep := MenuItem{Sep: true}
	toggle := func(p *bool) func(*App) tea.Cmd {
		return func(*App) tea.Cmd { *p = !*p; return nil }
	}
	setTheme := func(n int) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd { a.themeIdx = n; return nil }
	}
	switch i {
	case 0:
		return []MenuItem{{Label: "Connect"}, {Label: "Disconnect"}, {Label: "Select Server..."}, sep,
			{Label: "New Window", Sub: []MenuItem{{Label: "Server"}, {Label: "Channel..."}, {Label: "Query..."}}}, sep,
			{Label: "Save Settings"}, sep,
			{Label: "Exit", Action: func(*App) tea.Cmd { return tea.Quit }}}
	case 1:
		return []MenuItem{{Label: "Menubar", Check: true, Disabled: true},
			{Label: "Toolbar", Check: a.showToolbar, Action: toggle(&a.showToolbar)},
			{Label: "Switchbar", Check: a.showSwitchbar, Action: toggle(&a.showSwitchbar)},
			{Label: "Treebar", Check: a.showTreebar, Action: toggle(&a.showTreebar)},
			{Label: "Nicklist", Check: a.showNicklist, Action: toggle(&a.showNicklist)},
			{Label: "Timestamps", Check: a.stamps, Action: toggle(&a.stamps)}, sep,
			{Label: "Classic Theme", Check: a.themeIdx == 0, Action: setTheme(0)},
			{Label: "Dark Theme", Check: a.themeIdx == 1, Action: setTheme(1)}, sep,
			{Label: "Font..."}}
	case 2:
		n := a.active.Net
		items := []MenuItem{{Label: "Add to Favorites..."},
			{Label: "Organize Favorites...", Key: "Alt+J", Action: func(a *App) tea.Cmd { a.openFavorites(); return nil }}, sep}
		for _, ch := range demo.Favorites[n.Name] {
			items = append(items, MenuItem{Label: ch, Action: func(a *App) tea.Cmd { a.join(n, ch); return nil }})
		}
		return items
	case 3:
		items := []MenuItem{{Label: "Address Book..."}, {Label: "Aliases..."}, {Label: "Popups..."},
			{Label: "Scripts Editor..."}, sep,
			{Label: "Channels List...", Key: "Alt+L", Action: func(a *App) tea.Cmd { a.openChannelsList(); return nil }},
			{Label: "URLs List..."}, sep}
		if a.opts.Demo { // bouncer setup and management are mock-ups so far
			items = append(items,
				MenuItem{Label: "Set Up Bouncer...", Action: func(a *App) tea.Cmd { a.openSetup(); return nil }},
				MenuItem{Label: "Bouncers...", Action: func(a *App) tea.Cmd { a.openBouncers(); return nil }})
		}
		return append(items,
			MenuItem{Label: "Options...", Key: "Alt+O", Action: func(a *App) tea.Cmd { a.openOptions(optDisplay); return nil }})
	case 4:
		return []MenuItem{{Label: "Join Channel..."}, {Label: "Part Channel..."}, {Label: "Query User..."},
			{Label: "Send Notice..."}, {Label: "Whois User..."}, {Label: "Send CTCP..."}, sep,
			{Label: "Set Away..."}, {Label: "Change Nick..."}, sep, {Label: "Ignore User..."}, {Label: "Invite User..."}}
	case 5:
		items := []MenuItem{{Label: "Tile Horizontal", Disabled: true}, {Label: "Tile Vertical", Disabled: true},
			{Label: "Cascade", Disabled: true}, {Label: "Arrange Icons", Disabled: true}, sep}
		for n, w := range a.order() {
			label := w.Name
			if w.Kind == state.KStatus {
				label = "Status: " + w.Net.Name
			}
			items = append(items, MenuItem{Label: fmt.Sprintf("%d %s", n+1, label), Check: w == a.active,
				Action: func(a *App) tea.Cmd { a.activate(w); return nil }})
		}
		return items
	}
	return []MenuItem{{Label: "Help Topics"},
		{Label: "Keyboard Shortcuts", Key: "F1", Action: func(a *App) tea.Cmd { a.openDialog(shortcutsBox()); return nil }}, sep,
		{Label: "About trout", Action: func(a *App) tea.Cmd { a.openDialog(aboutBox()); return nil }}}
}

// windowMenu is the popup for a switchbar or treebar button.
func (a *App) windowMenu(w *state.Window, x, y int) {
	clear := MenuItem{Label: "Clear", Action: func(*App) tea.Cmd {
		w.Lines, w.Scroll, w.Marker = nil, 0, 0
		return nil
	}}
	closeAs := func(label string) MenuItem {
		return MenuItem{Label: label, Key: "Ctrl+W", Action: func(a *App) tea.Cmd { a.closeWindow(w); return nil }}
	}
	logging := MenuItem{Label: "Logging", Disabled: true}
	var items []MenuItem
	switch w.Kind {
	case state.KStatus:
		items = []MenuItem{clear, {Sep: true}, {Label: "Disconnect"}, logging}
	case state.KChannel:
		items = []MenuItem{clear, closeAs("Part"), {Sep: true}, {Label: "Channel Modes..."}, logging}
	default:
		items = []MenuItem{clear, closeAs("Close"), {Sep: true}, {Label: "Whois"}, logging}
	}
	a.closeMenus()
	a.pushMenu(items, x, y)
}

// nickMenu is mIRC's default nicklist popup. Only Query does anything.
func (a *App) nickMenu(win *state.Window, nick string, x, y int) {
	sub := func(labels ...string) []MenuItem {
		items := make([]MenuItem, len(labels))
		for i, l := range labels {
			items[i] = MenuItem{Label: l}
		}
		return items
	}
	query := func(a *App) tea.Cmd { a.openQuery(win.Net, nick); return nil }
	control := sub("Ignore", "Unignore", "Op", "Deop", "Voice", "Devoice",
		"Kick", "Kick (why)", "Ban", "Ban, Kick", "Ban, Kick (why)")
	whois, slap := MenuItem{Label: "Whois"}, MenuItem{Label: "Slap!"}
	if !a.opts.Demo {
		// The channel operator actions are real; "(why)" variants use a
		// stock reason until there's a prompt dialog.
		do := func(f func()) func(*App) tea.Cmd { return func(*App) tea.Cmd { f(); return nil } }
		mode := func(m, arg string) func(*App) tea.Cmd {
			return do(func() { a.sendTo(win, "MODE", win.Name, m, arg) })
		}
		kick := do(func() { a.sendTo(win, "KICK", win.Name, nick, "trout") })
		ban := mode("+b", nick+"!*@*")
		for i, act := range []func(*App) tea.Cmd{nil, nil, mode("+o", nick), mode("-o", nick),
			mode("+v", nick), mode("-v", nick), kick, kick, ban,
			do(func() { ban(a); kick(a) }), do(func() { ban(a); kick(a) })} {
			control[i].Action = act
		}
		whois.Action = do(func() { a.sendTo(win, "WHOIS", nick) })
		slap.Action = do(func() { a.action(win, "slaps "+nick+" around a bit with a large trout") })
	}
	a.closeMenus()
	a.pushMenu([]MenuItem{
		{Label: "Info"}, whois, {Label: "Query", Action: query}, {Sep: true},
		{Label: "Control", Sub: control},
		{Label: "CTCP", Sub: sub("Ping", "Time", "Version")},
		{Label: "DCC", Sub: sub("Send", "Chat")},
		{Sep: true},
		slap,
	}, x, y)
}
