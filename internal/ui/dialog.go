package ui

import (
	"slices"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// modal is a dialog drawn over everything. While one is open it gets all
// keyboard and mouse input; r is the HitDlg region under the pointer, or
// the zero Region.
type modal interface {
	title() string
	size(a *App) (w, h int)
	draw(a *App, c *Canvas, x, y, w, h int)
	key(a *App, k string) tea.Cmd
	mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd
}

func (a *App) openDialog(d modal) {
	a.closeMenus()
	a.pressedTool = -1
	a.dlg = d
}

// whenFree runs open, which may open a dialog, now if no dialog or menu
// is open, or else once they've closed: something from the network (a
// DCC offer) mustn't replace the dialog you're typing in.
func (a *App) whenFree(open func(a *App) tea.Cmd) {
	if a.dlg == nil && len(a.menus) == 0 {
		a.later(open(a))
		return
	}
	a.waiting = append(a.waiting, open)
}

// openWaiting runs what whenFree held back, now that nothing's open.
func (a *App) openWaiting() tea.Cmd {
	var cmds []tea.Cmd
	for a.dlg == nil && len(a.menus) == 0 && len(a.waiting) > 0 {
		open := a.waiting[0]
		a.waiting = a.waiting[1:]
		cmds = append(cmds, open(a))
	}
	return tea.Batch(cmds...)
}

// drawModal draws the open dialog's frame, title bar and close box
// centered on screen, then lets the dialog fill it in.
func (a *App) drawModal(c *Canvas) {
	if a.dlg == nil {
		return
	}
	t := a.theme()
	w, h := a.dlg.size(a)
	w, h = min(w, a.w), min(h, a.h)
	x, y := (a.w-w)/2, (a.h-h)/2
	a.raisedBox(c, x, y, w, h)
	for i := range w {
		c.Set(x+i, y, ' ', Style{BG: lerp(t.TitleA, t.TitleB, float64(i)/float64(max(1, w-1)))})
	}
	c.TextOver(x+1, y, w-6, a.dlg.title(), t.TitleText, true)
	closeBox := Style{FG: t.FaceText, BG: t.Face, Bold: true}
	c.Fill(x+w-4, y, 3, 1, closeBox)
	c.Set(x+w-3, y, '×', closeBox)
	a.addRegion(Region{X: x + w - 4, Y: y, W: 3, H: 1, Kind: HitDlg, S: "close"})
	a.dlg.draw(a, c, x, y, w, h)
}

// wrapText breaks lines into rows of at most w cells, at spaces where it
// can, so a dialog at the smallest screen still shows all its text. A
// wrapped line's rows keep its indent, and a word too long for a row (a
// fingerprint) is cut into even pieces. Wrapping the result again at any
// width from its widest row up to w gives the same rows, so a dialog's
// size and draw agree.
func wrapText(lines []string, w int) []string {
	var rows []string
	for _, line := range lines {
		if w < 1 || runewidth.StringWidth(line) <= w {
			rows = append(rows, line)
			continue
		}
		rest := strings.TrimLeft(line, " ")
		indent := line[:len(line)-len(rest)]
		if len(indent) > w/2 {
			indent = ""
		}
		avail := w - len(indent)
		cur := ""
		flush := func() {
			rows = append(rows, indent+cur)
			cur = ""
		}
		for word := range strings.FieldsSeq(rest) {
			ww := runewidth.StringWidth(word)
			switch {
			case cur != "" && runewidth.StringWidth(cur)+1+ww <= avail:
				cur += " " + word
				continue
			case cur != "":
				flush()
			}
			if ww <= avail {
				cur = word
				continue
			}
			pieces := cutEven(word, (ww+avail-1)/avail)
			for _, p := range pieces[:len(pieces)-1] {
				rows = append(rows, indent+p)
			}
			cur = pieces[len(pieces)-1]
		}
		flush()
	}
	return rows
}

// cutEven cuts s into n pieces about as wide as each other, never inside
// a grapheme cluster.
func cutEven(s string, n int) []string {
	target := (runewidth.StringWidth(s) + n - 1) / n
	var pieces []string
	var cc clusterCells
	start, width := 0, 0
	for i, r := range s {
		joins := cc.joins(r)
		cw := cc.add(r)
		if width+cw > target && width > 0 && !joins {
			pieces = append(pieces, s[start:i])
			start, width = i, 0
		}
		width += cw
	}
	return append(pieces, s[start:])
}

func buttonsWidth(labels []string) int {
	w := 0
	for i, b := range labels {
		w += runewidth.StringWidth(b) + 6
		if i > 0 {
			w += 2
		}
	}
	return w
}

// dlgButtons draws a centered row of push buttons with underlined
// mnemonics; focus is drawn bold.
func (a *App) dlgButtons(c *Canvas, x, y, w int, labels []string, focus int) {
	a.dlgButtonsTaken(c, x, y, w, labels, focus, "")
}

// dlgButtonsTaken is dlgButtons in a dialog where other things already
// have some letters (taken), like the Scripts Editor's menus.
func (a *App) dlgButtonsTaken(c *Canvas, x, y, w int, labels []string, focus int, taken string) {
	t := a.theme()
	bx := x + (w-buttonsWidth(labels))/2
	mns := mnemonics(labels, taken)
	for i, b := range labels {
		st := Style{FG: t.FaceText, BG: t.Face}
		if i == focus {
			st.BG, st.Bold = t.Hover, true
		}
		n := a.button(c, bx, y, "  "+b+"  ", st, 1)
		a.underlineMnemonic(c, bx+3, y, b, mns[i], st)
		a.addRegion(Region{X: bx, Y: y, W: n, H: 1, Kind: HitDlg, S: "button", I: i})
		bx += n + 2
	}
}

// underlineMnemonic underlines label's mnemonic, the mn-th rune, where
// label is drawn at x (-1: it has none).
func (a *App) underlineMnemonic(c *Canvas, x, y int, label string, mn int, st Style) {
	if mn < 0 {
		return
	}
	rs := []rune(label)
	st.Underline = true
	c.Set(x+runewidth.StringWidth(string(rs[:mn])), y, rs[mn], st)
}

// noMnemonic are the buttons that have no letter, like mIRC's: OK is
// Enter, and Cancel and Close are Esc.
var noMnemonic = map[string]bool{"OK": true, "Cancel": true, "Close": true}

// mnemonics are the letters a row of buttons underlines, as the index of
// the rune in each label (-1 for none), each one different, like Windows
// programs place their &: a button's first letter, or if that's taken,
// the first letter of another of its words, else another of its letters.
// Earlier buttons get theirs first. taken are letters the dialog already
// uses for something else.
func mnemonics(labels []string, taken string) []int {
	used := strings.ToLower(taken)
	mns := make([]int, len(labels))
	for i, label := range labels {
		mns[i] = -1
		if noMnemonic[label] {
			continue
		}
		rs := []rune(label)
		var initials, others []int
		for j, r := range rs {
			switch {
			case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			case j == 0 || rs[j-1] == ' ':
				initials = append(initials, j)
			default:
				others = append(others, j)
			}
		}
		for _, j := range append(initials, others...) {
			if l := unicode.ToLower(rs[j]); !strings.ContainsRune(used, l) {
				mns[i] = j
				used += string(l)
				break
			}
		}
	}
	return mns
}

// buttonRow is a dialog whose buttons work by their underlined letters.
type buttonRow interface {
	buttonLabels(a *App) []string // as they're drawn
	pressButton(a *App, i int) tea.Cmd
	// typing says the letter alone goes to a text field now: then only
	// Alt and the letter presses a button.
	typing() bool
}

// mnemonicsTaken is a buttonRow whose dialog already uses some letters.
type mnemonicsTaken interface{ takenMnemonics() string }

// buttonKey is the button k presses by its underlined letter: Alt and
// the letter, or the letter alone when it isn't typing, like a Windows
// dialog.
func buttonKey(d buttonRow, a *App, k string) (int, bool) {
	letter, alt := strings.CutPrefix(k, "alt+")
	if len([]rune(letter)) != 1 || !alt && d.typing() {
		return 0, false
	}
	taken := ""
	if t, ok := d.(mnemonicsTaken); ok {
		taken = t.takenMnemonics()
	}
	labels := d.buttonLabels(a)
	l := unicode.ToLower([]rune(letter)[0])
	for i, mn := range mnemonics(labels, taken) {
		if mn >= 0 && unicode.ToLower([]rune(labels[i])[mn]) == l {
			return i, true
		}
	}
	return 0, false
}

// msgBox is a Windows-style message box.
type msgBox struct {
	Title   string
	Lines   []string
	Buttons []string
	Sel     int // focused button
	Cancel  int // button chosen by Esc or the close box
	OnClose func(a *App, button int) tea.Cmd

	// Check, if set, is a checkbox under the text (Tab to it, Space or a
	// click toggles it) that OnClose can read as Checked.
	Check      string
	Checked    bool
	checkFocus bool
}

func message(lines ...string) *msgBox {
	return &msgBox{Title: "trout", Lines: lines, Buttons: []string{"OK"}}
}

func (d *msgBox) title() string { return d.Title }

// text is the box's lines wrapped to a box w wide. If they're still taller
// than the screen, the blank lines between paragraphs go first.
func (d *msgBox) text(a *App, w int) []string {
	lines := wrapText(d.Lines, w-6)
	room := a.h - 6
	if d.Check != "" {
		room -= 2
	}
	if len(lines) > room {
		lines = slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return l == "" })
	}
	return lines
}

