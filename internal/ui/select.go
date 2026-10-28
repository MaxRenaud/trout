package ui

import (
	"strings"

	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// selection is text being drag-selected in the message pane. Rows index
// windowRows; columns count cells from the left edge of the text. Like
// mIRC, letting go of the mouse copies the text and clears the selection;
// holding Ctrl copies the whole lines with their color and format codes,
// and Shift keeps the line breaks as they're shown.
type selection struct {
	win            *state.Window
	ar, ac, br, bc int // anchor and current end
	moved          bool
	codes, breaks  bool
}

func (a *App) startSelection(w *state.Window, x, y int) {
	row, col := a.msgTop+y-a.msgY, max(0, x-a.msgX)
	a.sel = &selection{win: w, ar: row, ac: col, br: row, bc: col}
	a.drag = dragSelect
}

// extendSelection follows the pointer, scrolling when it's dragged past
// the top or bottom of the pane.
func (a *App) extendSelection(x, y int) {
	s := a.sel
	switch {
	case y < a.msgY:
		s.win.Scroll++
	case y >= a.msgY+a.msgH:
		s.win.Scroll--
	}
	y = clamp(y, a.msgY, a.msgY+a.msgH-1)
	if r, c := a.msgTop+y-a.msgY, max(0, x-a.msgX); r != s.br || c != s.bc {
		s.br, s.bc, s.moved = r, c, true
	}
}

func (a *App) finishSelection() tea.Cmd {
	text := a.selectedText()
	a.sel = nil
	if text == "" {
		return nil
	}
	return copyText(text)
}

// span returns the selected columns [from, to) of row.
func (s *selection) span(row int) (from, to int, ok bool) {
	r0, c0, r1, c1 := s.ar, s.ac, s.br, s.bc
	if r1 < r0 || r1 == r0 && c1 < c0 {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}
	if row < r0 || row > r1 {
		return 0, 0, false
	}
	from, to = 0, 1<<30
	if row == r0 {
		from = c0
	}
	if row == r1 {
		to = c1 + 1
	}
	return from, to, true
}

// selectedText is the selection as plain text, with wrapped rows joined
// back into their lines.
func (a *App) selectedText() string {
	s := a.sel
	if s == nil || !s.moved {
		return ""
	}
	rows, _ := a.windowRows(s.win, a.msgW)
	if s.codes {
		return a.selectedLines(rows)
	}
	var lines []string
	var cur []rune
	started := false
	for ri, r := range rows {
		from, to, ok := s.span(ri)
		if !ok || r.marker {
			continue
		}
		if started && (!r.cont || s.breaks) {
			lines = append(lines, strings.TrimRight(string(cur), " "))
			cur = nil
		}
		started = true
		col := r.indent
		for _, sr := range r.runes {
			if col >= from && col < to {
				cur = append(cur, sr.R)
			}
			col += runewidth.RuneWidth(sr.R)
		}
	}
	if started {
		lines = append(lines, strings.TrimRight(string(cur), " "))
	}
	text := strings.Join(lines, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text
}

// copyText puts text on the system clipboard. This only reaches your own
// clipboard when running locally; over SSH a real client would use the
// terminal's OSC 52 clipboard sequence instead.
func copyText(text string) tea.Cmd {
	return func() tea.Msg {
		writeClipboard(text)
		return nil
	}
}

// writeClipboard is copyText's clipboard (clipboard_*.go); tests replace it.
var writeClipboard = func(text string) { _ = systemCopy(text) }

// selectedLines is every line the selection touches, as received: color
// and format codes and all.
func (a *App) selectedLines(rows []textRow) string {
	var lines []string
	last := -1
	for ri, r := range rows {
		if _, _, ok := a.sel.span(ri); !ok || r.marker || r.line == last {
			continue
		}
		last = r.line
		l := a.sel.win.Lines[r.line]
		text := l.Text
		if a.stampsFor(a.sel.win) {
			text = l.Time.Format(stampLayout(a.stampFormat)) + " " + text
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}
