package ui

import (
	"os"
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// A window's editboxes, like mIRC's: one line until Alt+Enter adds
// another (the multi-line editbox, at most maxEditRows tall, then it
// scrolls), text selection with Shift or the mouse, and the second
// editbox (Alt+Q).

const maxEditRows = 5

// editSpan is one row of a multi-line editbox: Buf[from:to].
type editSpan struct{ from, to int }

// editLayout breaks text into rows: at line breaks, and where a line is
// wider than w cells.
func editLayout(buf []rune, w int) []editSpan {
	w = max(1, w)
	var out []editSpan
	start := 0
	for i := 0; i <= len(buf); i++ {
		if i < len(buf) && buf[i] != '\n' {
			continue
		}
		cur, width := start, 0
		for j := start; j < i; j++ {
			rw := editWidth(buf[j])
			if width+rw > w && j > cur {
				out = append(out, editSpan{cur, j})
				cur, width = j, 0
			}
			width += rw
		}
		out = append(out, editSpan{cur, i})
		start = i + 1
	}
	return out
}

// editRow is the row of spans the cursor at pos is on.
func editRow(spans []editSpan, pos int) int {
	r := 0
	for i, s := range spans {
		if s.from <= pos {
			r = i
		}
	}
	return r
}

// editHeight is how many rows an editbox w wide takes.
func editHeight(e *state.EditBox, w int) int {
	if !e.Multi() {
		return 1
	}
	return min(maxEditRows, len(editLayout(e.Buf, w-2)))
}

// edit is the editbox typing goes to in w.
func (a *App) edit(w *state.Window) *state.EditBox {
	if w.Edit2On && w.Edit2Focus {
		return &w.Edit2
	}
	return &w.Edit
}

// toggleEdit2 shows or hides w's second editbox; shown, it has the focus.
func (a *App) toggleEdit2(w *state.Window) {
	w.Edit2On = !w.Edit2On
	w.Edit2Focus = w.Edit2On
}

// drawWinEdit draws one of a window's editboxes, h rows tall, and
// registers it as box 1 or 2. It returns the cursor's cell.
func (a *App) drawWinEdit(c *Canvas, e *state.EditBox, box, x, y, w, h int, caret bool) (cx, cy int) {
	if !e.Multi() {
		e.Top = 0
		cx = a.drawEditBox(c, e, x, y, w, caret, false)
		a.addRegion(Region{X: x, Y: y, W: w, H: 1, Kind: HitEdit, I: 0, S: editBoxName[box]})
		return cx, y
	}
	t := a.theme()
	spans := editLayout(e.Buf, w-2)
	row := editRow(spans, e.Pos)
	e.Top = clamp(e.Top, row-h+1, row)
	e.Top = clamp(e.Top, 0, max(0, len(spans)-h))
	from, to, sel := e.Selection()
	cx, cy = x+1, y
	for i := range h {
		ry := y + i
		c.Fill(x, ry, w, 1, Style{FG: t.TextFG, BG: t.TextBG})
		a.addRegion(Region{X: x, Y: ry, W: w, H: 1, Kind: HitEdit, I: e.Top + i, S: editBoxName[box]})
		if e.Top+i >= len(spans) {
			continue
		}
		sp := spans[e.Top+i]
		px := x + 1
		for j := sp.from; j <= sp.to && px < x+w; j++ {
			r, st := ' ', Style{FG: t.TextFG, BG: t.TextBG}
			if j < sp.to {
				r = e.Buf[j]
				if g, ok := ctrlGlyph[r]; ok {
					r, st = g, Style{FG: t.TextBG, BG: t.Lines}
				}
				if sel && j >= from && j < to {
					st = Style{FG: t.SelFG, BG: t.SelBG}
				}
			}
			if j == e.Pos && e.Top+i == row {
				if caret {
					st = Style{FG: t.TextBG, BG: t.TextFG}
				}
				cx, cy = px, ry
			}
			if j == sp.to && (j != e.Pos || e.Top+i != row) {
				break // nothing to draw after the row's text
			}
			px += c.Rune(px, ry, x+w-px, r, st)
		}
	}
	return cx, cy
}

var editBoxName = [...]string{1: "edit1", 2: "edit2"}

// editPosAt is the position in e of a click rel cells into row (a region
// from drawWinEdit) of a box w cells wide.
func editPosAt(e *state.EditBox, row, rel, w int) int {
	if !e.Multi() {
		tmp := *e
		clickEditBox(&tmp, rel, false)
		return tmp.Pos
	}
	spans := editLayout(e.Buf, w-2)
	if row >= len(spans) {
		return len(e.Buf)
	}
	sp := spans[row]
	i, cx := sp.from, 1
	for i < sp.to {
		cw := editWidth(e.Buf[i])
		if cx+cw > rel {
			break
		}
		cx += cw
		i++
	}
	return i
}

// pasteClipboard is Ctrl+V: the system clipboard's text, as if pasted
// into the terminal. (Most terminals paste with their own key too.)
func pasteClipboard() tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch {
		case runtime.GOOS == "darwin":
			cmd = exec.Command("pbpaste")
		case runtime.GOOS == "windows":
			cmd = exec.Command("powershell", "-NoProfile", "-Command", "Get-Clipboard")
		case os.Getenv("WAYLAND_DISPLAY") != "":
			cmd = exec.Command("wl-paste", "--no-newline")
		default:
			cmd = exec.Command("xclip", "-selection", "clipboard", "-o")
		}
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			return nil
		}
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(string(out)), Paste: true}
	}
}