// msgBoxMaxW is the widest a message box grows for its text; like
// Windows's, longer lines wrap.
const msgBoxMaxW = 110

func (d *msgBox) size(a *App) (int, int) {
	lines := d.text(a, min(a.w, msgBoxMaxW))
	tw := 0
	for _, l := range lines {
		tw = max(tw, runewidth.StringWidth(l))
	}
	h := len(lines) + 6
	if d.Check != "" {
		h += 2
		tw = max(tw, runewidth.StringWidth(d.Check)+4)
	}
	return max(tw+6, buttonsWidth(d.Buttons)+4, runewidth.StringWidth(d.Title)+8), h
}

func (d *msgBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	lines := d.text(a, min(w, msgBoxMaxW))
	for i, l := range lines {
		if 2+i >= h-4 {
			break // what still doesn't fit stays off the buttons
		}
		c.Text(x+3, y+2+i, w-6, l, Style{FG: t.FaceText, BG: t.Face})
	}
	sel := d.Sel
	if d.Check != "" {
		box := "[ ] "
		if d.Checked {
			box = "[x] "
		}
		st := Style{FG: t.FaceText, BG: t.Face}
		if d.checkFocus {
			st, sel = Style{FG: t.MenuSelText, BG: t.MenuSel}, -1
		}
		cy := y + 3 + len(lines)
		c.Text(x+3, cy, w-6, box+d.Check, st)
		a.addRegion(Region{X: x + 3, Y: cy, W: runewidth.StringWidth(box + d.Check), H: 1, Kind: HitDlg, S: "check"})
	}
	a.dlgButtons(c, x, y+h-3, w, d.Buttons, sel)
}

