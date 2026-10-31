package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/MaxRenaud/trout/internal/state"

	"github.com/mattn/go-runewidth"
)

type HitKind int

const (
	HitNone HitKind = iota
	HitCaption
	HitChildBtn
	HitMenuBar
	HitTool
	HitSwitch
	HitTree
	HitTreeToggle
	HitTreeSplit
	HitNickSplit
	HitMsg
	HitMsgScroll
	HitNick
	HitNickScroll
	HitEdit
	HitDlg // anything inside a dialog; S says what
	HitSwatch
	HitURL
	HitTransfer // a Get window's Cancel or Open Folder button
	HitNickText // a nick in a channel's text: S is the nick
)

// Region is a clickable area recorded while drawing. Later regions sit on
// top of earlier ones. For scrollbars, I is -1/1 for the arrows and -2/2
// for the track above/below the thumb.
type Region struct {
	X, Y, W, H int
	Kind       HitKind
	I          int
	S          string
	Win        *state.Window
	Net        *state.Network
}

var menuBarNames = [...]string{"File", "View", "Favorites", "Tools", "Commands", "Window", "Help"}

// menuMnemonics holds each menubar entry's Alt key, which is also the
// letter drawn underlined ("F&avorites").
const menuMnemonics = "fvatcwh"

// An empty label is a separator.
var toolButtons = []string{"Connect", "Options", "", "Favorites", "Channels", "Scripts", "",
	"Address Book", "Notify", "", "Send File", "Received", "", "Colors"}

type textRow struct {
	line   int // index into the window's Lines
	runes  []SRune
	indent int
	shade  bool // line shading
	marker bool // the line marker, not text
	cont   bool // a wrapped continuation of the row before
}

func (a *App) addRegion(r Region) { a.regions = append(a.regions, r) }

func (a *App) hit(x, y int) (Region, bool) {
	for i := len(a.regions) - 1; i >= 0; i-- {
		r := a.regions[i]
		if x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H {
			return r, true
		}
	}
	return Region{}, false
}

func (a *App) View() string {
	if a.w < 60 || a.h < 16 {
		return "Terminal too small for trout (need at least 60x16)."
	}
	t := a.theme()
	c := NewCanvas(a.w, a.h, Style{FG: t.TextFG, BG: t.TextBG})
	a.regions = a.regions[:0]
	clear(a.sbars)
	y := 0
	a.drawTitle(c, y)
	y++
	a.drawMenuBar(c, y)
	y++
	if a.showToolbar {
		a.drawToolbar(c, y)
		y++
	}
	if a.showSwitchbar {
		y += a.drawSwitchbar(c, y)
	}
	x := 0
	if a.showTreebar {
		a.treeW = clamp(a.treeW, 10, a.w/3)
		a.drawTreebar(c, 0, y, a.treeW, a.h-y)
		x = a.treeW + 1
	}
	a.drawWindow(c, a.active, x, y, a.w-x, a.h-y)
	a.drawPalette(c)
	a.drawModal(c)
	a.drawMenus(c) // over the dialog: the Scripts Editor has its own menubar
	return c.String()
}

func (a *App) drawTitle(c *Canvas, y int) {
	t := a.theme()
	for x := range a.w {
		c.Set(x, y, ' ', Style{BG: lerp(t.TitleA, t.TitleB, float64(x)/float64(a.w-1))})
	}
	c.TextOver(1, y, a.w-7, "trout - ["+a.winTitle(a.active)+"]", t.TitleText, true)
	// Just a close box: a terminal app can't minimize or maximize itself.
	btn := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	c.Fill(a.w-5, y, 3, 1, btn)
	c.Set(a.w-4, y, '×', btn)
	a.addRegion(Region{X: a.w - 5, Y: y, W: 3, H: 1, Kind: HitCaption})
}

