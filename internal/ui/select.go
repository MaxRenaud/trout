package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/MaxRenaud/trout/internal/state"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

// selection is text being drag-selected in the message pane. Rows index
// windowRows; columns count cells from the left edge of the text. Like
// mIRC, letting go of the mouse copies the text and clears the selection.
type selection struct {
	win            *state.Window
	ar, ac, br, bc int // anchor and current end
	moved          bool
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
	var lines []string
	var cur []rune
	started := false
	for ri, r := range rows {
		from, to, ok := s.span(ri)
		if !ok || r.marker {
			continue
		}
		if started && !r.cont {
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
		var cmd *exec.Cmd
		switch {
		case runtime.GOOS == "darwin":
			cmd = exec.Command("pbcopy")
		case runtime.GOOS == "windows":
			cmd = exec.Command("clip")
		case os.Getenv("WAYLAND_DISPLAY") != "":
			cmd = exec.Command("wl-copy")
		default:
			cmd = exec.Command("xclip", "-selection", "clipboard")
		}
		cmd.Stdin = strings.NewReader(text)
		_ = cmd.Run()
		return nil
	}
}