func (d *msgBox) close(a *App, button int) tea.Cmd {
	a.dlg = nil
	if d.OnClose != nil {
		return d.OnClose(a, button)
	}
	return nil
}

func (d *msgBox) key(a *App, k string) tea.Cmd {
	n := len(d.Buttons)
	if d.Check != "" && (k == "tab" || k == "shift+tab") {
		d.checkFocus = !d.checkFocus
		return nil
	}
	if d.checkFocus && k == " " {
		d.Checked = !d.Checked
		return nil
	}
	switch k {
	case "left", "shift+tab":
		d.Sel = (d.Sel + n - 1) % n
	case "right", "tab":
		d.Sel = (d.Sel + 1) % n
	case "enter", " ":
		return d.close(a, d.Sel)
	case "esc":
		return d.close(a, d.Cancel)
	default:
		if i, ok := buttonKey(d, a, k); ok {
			return d.close(a, i)
		}
	}
	return nil
}

func (d *msgBox) buttonLabels(*App) []string        { return d.Buttons }
func (d *msgBox) pressButton(a *App, i int) tea.Cmd { return d.close(a, i) }
func (d *msgBox) typing() bool                      { return false }

func (d *msgBox) mouse(a *App, m tea.MouseMsg, r Region, _ bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "button":
		return d.close(a, r.I)
	case "close":
		return d.close(a, d.Cancel)
	case "check":
		d.Checked = !d.Checked
	}
	return nil
}

// listBox is a dialog with a scrolling list over a row of buttons: the
// Favorites folder, the Channels List, the Address Book, URLs and
// downloads. The first button is the default (Enter, double-click), a
// "Remove" button also gets the Delete key, and the last one closes.
type listBox struct {
	Title, Header string
	Rows, Keys    []string // what's shown, and what OnPress gets
	Sel, Top      int
	Buttons       []string // Join, Close when empty
	// OnPress handles every button but the last; key is the selected
	// row's, or "" for an empty list. It closes the dialog if it should.
	OnPress func(a *App, l *listBox, button, key string) tea.Cmd
	// OnClose, if set, runs when the list closes (to go back to Options).
	OnClose func(a *App)

	// rowsW is the widest row's width, measured when Rows was rowsOf:
	// the Channels List can have 50,000 rows, and size runs every frame.
	rowsW  int
	rowsOf []string
}

// sameSlice reports whether a and b are the same slice: the same array
// and length. Rows are only ever replaced, appended to or cut, never
// changed in place, so that's the same rows.
func sameSlice(a, b []string) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

func (l *listBox) close(a *App) {
	a.dlg = nil
	if l.OnClose != nil {
		l.OnClose(a)
	}
}

// joinList is an OnPress that closes the list and joins the selected
// channel.
func joinList(join func(a *App, ch string)) func(*App, *listBox, string, string) tea.Cmd {
	return func(a *App, _ *listBox, _ string, ch string) tea.Cmd {
		a.dlg = nil
		if ch != "" {
			join(a, ch)
		}
		return nil
	}
}