func (a *App) drawMenuBar(c *Canvas, y int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	c.Fill(0, y, a.w, 1, face)
	x := 1
	for i, name := range menuBarNames {
		st := face
		if a.menuBar == i && len(a.menus) > 0 && !a.dlgMenu {
			st = Style{FG: t.MenuSelText, BG: t.MenuSel}
		}
		w := len(name) + 2
		c.Fill(x, y, w, 1, st)
		mn := strings.IndexByte(strings.ToLower(name), menuMnemonics[i])
		for j, r := range name {
			s := st
			s.Underline = j == mn
			c.Set(x+1+j, y, r, s)
		}
		a.menuBarX[i] = x
		a.addRegion(Region{X: x, Y: y, W: w, H: 1, Kind: HitMenuBar, I: i})
		x += w
	}
}

// raisedBox draws a filled Win98-style raised frame with a drop shadow, the
// base of menus, dialogs and the color picker.
func (a *App) raisedBox(c *Canvas, x, y, w, h int) {
	t := a.theme()
	for j := y + 1; j <= y+h; j++ {
		c.Dim(x+w, j)
	}
	for i := x + 1; i < x+w; i++ {
		c.Dim(i, y+h)
	}
	c.Fill(x, y, w, h, Style{FG: t.FaceText, BG: t.Face})
	hi, lo := Style{FG: t.Light, BG: t.Face}, Style{FG: t.Shadow, BG: t.Face}
	right, bottom := x+w-1, y+h-1
	for i := x + 1; i < right; i++ {
		c.Set(i, y, '─', hi)
		c.Set(i, bottom, '─', lo)
	}
	for j := y + 1; j < bottom; j++ {
		c.Set(x, j, '│', hi)
		c.Set(right, j, '│', lo)
	}
	c.Set(x, y, '┌', hi)
	c.Set(right, y, '┐', lo)
	c.Set(x, bottom, '└', lo)
	c.Set(right, bottom, '┘', lo)
}

// button draws a label with a one-eighth-block bevel: raised (1), sunken
// (-1) or flat (0). It returns the button's width.
func (a *App) button(c *Canvas, x, y int, label string, st Style, bevel int) int {
	t := a.theme()
	w := runewidth.StringWidth(label) + 2
	c.Fill(x, y, w, 1, st)
	c.Text(x+1, y, w-2, label, st)
	if bevel != 0 {
		l, r := t.Light, t.Shadow
		if bevel < 0 {
			l, r = r, l
		}
		c.Set(x, y, '▏', Style{FG: l, BG: st.BG})
		c.Set(x+w-1, y, '▕', Style{FG: r, BG: st.BG})
	}
	return w
}

func (a *App) drawToolbar(c *Canvas, y int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	c.Fill(0, y, a.w, 1, face)
	x := 1
	for i, label := range toolButtons {
		if label == "" {
			c.Set(x, y, '│', Style{FG: t.Shadow, BG: t.Face})
			x += 2
			continue
		}
		if label == "Connect" && !a.opts.Demo && a.connected(a.active.Net) {
			label = "Disconnect" // a toggle, like mIRC's
		}
		w := runewidth.StringWidth(label) + 2
		if x+w > a.w {
			break
		}
		st, bevel := face, 0
		if i == a.pressedTool {
			st.BG, bevel = t.Pressed, -1
		} else if a.my == y && a.mx >= x && a.mx < x+w && len(a.menus) == 0 && a.dlg == nil {
			st.BG, bevel = t.Hover, 1
		}
		a.button(c, x, y, label, st, bevel)
		a.addRegion(Region{X: x, Y: y, W: w, H: 1, Kind: HitTool, I: i})
		x += w
	}
}

