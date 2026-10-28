package ui

import (
	"strings"

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
	t := a.theme()
	bx := x + (w-buttonsWidth(labels))/2
	for i, b := range labels {
		st := Style{FG: t.FaceText, BG: t.Face}
		if i == focus {
			st.BG, st.Bold = t.Hover, true
		}
		n := a.button(c, bx, y, "  "+b+"  ", st, 1)
		mn := st
		mn.Underline = true
		c.Set(bx+3, y, []rune(b)[0], mn)
		a.addRegion(Region{X: bx, Y: y, W: n, H: 1, Kind: HitDlg, S: "button", I: i})
		bx += n + 2
	}
}

// msgBox is a Windows-style message box.
type msgBox struct {
	Title   string
	Lines   []string
	Buttons []string
	Sel     int // focused button
	Cancel  int // button chosen by Esc or the close box
	OnClose func(a *App, button int) tea.Cmd
}

func message(lines ...string) *msgBox {
	return &msgBox{Title: "trout", Lines: lines, Buttons: []string{"OK"}}
}

func (d *msgBox) title() string { return d.Title }

func (d *msgBox) size(a *App) (int, int) {
	tw := 0
	for _, l := range d.Lines {
		tw = max(tw, runewidth.StringWidth(l))
	}
	return max(tw+6, buttonsWidth(d.Buttons)+4, runewidth.StringWidth(d.Title)+8), len(d.Lines) + 6
}

func (d *msgBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	for i, l := range d.Lines {
		c.Text(x+3, y+2+i, w-6, l, Style{FG: t.FaceText, BG: t.Face})
	}
	a.dlgButtons(c, x, y+h-3, w, d.Buttons, d.Sel)
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
		for i, b := range d.Buttons {
			if strings.EqualFold(k, b[:1]) {
				return d.close(a, i)
			}
		}
	}
	return nil
}

func (d *msgBox) mouse(a *App, m tea.MouseMsg, r Region, _ bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "button":
		return d.close(a, r.I)
	case "close":
		return d.close(a, d.Cancel)
	}
	return nil
}

// listBox is a dialog with a scrolling list and Join/Close buttons, used
// for the Favorites folder and the Channels List.
type listBox struct {
	Title, Header string
	Rows, Keys    []string // what's shown, and what OnJoin gets
	Sel, Top      int
	OnJoin        func(a *App, key string)
}

func (l *listBox) title() string { return l.Title }

func (l *listBox) visible(a *App) int { return clamp(a.h-9, 3, 12) }

func (l *listBox) size(a *App) (int, int) {
	w := runewidth.StringWidth(l.Header)
	for _, r := range l.Rows {
		w = max(w, runewidth.StringWidth(r))
	}
	return min(max(w+8, 40), a.w-2), l.visible(a) + 7
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
	a.dlgButtons(c, x, y+h-3, w, []string{"Join", "Close"}, 0)
}

func (l *listBox) join(a *App) tea.Cmd {
	a.dlg = nil
	if len(l.Keys) > 0 {
		l.OnJoin(a, l.Keys[clamp(l.Sel, 0, len(l.Keys)-1)])
	}
	return nil
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
		return l.join(a)
	case "esc":
		a.dlg = nil
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
			return l.join(a)
		}
	case r.S == "button":
		if r.I == 0 {
			return l.join(a)
		}
		a.dlg = nil
	case r.S == "close":
		a.dlg = nil
	case r.Kind == HitDlg: // scrollbar arrows and track
		l.Top += scrollStep(r.I, l.visible(a))
	}
	l.Sel = clamp(l.Sel, 0, len(l.Rows)-1)
	return nil
}