func (l *listBox) buttons() []string {
	if len(l.Buttons) == 0 {
		return []string{"Join", "Close"}
	}
	return l.Buttons
}

func (l *listBox) press(a *App, button string) tea.Cmd {
	bs := l.buttons()
	if button == bs[len(bs)-1] || l.OnPress == nil {
		l.close(a)
		return nil
	}
	key := ""
	if len(l.Keys) > 0 {
		key = l.Keys[clamp(l.Sel, 0, len(l.Keys)-1)]
	}
	return l.OnPress(a, l, button, key)
}

// removeSel drops the selected row.
func (l *listBox) removeSel() {
	if len(l.Keys) == 0 {
		return
	}
	i := clamp(l.Sel, 0, len(l.Keys)-1)
	l.Rows = slices.Delete(l.Rows, i, min(i+1, len(l.Rows)))
	l.Keys = slices.Delete(l.Keys, i, i+1)
}

func (l *listBox) title() string { return l.Title }

func (l *listBox) visible(a *App) int { return clamp(a.h-9, 3, 12) }

func (l *listBox) size(a *App) (int, int) {
	if !sameSlice(l.Rows, l.rowsOf) {
		l.rowsW, l.rowsOf = 0, l.Rows
		for _, r := range l.Rows {
			l.rowsW = max(l.rowsW, runewidth.StringWidth(r))
		}
	}
	w := max(runewidth.StringWidth(l.Header), l.rowsW)
	// Wide enough for its buttons too.
	return min(max(w+8, 40, buttonsWidth(l.buttons())+4), a.w-2), l.visible(a) + 7
}

func (l *listBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	n := l.visible(a)
	lx, lw := x+2, w-4
	c.Text(lx+1, y+2, lw-1, l.Header, Style{FG: t.FaceText, BG: t.Face, Bold: true})
	c.Fill(lx, y+3, lw, n, Style{BG: t.TextBG})
	l.Sel = clamp(l.Sel, 0, len(l.Rows)-1)
	l.Top = clamp(l.Top, 0, len(l.Rows)-n)
	rw := lw
	if len(l.Rows) > n {
		rw--
		a.drawScrollbar(c, lx+lw-1, y+3, n, len(l.Rows), n, l.Top, HitDlg)
	}
	for i := 0; i < n && l.Top+i < len(l.Rows); i++ {
		idx := l.Top + i
		st := Style{FG: t.TextFG, BG: t.TextBG}
		if idx == l.Sel {
			st = Style{FG: t.SelFG, BG: t.SelBG}
		}
		c.Fill(lx, y+3+i, rw, 1, st)
		c.Text(lx+1, y+3+i, rw-1, l.Rows[idx], st)
		a.addRegion(Region{X: lx, Y: y + 3 + i, W: rw, H: 1, Kind: HitDlg, S: "item", I: idx})
	}
	a.dlgButtons(c, x, y+h-3, w, l.buttons(), 0)
}

func (l *listBox) key(a *App, k string) tea.Cmd {
	n := l.visible(a)
	switch k {
	case "up":
		l.Sel--
	case "down":
		l.Sel++
	case "pgup":
		l.Sel -= n
	case "pgdown":
		l.Sel += n
	case "home":
		l.Sel = 0
	case "end":
		l.Sel = len(l.Rows) - 1
	case "enter":
		return l.press(a, l.buttons()[0])
	case "delete", "backspace":
		if slices.Contains(l.buttons(), "Remove") {
			return l.press(a, "Remove")
		}
	case "esc":
		l.close(a)
	}
	l.Sel = clamp(l.Sel, 0, len(l.Rows)-1)
	l.Top = clamp(l.Top, l.Sel-n+1, l.Sel) // keep the selection in view
	return nil
}

func (l *listBox) mouse(a *App, m tea.MouseMsg, r Region, double bool) tea.Cmd {
	switch {
	case m.Button == tea.MouseButtonWheelUp:
		l.Top -= 3
	case m.Button == tea.MouseButtonWheelDown:
		l.Top += 3
	case m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft:
		return nil
	case r.S == "item":
		l.Sel = r.I
		if double {
			return l.press(a, l.buttons()[0])
		}
	case r.S == "button":
		return l.press(a, l.buttons()[r.I])
	case r.S == "close":
		l.close(a)
	case r.Kind == HitDlg: // scrollbar arrows and track
		l.Top += scrollStep(r.I, l.visible(a))
	}
	l.Sel = clamp(l.Sel, 0, len(l.Rows)-1)
	return nil
}

func (l *listBox) buttonLabels(*App) []string        { return l.buttons() }
func (l *listBox) pressButton(a *App, i int) tea.Cmd { return l.press(a, l.buttons()[i]) }
func (l *listBox) typing() bool                      { return false }
