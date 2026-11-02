package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/script"
	"github.com/MaxRenaud/trout/internal/state"
)

// Custom windows: scripts' @windows (/window, /aline...), kept here as
// state.KCustom windows so the switchbar, scrolling, Find and copying work
// on them like any window. The engine changes them through scriptHost as a
// script runs (script.Windows); this is that side, and how they're drawn
// and clicked: a text window like a query's, or a listbox, with an editbox
// only if the script asked for one (-e), and a side listbox (-lN).

// customWin is a custom window as the engine sees it.
type customWin struct {
	a *App
	w *state.Window
}

func (c customWin) Lines(side bool) []script.WinLine {
	src := c.w.Lines
	if side {
		src = c.w.Custom.Side
	}
	out := make([]script.WinLine, len(src))
	for i, l := range src {
		out[i] = script.WinLine{Text: l.Text, Color: l.Color - 1, Sel: l.Sel}
	}
	return out
}

func (c customWin) SetLines(side bool, lines []script.WinLine) {
	now := time.Now()
	out := make([]state.Line, len(lines))
	for i, l := range lines {
		out[i] = state.Line{Time: now, Role: state.RoleNormal, Text: l.Text, Sel: l.Sel}
		if l.Color >= 0 {
			out[i].Color = l.Color%16 + 1
		}
	}
	if n := len(out) - state.MaxLines; n > 0 {
		out = out[n:]
	}
	if side {
		c.w.Custom.Side = out
		return
	}
	c.w.Lines = out
	c.w.Marker = min(c.w.Marker, len(out))
}

func (c customWin) Opts() script.WinOpts {
	cu := c.w.Custom
	return script.WinOpts{Network: c.w.Net.Name, Listbox: cu.Listbox, Side: cu.SideW, Editbox: cu.Editbox,
		Sort: cu.Sort, SortSide: cu.SortSide, HidePrefix: cu.HidePrefix, Hidden: cu.Hidden, NoButton: cu.NoButton,
		Minimized: cu.Minimized, Title: cu.Title, Command: cu.Command, Popup: cu.Popup, Buffer: cu.Buffer,
		Highlight: cu.Highlight, AnyConn: cu.AnyConn}
}

func (c customWin) SetOpts(o script.WinOpts) {
	cu := c.w.Custom
	*cu = state.Custom{Listbox: o.Listbox, SideW: o.Side, Side: cu.Side, SideTop: cu.SideTop, Editbox: o.Editbox,
		Sort: o.Sort, SortSide: o.SortSide, HidePrefix: o.HidePrefix, Hidden: o.Hidden, NoButton: o.NoButton,
		Minimized: o.Minimized, Title: o.Title, Command: o.Command, Popup: o.Popup, Buffer: o.Buffer,
		Highlight: o.Highlight, AnyConn: o.AnyConn}
	a := c.a
	if c.w != a.active {
		// -g, or -h on a line added while it's minimized: its button's
		// color, like mIRC's.
		switch o.Highlight {
		case 1:
			c.w.Act = max(c.w.Act, state.ActMsg)
		case 2:
			c.w.Act = state.ActHighlight
		}
	} else if o.Hidden {
		a.activate(a.prevWindow(c.w))
	}
}

// prevWindow is the window before w, to go to when w goes away.
func (a *App) prevWindow(w *state.Window) *state.Window {
	order := a.order()
	if i := slices.Index(order, w); i > 0 {
		return order[i-1]
	}
	return w.Net.Status()
}

// customWins is every custom window, in the order they opened (by
// network).
func (a *App) customWins() []*state.Window {
	var out []*state.Window
	for _, n := range a.nets {
		for _, w := range n.Wins {
			if w.Kind == state.KCustom {
				out = append(out, w)
			}
		}
	}
	return out
}

func (a *App) customWin(name string) *state.Window {
	for _, w := range a.customWins() {
		if strings.EqualFold(w.Name, name) {
			return w
		}
	}
	return nil
}

func (h scriptHost) CustomWindows() []string {
	var names []string
	for _, w := range h.a.customWins() {
		names = append(names, w.Name)
	}
	return names
}

func (h scriptHost) CustomWindow(name string) script.Window {
	if w := h.a.customWin(name); w != nil {
		return customWin{h.a, w}
	}
	return nil
}

