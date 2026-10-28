package ui

import (
	"strings"

	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// formBox is a dialog of labeled one-line text fields over a row of
// buttons, the first of which is OK and the last Cancel. It's the Connect
// dialog, and prompts like Kick (why) that mIRC asks with $?="...".
type formBox struct {
	Title   string
	Lines   []string // shown above the fields
	Fields  []*formField
	Buttons []string
	Note    string // under the fields: what's wrong with the input
	Compact bool   // no blank rows between fields

	// OnOK gets the fields' values. An error keeps the dialog open and
	// shows it as the note.
	OnOK     func(a *App, vals []string) (tea.Cmd, error)
	OnCancel func(a *App) tea.Cmd
	// OnButton handles buttons besides the first (OK) and the last
	// (Cancel), by label.
	OnButton map[string]func(*App) tea.Cmd

	focus int // a field, or len(Fields)+i for button i
}

type formField struct {
	Label string
	Mask  bool // a password, shown as dots
	Edit  state.EditBox
	Check bool // a checkbox instead of a text box: Label is its text
	On    bool
	// Group makes checkboxes radio buttons: turning one on turns off the
	// others with the same Group.
	Group string
}

func newRadio(group, label string, on bool) *formField {
	return &formField{Label: label, Check: true, On: on, Group: group}
}

// toggle flips a checkbox, or picks a radio button.
func (f *formBox) toggle(fl *formField) {
	if fl.Group == "" {
		fl.On = !fl.On
		return
	}
	for _, o := range f.Fields {
		if o.Group == fl.Group {
			o.On = o == fl
		}
	}
}

func newCheck(label string, on bool) *formField { return &formField{Label: label, Check: true, On: on} }

func newField(label, value string, mask bool) *formField {
	f := &formField{Label: label, Mask: mask}
	f.Edit.Buf = []rune(value)
	f.Edit.Pos = len(f.Edit.Buf)
	return f
}

const formFieldW = 36

func (f *formBox) title() string { return f.Title }

// step is the rows from one field to the next.
func (f *formBox) step() int {
	if f.Compact {
		return 1
	}
	return 2
}

func (f *formBox) labelW() int {
	w := 0
	for _, fl := range f.Fields {
		if !fl.Check {
			w = max(w, runewidth.StringWidth(fl.Label))
		}
	}
	return w
}

func (f *formBox) size(a *App) (int, int) {
	w := max(f.labelW()+1+formFieldW+6, runewidth.StringWidth(f.Note)+6,
		buttonsWidth(f.Buttons)+4, runewidth.StringWidth(f.Title)+8)
	for _, l := range f.Lines {
		w = max(w, runewidth.StringWidth(l)+6)
	}
	// The fields, then a blank row and the note.
	content := f.step()*len(f.Fields) - (f.step() - 1) + 2
	if len(f.Lines) > 0 {
		content += len(f.Lines) + 1
	}
	return w, content + 6
}

func (f *formBox) draw(a *App, c *Canvas, x, y, w, h int) {
	t := a.theme()
	face := Style{FG: t.FaceText, BG: t.Face}
	tx, tw, row := x+3, w-6, y+2
	for _, l := range f.Lines {
		c.Text(tx, row, tw, l, face)
		row++
	}
	if len(f.Lines) > 0 {
		row++
	}
	lw := f.labelW()
	bx := tx + lw + 1
	bw := x + w - 3 - bx
	for i, fl := range f.Fields {
		if fl.Check {
			box := "[ ] "
			switch {
			case fl.Group != "" && fl.On:
				box = "(•) "
			case fl.Group != "":
				box = "( ) "
			case fl.On:
				box = "[x] "
			}
			st := face
			if i == f.focus {
				st = Style{FG: t.MenuSelText, BG: t.MenuSel}
			}
			label := box + fl.Label
			c.Text(bx, row, bw, label, st)
			a.addRegion(Region{X: bx, Y: row, W: runewidth.StringWidth(label), H: 1, Kind: HitDlg, S: "field", I: i})
			row += f.step()
			continue
		}
		c.Text(tx, row, lw, fl.Label, face)
		a.drawEditBox(c, &fl.Edit, bx, row, bw, i == f.focus, fl.Mask)
		a.addRegion(Region{X: bx, Y: row, W: bw, H: 1, Kind: HitDlg, S: "field", I: i})
		row += f.step()
	}
	if f.Note != "" {
		note := strings.ToUpper(f.Note[:1]) + f.Note[1:]
		c.Text(tx, y+h-5, tw, note, Style{FG: t.Warn, BG: t.Face, Bold: true})
	}
	// While typing in a field, Enter presses the first button, so it's
	// drawn as the default.
	a.dlgButtons(c, x, y+h-3, w, f.Buttons, max(0, f.focus-len(f.Fields)))
}

// press pushes button i: the last one cancels, any other submits.
func (f *formBox) press(a *App, i int) tea.Cmd {
	if i == len(f.Buttons)-1 && i > 0 {
		a.dlg = nil
		if f.OnCancel != nil {
			return f.OnCancel(a)
		}
		return nil
	}
	if h := f.OnButton[f.Buttons[i]]; h != nil && i > 0 {
		return h(a)
	}
	vals := make([]string, len(f.Fields))
	for j, fl := range f.Fields {
		vals[j] = string(fl.Edit.Buf)
		if fl.Check && fl.On {
			vals[j] = "1"
		}
	}
	cmd, err := f.OnOK(a, vals)
	if err != nil {
		f.Note = err.Error()
		return cmd
	}
	if a.dlg == f {
		a.dlg = nil
	}
	return cmd
}

func (f *formBox) key(a *App, k string) tea.Cmd {
	n := len(f.Fields) + len(f.Buttons)
	switch k {
	case "tab", "down":
		f.focus = (f.focus + 1) % n
		return nil
	case "shift+tab", "up":
		f.focus = (f.focus + n - 1) % n
		return nil
	case "enter":
		return f.press(a, max(0, f.focus-len(f.Fields)))
	case "esc":
		return f.press(a, len(f.Buttons)-1)
	}
	if f.focus >= len(f.Fields) {
		switch k {
		case "left":
			f.focus = len(f.Fields) + (f.focus-len(f.Fields)+len(f.Buttons)-1)%len(f.Buttons)
		case "right":
			f.focus = len(f.Fields) + (f.focus-len(f.Fields)+1)%len(f.Buttons)
		}
		return nil
	}
	if fl := f.Fields[f.focus]; fl.Check {
		return nil
	}
	e := &f.Fields[f.focus].Edit
	switch k {
	case "backspace":
		e.Backspace()
	case "delete":
		e.Delete()
	case "left":
		e.Move(-1)
	case "right":
		e.Move(1)
	case "home", "ctrl+a":
		e.Pos = 0
	case "end", "ctrl+e":
		e.Pos = len(e.Buf)
	}
	return nil
}

// typed is text typed or pasted while the form is open.
func (f *formBox) typed(a *App, rs []rune) tea.Cmd {
	if f.focus < len(f.Fields) {
		if fl := f.Fields[f.focus]; fl.Check {
			if string(rs) == " " {
				f.toggle(fl)
			}
			return nil
		}
		f.Fields[f.focus].Edit.Insert(rs...)
		return nil
	}
	if string(rs) == " " {
		return f.press(a, f.focus-len(f.Fields))
	}
	return nil
}

func (f *formBox) mouse(a *App, m tea.MouseMsg, r Region, _ bool) tea.Cmd {
	if m.Action != tea.MouseActionPress || m.Button != tea.MouseButtonLeft {
		return nil
	}
	switch r.S {
	case "field":
		f.focus = r.I
		fl := f.Fields[r.I]
		if fl.Check {
			f.toggle(fl)
			return nil
		}
		clickEditBox(&fl.Edit, m.X-r.X, fl.Mask)
	case "button":
		return f.press(a, r.I)
	case "close":
		return f.press(a, len(f.Buttons)-1)
	}
	return nil
}