// switchOrder is the windows in switchbar order: the network order, or,
// with Display > Sort switchbar, each network's status window then its
// channels and queries alphabetically, like mIRC.
func (a *App) switchOrder() []*state.Window {
	if !a.sortSwitch {
		return a.order()
	}
	var out []*state.Window
	for _, n := range a.nets {
		ws := slices.Clone(n.Wins[1:])
		slices.SortStableFunc(ws, func(x, y *state.Window) int {
			return cmp.Or(cmp.Compare(x.Kind, y.Kind), strings.Compare(n.Fold(x.Name), n.Fold(y.Name)))
		})
		out = append(append(out, n.Status()), ws...)
	}
	return out
}

// switchBtn is one switchbar button, laid out.
type switchBtn struct {
	w        *state.Window
	label    string
	x, row   int
	width    int
	closable bool
}

// minSwitchLabel is how short a crowded switchbar cuts labels before it
// wraps onto another row.
const minSwitchLabel = 8

// switchLayout lays the switchbar out like mIRC's: when the buttons don't
// fit, their labels get shorter, down to minSwitchLabel, and then they
// wrap onto more rows, so every window keeps a button. Rows are decided
// without the hovered button's ×, so hovering never changes the layout's
// height.
func (a *App) switchLayout(y int) (btns []switchBtn, rows int) {
	// Each row keeps room for one hovered button's " ×", so hovering
	// never pushes a button off the end of its row.
	const hoverRoom = 2
	right := a.w - 1 - hoverRoom
	type item struct {
		w        *state.Window
		name     string
		closable bool
	}
	var items []item
	longest := 0
	for _, w := range a.switchOrder() {
		name := w.Name
		if w.Kind == state.KStatus {
			name = w.Net.Name
		}
		items = append(items, item{w, name, w.Kind != state.KStatus && w == a.active})
		longest = max(longest, runewidth.StringWidth(name))
	}
	width := func(it item, limit int) int {
		bw := min(runewidth.StringWidth(it.name), limit) + 2
		if it.closable {
			bw += 2
		}
		return bw
	}
	limit := longest
	for ; limit > minSwitchLabel; limit-- {
		total := -1
		for _, it := range items {
			total += width(it, limit) + 1
		}
		if 1+total <= right {
			break
		}
	}
	x := 1
	for _, it := range items {
		bw := width(it, limit)
		if x+bw > right && x > 1 {
			rows, x = rows+1, 1
		}
		label := it.name
		if runewidth.StringWidth(label) > limit {
			label = runewidth.Truncate(label, limit, "…")
		}
		btns = append(btns, switchBtn{w: it.w, label: label, x: x, row: rows, width: bw, closable: it.closable})
		x += bw + 1
	}
	if a.fillSwitch { // Display: stretch each row's buttons over the bar, like mIRC
		for row := 0; row <= rows; row++ {
			var idx []int
			end := 1
			for i, b := range btns {
				if b.row == row {
					idx = append(idx, i)
					end = b.x + b.width + 1
				}
			}
			if len(idx) == 0 {
				continue
			}
			spare, shift := right+1-end, 0
			for j, i := range idx { // spread the spare columns, the first ones get the remainder
				extra := spare / len(idx)
				if j < spare%len(idx) {
					extra++
				}
				btns[i].x += shift
				btns[i].width += extra
				shift += extra
			}
		}
	}
	// A window's close box is on its switchbar button, like a browser
	// tab's, while it's active or under the mouse. (mIRC put it on the
	// menubar, which only makes sense next to MDI window frames.) A hovered
	// button grows to the right within its row, so the × stays under the
	// pointer; it stays hovered over its × too.
	a.hoverTab = nil
	for i := range btns {
		b := &btns[i]
		over := func(width int) bool { return a.my == y+b.row && a.mx >= b.x && a.mx < b.x+width }
		if b.w.Kind == state.KStatus || !(over(b.width) || (a.hoverTab == nil && a.lastHover == b.w && over(b.width+2))) {
			continue
		}
		a.hoverTab = b.w
		if !b.closable {
			b.closable, b.width = true, b.width+2
			for j := i + 1; j < len(btns) && btns[j].row == b.row; j++ {
				btns[j].x += 2
			}
		}
	}
	a.lastHover = a.hoverTab
	return btns, rows + 1
}