func (h scriptHost) OpenWindow(name string, o script.WinOpts) script.Window {
	n := h.net(o.Network)
	if n == nil {
		n = h.a.active.Net
	}
	w := n.Add(&state.Window{Kind: state.KCustom, Name: name, Custom: &state.Custom{}})
	c := customWin{h.a, w}
	c.SetOpts(o)
	return c
}

// CloseWindow is a script closing one (/window -c): no on CLOSE, like
// mIRC's.
func (h scriptHost) CloseWindow(name string) {
	if w := h.a.customWin(name); w != nil {
		h.a.removeWindow(w)
	}
}

func (h scriptHost) ActivateWindow(name string) {
	if w := h.a.customWin(name); w != nil {
		w.Custom.Minimized, w.Custom.Hidden = false, false
		h.a.activate(w)
	}
}

func (h scriptHost) RenameWindow(old, name string) {
	if w := h.a.customWin(old); w != nil {
		w.Name = name
	}
}

// onBar is whether a window has a switchbar and treebar button: not a
// custom window opened hidden (-h) or without one (-w0).
func onBar(w *state.Window) bool {
	return w.Custom == nil || !w.Custom.Hidden && !w.Custom.NoButton
}

// barName is a window's button: its name, without the @ for a custom
// window opened with -k0.
func barName(w *state.Window) string {
	if w.Kind == state.KStatus {
		return w.Net.Name
	}
	if w.Custom != nil && w.Custom.HidePrefix {
		return strings.TrimPrefix(w.Name, "@")
	}
	return w.Name
}

// customTitle is a custom window's title bar: its name, then /renwin's
// topic.
func customTitle(w *state.Window) string {
	return strings.TrimSpace(w.Name + " " + w.Custom.Title)
}

// drawCustom draws a custom window: its text or listbox, a side listbox
// on the right (-lN), and an editbox only if it has one (-e).
func (a *App) drawCustom(c *Canvas, win *state.Window, x, y, w, h int) {
	t := a.theme()
	cu := win.Custom
	ph := h
	eh := 0
	if cu.Editbox {
		eh = editHeight(&win.Edit, w)
		ph = max(1, h-1-eh)
	}
	right := x + w
	if cu.SideW > 0 {
		sw := clamp(cu.SideW+2, 6, w/2)
		a.drawCustomList(c, win, true, right-sw, y, sw, ph)
		for j := y; j < y+ph; j++ {
			c.Set(right-sw-1, j, ' ', Style{BG: t.Face})
		}
		right -= sw + 1
	}
	if cu.Listbox {
		a.drawCustomList(c, win, false, x, y, right-x, ph)
		a.msgW, a.msgH = right-x-2, ph
	} else {
		a.drawMessages(c, win, x, y, right-x, ph)
	}
	if !cu.Editbox {
		return
	}
	sep := Style{FG: t.Lines, BG: t.TextBG}
	for i := x; i < x+w; i++ {
		c.Set(i, y+ph, '─', sep)
	}
	cx, cy := a.drawWinEdit(c, &win.Edit, 1, x, y+ph+1, w, eh, true)
	a.caretX, a.caretY = cx, cy
}

// drawCustomList draws a listbox: a line a row, cut off rather than
// wrapped, the selected ones highlighted.
func (a *App) drawCustomList(c *Canvas, win *state.Window, side bool, x, y, w, h int) {
	t := a.theme()
	lines, top, kind, s := win.Lines, &win.Scroll, HitCustomScroll, ""
	if side {
		lines, top, kind, s = win.Custom.Side, &win.Custom.SideTop, HitSideScroll, "side"
	}
	n := len(lines)
	lw := w
	if n > h {
		lw = w - 1
	}
	c.Fill(x, y, w, h, Style{FG: t.TextFG, BG: t.TextBG})
	a.addRegion(Region{X: x, Y: y, W: lw, H: h, Kind: HitCustom, I: -1, S: s, Win: win}) // empty space
	*top = clamp(*top, 0, n-h)
	for i := 0; i < h && *top+i < n; i++ {
		l, ry := lines[*top+i], y+i
		base := Style{FG: t.TextFG, BG: t.TextBG}
		if l.Color > 0 {
			base.FG = t.Pal[(l.Color-1)%16]
		}
		cx := x + 1
		for _, sr := range parseMIRC(l.Text, base, &t.Pal) {
			if cx >= x+lw {
				break
			}
			cx += c.Rune(cx, ry, x+lw-cx, sr.R, sr.St)
		}
		if l.Sel {
			for sx := x; sx < x+lw; sx++ {
				c.Restyle(sx, ry, t.SelFG, t.SelBG)
			}
		}
		a.addRegion(Region{X: x, Y: ry, W: lw, H: 1, Kind: HitCustom, I: *top + i, S: s, Win: win})
	}
	if n > h {
		a.drawScrollbar(c, x+w-1, y, h, n, h, *top, kind)
	}
}

