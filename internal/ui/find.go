package ui

import (
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaxRenaud/trout/internal/state"
)

// finder is Ctrl+F's search through a window's lines, like mIRC's Find
// Text: newest match first, F3 for the next older one, Shift+F3 newer.
// The match is highlighted until Esc or leaving the window.
type finder struct {
	win  *state.Window
	text string // what to look for, lowercased
	line int    // index of the matching line
}

func (a *App) openFind() {
	w := a.active
	f := &formBox{Title: "Find Text", Lines: []string{"Search " + a.winName(w) + ", newest first.",
		"F3 finds the next older match, Shift+F3 a newer one."},
		Fields:  []*formField{newField("Find:", a.findText, false)},
		Buttons: []string{"Find", "Cancel"}}
	history := a.canSearch(w.Net)
	if history {
		f.Fields = append(f.Fields, newCheck("Search the bouncer's history instead", false))
	}
	f.OnOK = func(a *App, vals []string) (tea.Cmd, error) {
		text := strings.TrimSpace(vals[0])
		if text == "" {
			return nil, errors.New("type something to find")
		}
		a.findText = text
		if history && vals[1] == "1" {
			return nil, a.searchHistory(w, text)
		}
		a.find = &finder{win: w, text: strings.ToLower(text), line: len(w.Lines)}
		if !a.findNext(-1) {
			a.find = nil
			if history {
				return nil, errors.New("not in this window; try the bouncer's history")
			}
			return nil, errors.New("can't find \"" + text + "\" in this window")
		}
		return nil, nil
	}
	a.openDialog(f)
}

// winName is how a window is named in prompts.
func (a *App) winName(w *state.Window) string {
	if w.Kind == state.KStatus {
		return "the " + w.Net.Name + " status window"
	}
	return w.Name
}

// findNext moves to the next match in direction d (-1 older, 1 newer) and
// scrolls it into the middle of the view. It reports whether there was one.
func (a *App) findNext(d int) bool {
	f := a.find
	if f == nil || f.win != a.active {
		return false
	}
	w := f.win
	for i := f.line + d; i >= 0 && i < len(w.Lines); i += d {
		if strings.Contains(strings.ToLower(stripCodes(w.Lines[i].Text)), f.text) {
			f.line = i
			a.scrollToLine(w, i)
			return true
		}
	}
	return false
}

// scrollToLine puts line i of the active window in the middle of the
// message pane.
func (a *App) scrollToLine(w *state.Window, i int) {
	if a.msgW <= 0 {
		return
	}
	rows, _ := a.windowRows(w, a.msgW)
	for r, row := range rows {
		if row.line == i && !row.marker {
			w.Scroll = len(rows) - a.msgH - max(0, r-a.msgH/2)
			return
		}
	}
}

// found reports whether row belongs to the highlighted match in win.
func (a *App) found(win *state.Window, row textRow) bool {
	return a.find != nil && a.find.win == win && !row.marker && row.line == a.find.line
}