// drawSwitchbar draws the switchbar from row y and returns how many rows
// it took.
func (a *App) drawSwitchbar(c *Canvas, y int) int {
	t := a.theme()
	btns, rows := a.switchLayout(y)
	c.Fill(0, y, a.w, rows, Style{BG: t.Face})
	for _, b := range btns {
		label := b.label
		if pad := b.width - 2 - runewidth.StringWidth(label); b.closable && pad >= 2 {
			label += strings.Repeat(" ", pad-2) + " ×" // filled: the × at the end
		} else if b.closable {
			label += " ×"
		} else if pad > 0 {
			label = strings.Repeat(" ", pad/2) + label + strings.Repeat(" ", pad-pad/2)
		}
		st, bevel := Style{FG: a.actFG(b.w, t.FaceText), BG: t.Face}, 1
		if b.w == a.active {
			st.BG, st.Bold, bevel = t.ButtonDown, true, -1
		}
		by := y + b.row
		a.button(c, b.x, by, label, st, bevel)
		a.addRegion(Region{X: b.x, Y: by, W: b.width, H: 1, Kind: HitSwitch, Win: b.w})
		if b.closable {
			a.addRegion(Region{X: b.x + b.width - 3, Y: by, W: 2, H: 1, Kind: HitChildBtn, Win: b.w})
		}
	}
	return rows
}

func (a *App) drawTreebar(c *Canvas, x, y, w, h int) {
	t := a.theme()
	lines := Style{FG: t.Lines, BG: t.TextBG}
	c.Fill(x, y, w, h, Style{FG: t.TextFG, BG: t.TextBG})
	row := y
	item := func(win *state.Window, lx int, label string) {
		fg := t.TextFG
		if win.Kind == state.KStatus && a.offline(win.Net) {
			fg = t.Disabled
		}
		st := Style{FG: a.actFG(win, fg), BG: t.TextBG}
		if win == a.active {
			st = Style{FG: t.SelFG, BG: t.SelBG}
		}
		avail := x + w - lx
		c.Fill(lx, row, min(runewidth.StringWidth(label)+2, avail), 1, st)
		c.Text(lx+1, row, avail-1, label, st)
		a.addRegion(Region{X: x, Y: row, W: w, H: 1, Kind: HitTree, Win: win})
	}
	for _, n := range a.nets {
		if row >= y+h {
			break
		}
		arrow := '▾'
		if n.Collapsed {
			arrow = '▸'
		}
		c.Set(x+1, row, arrow, lines)
		item(n.Wins[0], x+2, n.Name)
		a.addRegion(Region{X: x, Y: row, W: 3, H: 1, Kind: HitTreeToggle, Net: n})
		row++
		if n.Collapsed {
			continue
		}
		kids := n.Wins[1:]
		for i, win := range kids {
			if row >= y+h {
				break
			}
			branch := "├─"
			if i == len(kids)-1 {
				branch = "└─"
			}
			c.Text(x+2, row, 2, branch, lines)
			item(win, x+4, win.Name)
			row++
		}
	}
	for j := y; j < y+h; j++ {
		c.Set(x+w, j, ' ', Style{BG: t.Face})
	}
	a.addRegion(Region{X: x + w, Y: y, W: 1, H: h, Kind: HitTreeSplit})
}