// customLines is a custom window's listbox lines: its own or the side
// listbox's.
func customLines(w *state.Window, side bool) []state.Line {
	if side {
		return w.Custom.Side
	}
	return w.Lines
}

// customEvent runs a custom window's mouse event from its menu (sclick,
// dclick, lbclick, rclick), with the line number as $1.
func (a *App) customEvent(w *state.Window, event string, line int) tea.Cmd {
	if a.scripts == nil {
		return nil
	}
	var params []string
	if line > 0 {
		params = []string{strconv.Itoa(line)}
	}
	return a.runScript(a.scripts.WindowEvent(event, w.Net.Name, w.Name, params, a.scriptHost()))
}

// customClick is a click in a custom window's listbox: it selects the
// line, then runs the window's sclick and lbclick, or dclick, or rclick
// and its popup.
func (a *App) customClick(w *state.Window, r Region, double, right bool, x, y int) tea.Cmd {
	lines := customLines(w, r.S == "side")
	if r.I >= 0 && r.I < len(lines) && !(right && lines[r.I].Sel) {
		for i := range lines {
			lines[i].Sel = i == r.I
		}
	}
	n := r.I + 1
	switch {
	case right:
		cmd := a.customEvent(w, "rclick", n)
		a.customMenu(w, n, x, y)
		return cmd
	case double:
		return a.customEvent(w, "dclick", n)
	}
	cmds := []tea.Cmd{a.customEvent(w, "sclick", n)}
	if r.I >= 0 {
		cmds = append(cmds, a.customEvent(w, "lbclick", n))
	}
	return tea.Batch(cmds...)
}

// customTextClick is a double click or right click on a custom text
// window's line: dclick, or rclick and the popup, with its line number.
func (a *App) customTextClick(w *state.Window, double bool, x, y int) tea.Cmd {
	n := 0
	rows, _ := a.windowRows(w, a.msgW)
	if i := a.msgTop + y - a.msgY; i >= 0 && i < len(rows) {
		n = rows[i].line + 1
	}
	if double {
		return a.customEvent(w, "dclick", n)
	}
	cmd := a.customEvent(w, "rclick", n)
	a.customMenu(w, n, x, y)
	return cmd
}

// customMenu is a custom window's popup: its menu sections (menu @name),
// with the line number as $1.
func (a *App) customMenu(w *state.Window, line, x, y int) {
	var params []string
	if line > 0 {
		params = []string{strconv.Itoa(line)}
	}
	a.closeMenus()
	if items := a.scriptMenu(w.Name, w, params); len(items) > 0 {
		a.pushMenu(items, x, y)
	}
}

// customKey is a key in a custom window without an editbox: a listbox's
// Up and Down select (lbclick), Enter is a double click (dclick).
func (a *App) customKey(w *state.Window, s string) (tea.Cmd, bool) {
	cu := w.Custom
	if cu.Editbox {
		return nil, false
	}
	if !cu.Listbox {
		switch s {
		case "up", "down", "pgup", "pgdown", "home", "end", "enter":
			return nil, true // nothing to type in
		}
		return nil, false
	}
	sel := slices.IndexFunc(w.Lines, func(l state.Line) bool { return l.Sel })
	move := func(d int) tea.Cmd {
		if len(w.Lines) == 0 {
			return nil
		}
		i := clamp(sel+d, 0, len(w.Lines)-1)
		if sel < 0 {
			i = 0
		}
		for j := range w.Lines {
			w.Lines[j].Sel = j == i
		}
		w.Scroll = clamp(w.Scroll, i-a.msgH+1, i) // keep it in view
		return a.customEvent(w, "lbclick", i+1)
	}
	switch s {
	case "up":
		return move(-1), true
	case "down":
		return move(1), true
	case "pgup":
		return move(-max(1, a.msgH-1)), true
	case "pgdown":
		return move(max(1, a.msgH-1)), true
	case "home":
		return move(-len(w.Lines)), true
	case "end":
		return move(len(w.Lines)), true
	case "enter":
		if sel >= 0 {
			return a.customEvent(w, "dclick", sel+1), true
		}
		return nil, true
	}
	return nil, false
}
