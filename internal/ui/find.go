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

// findTextCmd is /findtext [-hcn] [text]: like mIRC's, it goes straight
// to the next match above the last line you see (-n: below), with no
// dialog; it carries on from the last match of the same text. -c clears
// the highlighted line; -h is how trout always shows a match. Without
// text, it's Ctrl+F's dialog.
func (a *App) findTextCmd(w *state.Window, rest string) {
	d := -1
	for {
		sw, after, _ := strings.Cut(rest, " ")
		if len(sw) < 2 || sw[0] != '-' || strings.Trim(sw[1:], "hcn") != "" {
			break
		}
		if strings.Contains(sw, "n") {
			d = 1
		}
		if strings.Contains(sw, "c") {
			a.find = nil
		}
		rest = strings.TrimSpace(after)
	}
	if rest == "" {
		if a.find == nil || a.find.win != w {
			a.openFind()
		}
		return
	}
	a.findText = rest
	text := strings.ToLower(rest)
	if f := a.find; f == nil || f.win != w || f.text != text {
		start := a.lastVisibleLine(w) // searching up starts at it
		if d > 0 {
			start-- // and down, after it
		}
		a.find = &finder{win: w, text: text, line: start + 1}
	}
	if !a.findNext(d) {
		where := "above"
		if d > 0 {
			where = "below"
		}
		a.addLine(w, state.RoleInfo, "* /findtext: can't find \""+rest+"\" "+where)
		a.find = nil
	}
}

// lastVisibleLine is the index of the bottom line in w's message pane.
func (a *App) lastVisibleLine(w *state.Window) int {
	if a.msgW <= 0 || w.Scroll <= 0 {
		return len(w.Lines) - 1
	}
	rows, _ := a.windowRows(w, a.msgW)
	for r := len(rows) - 1 - w.Scroll; r >= 0; r-- {
		if !rows[r].marker {
			return rows[r].line
		}
	}
	return len(w.Lines) - 1
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