// drawWindow draws the active (maximized) window: messages, the nicklist
// for channels, and the editbox along the bottom.
func (a *App) drawWindow(c *Canvas, win *state.Window, x, y, w, h int) {
	if win.Kind == state.KTransfer { // no text or editbox, like mIRC's
		a.drawTransfer(c, win, x, y, w, h)
		return
	}
	t := a.theme()
	// The editbox grows with Alt+Enter's lines; the second editbox (Alt+Q)
	// takes a separator and its own rows under it.
	eh, e2h := editHeight(&win.Edit, w), 0
	if win.Edit2On {
		e2h = 1 + editHeight(&win.Edit2, w)
	}
	ph := max(1, h-1-eh-e2h)
	left, right := x, x+w
	if win.Kind == state.KChannel && a.showNicklist {
		a.nickW = clamp(a.nickW, 8, w/3)
		nx, sx := right-a.nickW, right-a.nickW-1
		a.nickLeftX = -1
		if a.winOpts(win).Nicklist == "left" { // the window's own Nicklist option
			nx, sx = x, x+a.nickW
			a.nickLeftX = x
		}
		a.drawNicklist(c, win, nx, y, a.nickW, ph)
		for j := y; j < y+ph; j++ {
			c.Set(sx, j, ' ', Style{BG: t.Face})
		}
		a.addRegion(Region{X: sx, Y: y, W: 1, H: ph, Kind: HitNickSplit})
		if a.nickLeftX >= 0 {
			left = sx + 1
		} else {
			right = sx
		}
	}
	a.drawMessages(c, win, left, y, right-left, ph)
	sep := Style{FG: t.Lines, BG: t.TextBG}
	for i := x; i < x+w; i++ {
		c.Set(i, y+ph, '─', sep)
	}
	focus1 := !a.nickFocus && !(win.Edit2On && win.Edit2Focus)
	cx, cy := a.drawWinEdit(c, &win.Edit, 1, x, y+ph+1, w, eh, focus1)
	if focus1 {
		a.caretX, a.caretY = cx, cy
	}
	if win.Edit2On {
		ey := y + ph + 1 + eh
		for i := x; i < x+w; i++ {
			c.Set(i, ey, '─', sep)
		}
		focus2 := !a.nickFocus && win.Edit2Focus
		cx, cy := a.drawWinEdit(c, &win.Edit2, 2, x, ey+1, w, e2h-1, focus2)
		if focus2 {
			a.caretX, a.caretY = cx, cy
		}
	}
}

func (a *App) drawMessages(c *Canvas, win *state.Window, x, y, w, h int) {
	t := a.theme()
	tw := w - 2 // one column of padding, one for the scrollbar
	rows, _ := a.windowRows(win, tw)
	win.Scroll = clamp(win.Scroll, 0, len(rows)-h)
	top := max(0, len(rows)-h-win.Scroll)
	a.addRegion(Region{X: x, Y: y, W: w - 1, H: h, Kind: HitMsg})
	// Nicks in the text are hotlinks, like mIRC's: double-click for a
	// query, right-click for the nicklist popup.
	var nicks map[string]string
	if win.Kind == state.KChannel {
		nicks = make(map[string]string, len(win.Nicks))
		for _, n := range win.Nicks {
			nicks[win.Net.Fold(n.Name)] = n.Name
		}
	}
	for i := 0; i < h && top+i < len(rows); i++ {
		r, ry := rows[top+i], y+i
		if r.marker {
			for cx := x + 1; cx < x+1+tw; cx++ {
				c.Set(cx, ry, '─', Style{FG: t.Marker, BG: t.TextBG})
			}
			continue
		}
		if r.shade {
			c.Fill(x, ry, w-1, 1, Style{BG: t.ShadeBG})
		}
		cx := x + 1 + r.indent
		var url *string
		ux := cx
		for _, sr := range r.runes {
			if sr.URL != url {
				if url != nil {
					a.addRegion(Region{X: ux, Y: ry, W: cx - ux, H: 1, Kind: HitURL, S: *url})
				}
				url, ux = sr.URL, cx
			}
			cx += c.Rune(cx, ry, x+1+tw-cx, sr.R, sr.St)
		}
		if url != nil {
			a.addRegion(Region{X: ux, Y: ry, W: cx - ux, H: 1, Kind: HitURL, S: *url})
		}
		if nicks != nil {
			a.nickLinks(win, r, nicks, x+1+r.indent, ry)
		}
		if a.found(win, r) {
			for sx := x + 1; sx < x+1+tw; sx++ {
				c.Restyle(sx, ry, t.SelFG, t.SelBG)
			}
		}
		if s := a.sel; s != nil && s.moved && s.win == win {
			if from, to, ok := s.span(top + i); ok {
				for sx := x + 1 + from; sx < min(x+1+tw, x+1+to); sx++ {
					c.Restyle(sx, ry, t.SelFG, t.SelBG)
				}
			}
		}
	}
	a.drawScrollbar(c, x+w-1, y, h, len(rows), h, top, HitMsgScroll)
	a.msgW, a.msgH = tw, h
	a.msgX, a.msgY, a.msgTop = x+1, y, top
}

// windowRows wraps a window's lines to width w and inserts the line
// marker; marker is the marker row's index, or -1.
func (a *App) windowRows(win *state.Window, w int) (rows []textRow, marker int) {
	marker = -1
	for i, l := range win.Lines {
		if a.showMarker && i > 0 && i == win.Marker {
			marker = len(rows)
			rows = append(rows, textRow{marker: true})
		}
		for _, r := range a.lineRows(win, l, w, a.shading && i%2 == 1) {
			r.line = i
			rows = append(rows, r)
		}
	}
	return rows, marker
}

// lineRows renders one line: the timestamp, a "<nick>" with its mode
// prefix and nick color, then the text with its mIRC codes and URLs.
func (a *App) lineRows(win *state.Window, l state.Line, w int, shade bool) []textRow {
	t := a.theme()
	bg := t.TextBG
	if shade {
		bg = t.ShadeBG
	}
	var rs []SRune
	add := func(s string, st Style) {
		for _, r := range s {
			rs = append(rs, SRune{R: r, St: st})
		}
	}
	indent := 0
	if a.stampsFor(win) { // the window's own Timestamp, or Options
		stamp := l.Time.Format(stampLayout(a.stampFormat)) + " "
		add(stamp, Style{FG: t.Roles[state.RoleTimestamp], BG: bg})
		indent = runewidth.StringWidth(stamp)
	}
	if w < indent+10 {
		indent = 0
	}
	base := Style{FG: t.Roles[l.Role], BG: bg}
	if l.Color > 0 { // a highlight rule's color
		base.FG = t.Pal[(l.Color-1)%16]
	}
	text := l.Text
	if !a.prefixOwn && l.Role == state.RoleOwn { // Options > Messages
		if _, after, ok := strings.Cut(text, "> "); ok && strings.HasPrefix(text, "<") {
			text = after
		}
	}
	if a.stripCodes && l.Role != state.RoleOwn { // Options > Messages
		text = stripCodes(text)
	}
	rest, isMsg := strings.CutPrefix(text, "<")
	if nick, after, ok := strings.Cut(rest, ">"); isMsg && ok && nick != "" {
		st := base
		switch c := a.nickRuleColor(win, nick); {
		case l.Role == state.RoleHighlight:
		case c >= 0: // Address Book > Nick Colors
			st.FG = t.Pal[c%16]
		case a.nickColors:
			st.FG = t.Pal[nickColor(nick)]
		}
		prefix := ""
		if a.modePrefix {
			prefix = win.PrefixOf(nick)
		}
		add("<"+prefix+nick+">", st)
		text = after
	}
	rs = append(rs, parseMIRC(text, base, &t.Pal)...)
	markURLs(rs)
	parts := wrap(rs, w, indent)
	out := make([]textRow, len(parts))
	for i, p := range parts {
		out[i] = textRow{runes: p, shade: shade, cont: i > 0}
		if i > 0 {
			out[i].indent = indent
		}
	}
	return out
}

// drawScrollbar draws a Windows 98 style scrollbar; top is the first
// visible row out of total.
func (a *App) drawScrollbar(c *Canvas, x, y, h, total, visible, top int, kind HitKind) {
	t := a.theme()
	btn := Style{FG: t.FaceText, BG: t.Face}
	if total <= visible {
		btn.FG = t.Disabled
	}
	c.Set(x, y, '▲', btn)
	c.Set(x, y+h-1, '▼', btn)
	a.addRegion(Region{X: x, Y: y, W: 1, H: 1, Kind: kind, I: -1})
	a.addRegion(Region{X: x, Y: y + h - 1, W: 1, H: 1, Kind: kind, I: 1})
	track := h - 2
	for j := range track {
		c.Set(x, y+1+j, '▒', Style{FG: t.TrackFG, BG: t.TrackBG})
	}
	if total <= visible || track < 1 {
		return
	}
	size := max(1, track*visible/total)
	pos := (track - size) * top / (total - visible)
	c.Fill(x, y+1+pos, 1, size, Style{BG: t.Face})
	a.addRegion(Region{X: x, Y: y + 1, W: 1, H: pos, Kind: kind, I: -2})
	a.addRegion(Region{X: x, Y: y + 1 + pos + size, W: 1, H: track - pos - size, Kind: kind, I: 2})
	a.addRegion(Region{X: x, Y: y + 1 + pos, W: 1, H: size, Kind: kind, S: "thumb"})
	a.sbars[kind] = scrollGeom{trackY: y + 1, track: track, size: size, total: total, visible: visible}
}

// scrollGeom is a scrollbar's layout from the last frame, for dragging its
// thumb.
type scrollGeom struct {
	trackY, track, size, total, visible int
}

func (a *App) drawNicklist(c *Canvas, win *state.Window, x, y, w, h int) {
	t := a.theme()
	n := len(win.Nicks)
	lw := w
	if n > h {
		lw = w - 1
	}
	a.addRegion(Region{X: x, Y: y, W: lw, H: h, Kind: HitNick, Win: win}) // empty space
	// Without keyboard focus the selection is drawn gray, like an
	// unfocused Windows list.
	focused := a.nickFocus && win == a.active
	win.NickTop = clamp(win.NickTop, 0, n-h)
	for i := 0; i < h && win.NickTop+i < n; i++ {
		nk := win.Nicks[win.NickTop+i]
		st := Style{FG: t.TextFG, BG: t.TextBG}
		if c := a.nickRuleColor(win, nk.Name); c >= 0 {
			st.FG = t.Pal[c%16]
		} else if a.nickColors {
			st.FG = t.Pal[nickColor(nk.Name)]
		}
		if nk.Away {
			st.FG = t.Disabled
		}
		switch {
		case nk.Name == win.NickSel && focused:
			st = Style{FG: t.SelFG, BG: t.SelBG}
		case nk.Name == win.NickSel:
			st = Style{FG: t.FaceText, BG: t.Face}
		}
		c.Fill(x, y+i, lw, 1, st)
		c.Text(x+1, y+i, lw-1, nk.Prefix+nk.Name, st)
		a.addRegion(Region{X: x, Y: y + i, W: lw, H: 1, Kind: HitNick, S: nk.Name, Win: win})
	}
	if n > h {
		a.drawScrollbar(c, x+w-1, y, h, n, h, win.NickTop, HitNickScroll)
	}
}

// drawEditBox draws a one-line text box, scrolled to keep the cursor in
// view, and returns the cursor's column. A masked box shows dots.
func (a *App) drawEditBox(c *Canvas, e *state.EditBox, x, y, w int, caret, mask bool) int {
	t := a.theme()
	c.Fill(x, y, w, 1, Style{FG: t.TextFG, BG: t.TextBG})
	avail := w - 2
	if e.Pos < e.Off {
		e.Off = e.Pos
	}
	for e.Off < e.Pos {
		width := 0
		for _, r := range e.Buf[e.Off:e.Pos] {
			width += boxRuneWidth(r, mask)
		}
		if width < avail {
			break
		}
		e.Off++
	}
	cx, caretX := x+1, x+1
	from, to, sel := e.Selection()
	for i := e.Off; i <= len(e.Buf) && cx < x+w; i++ {
		r, st := ' ', Style{FG: t.TextFG, BG: t.TextBG}
		if i < len(e.Buf) {
			r = e.Buf[i]
			if g, ok := ctrlGlyph[r]; ok && !mask {
				r, st = g, Style{FG: t.TextBG, BG: t.Lines}
			} else if mask {
				r = '•'
			}
			if sel && i >= from && i < to {
				st = Style{FG: t.SelFG, BG: t.SelBG}
			}
		}
		if i == e.Pos {
			if caret {
				st = Style{FG: t.TextBG, BG: t.TextFG}
			}
			caretX = cx
		}
		cx += c.Rune(cx, y, x+w-cx, r, st)
	}
	return caretX
}

// offline reports whether network n isn't connected: soju says so, or for
// soju's own window, trout isn't connected to soju.
func (a *App) offline(n *state.Network) bool {
	if n == a.home {
		return !a.opts.Demo && a.control == nil
	}
	return n.Down()
}

// winTitle is the caption mIRC shows for a window.
func (a *App) winTitle(w *state.Window) string {
	switch w.Kind {
	case state.KStatus:
		n := w.Net
		s := fmt.Sprintf("Status: %s [+%s] on %s (%s:%d)", n.Nick, n.Umodes, n.Name, n.Server, n.Port)
		if b := a.currentBouncer(); b != nil {
			s += " via " + b.Name
		}
		switch st := n.State(); {
		case n == a.home && a.offline(n):
			s += " [not connected]"
		case st != "" && st != "connected":
			s += " [" + st + "]"
		}
		if a.away != "" {
			s += " [away]"
		}
		if up, ok := a.upAt[n]; ok { // mIRC's online timer
			d := time.Since(up)
			s += fmt.Sprintf(" [online %d:%02d]", int(d.Hours()), int(d.Minutes())%60)
		}
		return s
	case state.KTransfer:
		if tr := a.transferFor(w); tr != nil && tr.size > 0 {
			return fmt.Sprintf("%s (%d%%)", w.Name, tr.got*100/tr.size)
		}
		return w.Name
	case state.KChannel:
		s := fmt.Sprintf("%s [%d] [+%s]", w.Name, len(w.Nicks), w.Modes)
		if w.Topic != "" {
			s += ": " + stripCodes(w.Topic)
		}
		return s
	}
	return fmt.Sprintf("%s (%s)", w.Name, w.Host)
}

// actFG is the switchbar/treebar text color for unseen activity.
func (a *App) actFG(w *state.Window, def RGB) RGB {
	t := a.theme()
	switch w.Act {
	case state.ActEvent:
		return t.ActEvent
	case state.ActMsg:
		return t.ActMsg
	case state.ActHighlight:
		return t.ActHi
	}
	return def
}

// nickLinks registers the nicks in a row of text as hotlinks: runs of
// nick characters, outside links, that are someone in the channel.
func (a *App) nickLinks(win *state.Window, r textRow, nicks map[string]string, x, y int) {
	cx, start, sx := x, -1, 0
	flush := func(end int) {
		if start < 0 {
			return
		}
		word := make([]rune, 0, end-start)
		for _, sr := range r.runes[start:end] {
			word = append(word, sr.R)
		}
		if nick, ok := nicks[win.Net.Fold(string(word))]; ok {
			a.addRegion(Region{X: sx, Y: y, W: cx - sx, H: 1, Kind: HitNickText, S: nick, Win: win})
		}
		start = -1
	}
	for i, sr := range r.runes {
		if sr.URL == nil && nickRune(sr.R) {
			if start < 0 {
				start, sx = i, cx
			}
		} else {
			flush(i)
		}
		cx += runewidth.RuneWidth(sr.R)
	}
	flush(len(r.runes))
}
